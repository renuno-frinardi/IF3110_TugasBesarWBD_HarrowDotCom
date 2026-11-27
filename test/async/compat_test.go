package async_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type asyncTestFixture struct {
	Pool         *pgxpool.Pool
	MigStore     *migration.Store
	MigAccess    *migration.DatabaseAccess
	DatabaseID   int
	EditorUserID int
	ViewerUserID int
	OutsiderID   int
	GroupID      int
	ViewerGrpID  int
}

func openAsyncTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	_ = godotenv.Load("../../.env")

	baseHost := envOr("POSTGRES_HOST", "localhost")
	basePort := envIntOr("POSTGRES_PORT", 5433)
	baseDB := envOr("POSTGRES_DB", "harrow_db")
	testDBName := envOr("POSTGRES_TEST_DB", "harrow_db_test")
	baseUser := envOr("POSTGRES_USER", "harrow")
	basePass := os.Getenv("POSTGRES_PASSWORD")

	if !strings.HasSuffix(testDBName, "_test") {
		testDBName = testDBName + "_test"
	}

	adminURL := store.PostgresAppURL(baseHost, basePort, baseDB, baseUser, basePass)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	adminConn, err := pgx.Connect(ctx, adminURL)
	cancel()
	if err != nil {
		t.Skipf("skipping integration test: PostgreSQL is not reachable (%v)", err)
	}

	var exists bool
	_ = adminConn.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, testDBName).Scan(&exists)
	if !exists {
		_, _ = adminConn.Exec(context.Background(), `CREATE DATABASE "`+strings.ReplaceAll(testDBName, `"`, `""`)+`"`)
	}
	_ = adminConn.Close(context.Background())

	testURL := store.PostgresAppURL(baseHost, basePort, testDBName, baseUser, basePass)

	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sqlDB, err := store.OpenDB(ctx, "postgresql", testURL)
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	if err := store.RunMigrations(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("failed to apply schema migrations to test DB: %v", err)
	}
	_ = sqlDB.Close()

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("failed to open pgxpool on test DB: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newDatabaseFixture(t *testing.T, pool *pgxpool.Pool, suffix string) *asyncTestFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	connCfg := pool.Config().ConnConfig
	encryptedPass, err := store.EncryptSecret(connCfg.Password, testEncryptionKey)
	if err != nil {
		t.Fatalf("encrypt test password: %v", err)
	}
	sslMode := "disable"
	if connCfg.TLSConfig != nil {
		sslMode = "require"
	}

	prefix := fmt.Sprintf("async_%s_%d", suffix, time.Now().UnixNano())
	var dbID int
	err = pool.QueryRow(ctx, `
		INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext, db_type, ssl_mode, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, 'postgresql', $7, TRUE)
		RETURNING id
	`, prefix, connCfg.Host, int(connCfg.Port), connCfg.Database, connCfg.User, encryptedPass, sslMode).Scan(&dbID)
	if err != nil {
		t.Fatalf("insert database_connections fixture: %v", err)
	}

	var editorID, viewerID, outsiderID int
	for i, role := range []string{"editor", "viewer", "outsider"} {
		var uid int
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, is_active)
			VALUES ($1, 'test-hash', TRUE) RETURNING id
		`, fmt.Sprintf("%s_%s", prefix, role)).Scan(&uid); err != nil {
			t.Fatalf("insert user %s: %v", role, err)
		}
		switch i {
		case 0:
			editorID = uid
		case 1:
			viewerID = uid
		case 2:
			outsiderID = uid
		}
	}

	var editGrpID, viewGrpID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO user_groups (name, leader_user_id) VALUES ($1, $2) RETURNING id
	`, prefix+"_edit_grp", editorID).Scan(&editGrpID); err != nil {
		t.Fatalf("insert edit group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO user_groups (name, leader_user_id) VALUES ($1, $2) RETURNING id
	`, prefix+"_view_grp", editorID).Scan(&viewGrpID); err != nil {
		t.Fatalf("insert view group: %v", err)
	}

	_, _ = pool.Exec(ctx, `INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2), ($3, $4)`, editGrpID, editorID, viewGrpID, viewerID)
	_, _ = pool.Exec(ctx, `INSERT INTO group_database_access (group_id, database_connection_id, access_level) VALUES ($1, $2, 'view_edit'), ($3, $2, 'view_only')`, editGrpID, dbID, viewGrpID)

	t.Cleanup(func() {
		cleanCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = pool.Exec(cleanCtx, `DELETE FROM database_migration_locks WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM migration_jobs WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM executions WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM migrations WHERE database_id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM database_connections WHERE id = $1`, dbID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM user_groups WHERE id IN ($1, $2)`, editGrpID, viewGrpID)
		_, _ = pool.Exec(cleanCtx, `DELETE FROM users WHERE id IN ($1, $2, $3)`, editorID, viewerID, outsiderID)
	})

	return &asyncTestFixture{
		Pool:         pool,
		MigStore:     migration.NewStore(pool),
		MigAccess:    migration.NewDatabaseAccess(pool),
		DatabaseID:   dbID,
		EditorUserID: editorID,
		ViewerUserID: viewerID,
		OutsiderID:   outsiderID,
		GroupID:      editGrpID,
		ViewerGrpID:  viewGrpID,
	}
}

func createAndApproveMigration(
	t *testing.T,
	ctx context.Context,
	svc *migration.Service,
	fx *asyncTestFixture,
	title, upSQL, downSQL string,
	irreversible bool,
	reason, mode string,
	cfg migration.BatchConfig,
) *migration.Migration {
	t.Helper()
	m, _, err := svc.CreateWorkflowDraft(ctx, fx.EditorUserID, fx.DatabaseID, title, upSQL, downSQL, irreversible, reason, mode, cfg)
	if err != nil {
		t.Fatalf("CreateWorkflowDraft(%s): %v", title, err)
	}
	req, err := svc.Submit(ctx, fx.EditorUserID, m.ID, downSQL, irreversible, reason, mode, cfg)
	if err != nil {
		t.Fatalf("Submit(%s): %v", title, err)
	}
	if _, err := svc.Approve(ctx, fx.EditorUserID, req.ID); err != nil {
		t.Fatalf("Approve(%s): %v", title, err)
	}
	return m
}

func waitForJobTerminal(t *testing.T, ctx context.Context, store *migration.Store, jobID string, timeout time.Duration) *migration.Execution {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := store.GetExecutionByJobID(ctx, jobID)
		if err == nil {
			switch e.Status {
			case migration.ExecSuccess, migration.ExecFailed, migration.ExecPartial, migration.ExecPartiallyReverted:
				return e
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	e, _ := store.GetExecutionByJobID(ctx, jobID)
	if e != nil {
		t.Fatalf("timed out waiting for job %s to finish; last status=%s", jobID, e.Status)
	}
	t.Fatalf("timed out waiting for job %s to finish", jobID)
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
