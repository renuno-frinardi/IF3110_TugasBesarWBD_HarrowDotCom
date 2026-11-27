package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/async"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

func main() {
	rowsFlag := flag.Int("rows", 600000, "number of rows to seed via generate_series")
	batchSizeFlag := flag.Int("batch-size", 50000, "batch size for batch-mode execution")
	verboseLogs := flag.Bool("verbose-logs", false, "emit worker JSON logs and OTel spans to stdout")
	flag.Parse()

	_ = godotenv.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pgURL := store.PostgresAppURL(
		envOrDefault("POSTGRES_HOST", "localhost"),
		envIntOrDefault("POSTGRES_PORT", 5433),
		envOrDefault("POSTGRES_DB", "harrow_db"),
		envOrDefault("POSTGRES_USER", "harrow"),
		os.Getenv("POSTGRES_PASSWORD"),
	)

	sqlDB, err := store.OpenDB(ctx, "postgresql", pgURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open metadata DB failed: %v\n", err)
		os.Exit(1)
	}
	if err := store.RunMigrations(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		fmt.Fprintf(os.Stderr, "migrate metadata DB failed: %v\n", err)
		os.Exit(1)
	}
	_ = sqlDB.Close()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open pgxpool failed: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	var logWriter io.Writer = io.Discard
	if *verboseLogs {
		logWriter = os.Stdout
	}

	logger := telemetry.NewJSONLogger(logWriter, slog.LevelInfo)

	var keyBytes [32]byte
	_, _ = rand.Read(keyBytes[:])
	encKey := hex.EncodeToString(keyBytes[:])

	connCfg := pool.Config().ConnConfig
	encryptedPass, err := store.EncryptSecret(connCfg.Password, encKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encrypt password failed: %v\n", err)
		os.Exit(1)
	}
	sslMode := "disable"
	if connCfg.TLSConfig != nil {
		sslMode = "require"
	}

	tag := fmt.Sprintf("bench_%d", time.Now().UnixNano())
	var dbID, userID, grpID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext, db_type, ssl_mode, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, 'postgresql', $7, TRUE) RETURNING id
	`, tag, connCfg.Host, int(connCfg.Port), connCfg.Database, connCfg.User, encryptedPass, sslMode).Scan(&dbID); err != nil {
		fmt.Fprintf(os.Stderr, "create bench connection failed: %v\n", err)
		os.Exit(1)
	}
	_ = pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, is_active) VALUES ($1, 'bench-hash', TRUE) RETURNING id`, tag+"_u").Scan(&userID)
	_ = pool.QueryRow(ctx, `INSERT INTO user_groups (name, leader_user_id) VALUES ($1, $2) RETURNING id`, tag+"_g", userID).Scan(&grpID)
	_, _ = pool.Exec(ctx, `INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)`, grpID, userID)
	_, _ = pool.Exec(ctx, `INSERT INTO group_database_access (group_id, database_connection_id, access_level) VALUES ($1, $2, 'view_edit')`, grpID, dbID)

	defer func() {
		cleanCtx, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		_, _ = pool.Exec(cleanCtx, `DROP TABLE IF EXISTS benchmark_ledger`)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM database_migration_locks WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM migration_jobs WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM executions WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM migrations WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM database_connections WHERE id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM user_groups WHERE id = $1`, grpID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM users WHERE id = $1`, userID)
	}()

	queue := async.NewQueue(pool, logger, 3)
	locks := async.NewLockManager(pool, logger, 30*time.Second)
	migStore := migration.NewStore(pool)
	migSvc := migration.NewServiceWithPublisher(migStore, migration.NewDatabaseAccess(pool), queue)
	migSvc.SetLogger(logger)

	worker := async.NewWorker(pool, queue, locks, migStore, migSvc, logger, async.WorkerConfig{
		WorkerID:      "bench-worker-1",
		Concurrency:   1,
		PollInterval:  50 * time.Millisecond,
		LeaseTTL:      30 * time.Second,
		EncryptionKey: encKey,
	})

	rowCount := *rowsFlag
	fmt.Printf("Seeding benchmark_ledger with %d rows using generate_series...\n", rowCount)
	if err := resetDataset(ctx, pool, rowCount); err != nil {
		fmt.Fprintf(os.Stderr, "seed dataset failed: %v\n", err)
		os.Exit(1)
	}

	normalUp := `UPDATE benchmark_ledger SET checksum = md5(raw_payload || account_code || md5(raw_payload)), migrated = TRUE, updated_at = NOW();`
	normalDown := `UPDATE benchmark_ledger SET checksum = '', migrated = FALSE, updated_at = NOW();`

	mNormal, _, err := migSvc.CreateWorkflowDraft(ctx, userID, dbID, "Benchmark Normal Full Update", normalUp, normalDown, false, "", migration.ModeNormal, migration.BatchConfig{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create normal draft: %v\n", err)
		os.Exit(1)
	}
	reqN, _ := migSvc.Submit(ctx, userID, mNormal.ID, normalDown, false, "", migration.ModeNormal, migration.BatchConfig{})
	_, _ = migSvc.Approve(ctx, userID, reqN.ID)

	normalStart := time.Now()
	execN, err := migSvc.ExecuteUp(ctx, userID, mNormal.ID, true, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute normal: %v\n", err)
		os.Exit(1)
	}
	_, _ = worker.ProcessOne(ctx)
	normalElapsed := time.Since(normalStart)

	resN, _ := migStore.GetExecutionByJobID(ctx, execN.JobID)
	fmt.Printf("Mode Normal -> status=%s | rows=%d | duration=%v\n", resN.Status, resN.RowsCommitted, normalElapsed.Round(time.Millisecond))

	fmt.Printf("Resetting identical %d-row dataset for Mode Batch (batch_size=%d)...\n", rowCount, *batchSizeFlag)
	if err := resetDataset(ctx, pool, rowCount); err != nil {
		fmt.Fprintf(os.Stderr, "reset dataset failed: %v\n", err)
		os.Exit(1)
	}

	batchUp := `UPDATE benchmark_ledger
SET checksum = md5(raw_payload || account_code || md5(raw_payload)), migrated = TRUE, updated_at = NOW()
WHERE {{batch_column}} IN (
    SELECT {{batch_column}} FROM benchmark_ledger
    WHERE {{batch_column}} > {{last_id}}
    ORDER BY {{batch_column}} ASC
    LIMIT {{batch_size}}
)`
	batchDown := `UPDATE benchmark_ledger
SET checksum = '', migrated = FALSE, updated_at = NOW()
WHERE {{batch_column}} BETWEEN {{range_from}} AND {{range_to}}`
	batchCfg := migration.BatchConfig{Column: "id", Size: *batchSizeFlag}

	mBatch, _, err := migSvc.CreateWorkflowDraft(ctx, userID, dbID, "Benchmark Cursor Batch Update", batchUp, batchDown, false, "", migration.ModeBatch, batchCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create batch draft: %v\n", err)
		os.Exit(1)
	}
	reqB, _ := migSvc.Submit(ctx, userID, mBatch.ID, batchDown, false, "", migration.ModeBatch, batchCfg)
	_, _ = migSvc.Approve(ctx, userID, reqB.ID)

	batchStart := time.Now()
	execB, err := migSvc.ExecuteUp(ctx, userID, mBatch.ID, true, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute batch: %v\n", err)
		os.Exit(1)
	}
	_, _ = worker.ProcessOne(ctx)
	batchElapsed := time.Since(batchStart)

	resB, _ := migStore.GetExecutionByJobID(ctx, execB.JobID)
	batches, _ := migStore.ListExecutionBatches(ctx, resB.ID)
	fmt.Printf("Mode Batch  -> status=%s | rows=%d | batches=%d | batch_size=%d | duration=%v\n",
		resB.Status, resB.RowsCommitted, len(batches), *batchSizeFlag, batchElapsed.Round(time.Millisecond))
}

func resetDataset(ctx context.Context, pool *pgxpool.Pool, rowCount int) error {
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		DROP TABLE IF EXISTS benchmark_ledger;
		CREATE TABLE benchmark_ledger (
			id BIGINT PRIMARY KEY,
			account_code TEXT NOT NULL,
			raw_payload TEXT NOT NULL,
			checksum TEXT NOT NULL DEFAULT '',
			migrated BOOLEAN NOT NULL DEFAULT FALSE,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		INSERT INTO benchmark_ledger (id, account_code, raw_payload, checksum, migrated)
		SELECT
			g,
			'ACC-' || lpad((g %% 10000)::text, 5, '0'),
			repeat(md5(g::text), 4),
			'',
			FALSE
		FROM generate_series(1, %d) AS g;
		CREATE INDEX idx_benchmark_ledger_account_migrated ON benchmark_ledger (account_code, migrated);
		ANALYZE benchmark_ledger;
	`, rowCount))
	return err
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
