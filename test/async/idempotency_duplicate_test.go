package async_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/async"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

func TestDuplicateQueueMessageAndUncertainCommitIdempotency(t *testing.T) {
	pool := openAsyncTestPool(t)
	fx := newDatabaseFixture(t, pool, "idemp_dup")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tbl := fmt.Sprintf("idemp_counter_%d", time.Now().UnixNano())
	_, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TABLE %s (n INT NOT NULL);`, tbl))
	if err != nil {
		t.Fatalf("create idemp table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+tbl)
	})

	var logBuf bytes.Buffer
	logger := telemetry.NewJSONLogger(&logBuf, slog.LevelInfo)
	queue := async.NewQueue(pool, logger, 3)
	svc := migration.NewServiceWithPublisher(fx.MigStore, fx.MigAccess, queue)
	svc.SetLogger(logger)

	mig := createAndApproveMigration(t, ctx, svc, fx, "Insert once",
		fmt.Sprintf("INSERT INTO %s (n) VALUES (1);", tbl),
		fmt.Sprintf("DELETE FROM %s WHERE n = 1;", tbl),
		false, "", migration.ModeNormal, migration.BatchConfig{})

	execRec, err := svc.ExecuteUp(ctx, fx.EditorUserID, mig.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp: %v", err)
	}

	locks := async.NewLockManager(pool, logger, 5*time.Second)
	worker := async.NewWorker(pool, queue, locks, fx.MigStore, svc, logger, async.WorkerConfig{
		WorkerID:      "worker-idemp",
		Concurrency:   1,
		PollInterval:  30 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
	})

	if ok, err := worker.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("first ProcessOne failed: ok=%v err=%v", ok, err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO migration_jobs (
			job_id, execution_id, database_id, migration_id, revision_id,
			job_kind, action, mode, sql_text, status, max_attempts
		) VALUES ($1, $2, $3, $4, $5, 'execute', 'up', 'normal', $6, 'queued', 3)
	`, execRec.JobID, execRec.ID, fx.DatabaseID, mig.ID, execRec.RevisionID, execRec.SQLExecuted)
	if err != nil {
		t.Fatalf("insert duplicate queue message: %v", err)
	}

	if ok, err := worker.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("duplicate ProcessOne failed: ok=%v err=%v", ok, err)
	}

	var rowCount int
	_ = pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", tbl)).Scan(&rowCount)
	if rowCount != 1 {
		t.Fatalf("duplicate message caused double commit: expected 1 row, got %d", rowCount)
	}

	mig2 := createAndApproveMigration(t, ctx, svc, fx, "Uncertain proof recovery",
		fmt.Sprintf("INSERT INTO %s (n) VALUES (2);", tbl),
		fmt.Sprintf("DELETE FROM %s WHERE n = 2;", tbl),
		false, "", migration.ModeNormal, migration.BatchConfig{})
	execRec2, err := svc.ExecuteUp(ctx, fx.EditorUserID, mig2.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp mig2: %v", err)
	}

	targetConn, err := migration.OpenTargetPostgresForWorker(ctx, pool, fx.DatabaseID, testEncryptionKey)
	if err != nil {
		t.Fatalf("open target conn: %v", err)
	}
	_ = async.EnsureTargetLedgerTables(ctx, targetConn)
	_, err = targetConn.Exec(ctx, `
		INSERT INTO _harrow_migration_ledger (execution_id, job_id, action, rows_affected)
		VALUES ($1, $2, 'up', 1)
	`, execRec2.ID, execRec2.JobID)
	_ = targetConn.Close(ctx)
	if err != nil {
		t.Fatalf("seed target ledger proof: %v", err)
	}

	if ok, err := worker.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("uncertain-recovery ProcessOne failed: ok=%v err=%v", ok, err)
	}
	res2, _ := fx.MigStore.GetExecutionByJobID(ctx, execRec2.JobID)
	if res2.Status != migration.ExecSuccess {
		t.Fatalf("expected ledger-proven execution to reconcile to success, got %s", res2.Status)
	}
	var count2 int
	_ = pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE n = 2", tbl)).Scan(&count2)
	if count2 != 0 {
		t.Fatalf("expected worker NOT to re-run SQL when target ledger proof already existed, got count=%d", count2)
	}
}

func TestTransientRetryRecordsAllAttemptsAndPropagatesTraceContext(t *testing.T) {
	pool := openAsyncTestPool(t)
	fx := newDatabaseFixture(t, pool, "retry_trace")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var logBuf bytes.Buffer
	var spanBuf bytes.Buffer

	t.Setenv("OTEL_EXPORTER", "stdout")
	shutdown, err := telemetry.InitTracing(ctx, "harrow-integration-test", &spanBuf)
	if err != nil {
		t.Fatalf("InitTracing: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	logger := telemetry.NewJSONLogger(&logBuf, slog.LevelInfo)
	queue := async.NewQueue(pool, logger, 3)
	svc := migration.NewServiceWithPublisher(fx.MigStore, fx.MigAccess, queue)
	svc.SetLogger(logger)

	mig := createAndApproveMigration(t, ctx, svc, fx, "Retryable migration",
		"SELECT 1;", "SELECT 1;", false, "", migration.ModeNormal, migration.BatchConfig{})

	apiCtx, apiSpan := telemetry.Tracer("api").Start(ctx, "POST /v2/migrations/execute")
	expectedTraceID := apiSpan.SpanContext().TraceID().String()
	traceHeader := telemetry.InjectTraceContext(apiCtx)

	execRec, err := svc.ExecuteUp(apiCtx, fx.EditorUserID, mig.ID, true, traceHeader)
	apiSpan.End()
	if err != nil {
		t.Fatalf("ExecuteUp: %v", err)
	}

	var dialCalls atomic.Int32
	locks := async.NewLockManager(pool, logger, 5*time.Second)
	worker := async.NewWorker(pool, queue, locks, fx.MigStore, svc, logger, async.WorkerConfig{
		WorkerID:      "worker-retry",
		Concurrency:   1,
		PollInterval:  20 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
		RetryPolicy: async.RetryPolicy{
			MaxAttempts: 3,
			BaseDelay:   30 * time.Millisecond,
			MaxDelay:    100 * time.Millisecond,
		},
		TargetDialer: func(dCtx context.Context, databaseID int) (*pgx.Conn, error) {
			if dialCalls.Add(1) == 1 {
				return nil, &pgconn.PgError{Code: "53300", Message: "sorry, too many clients already"}
			}
			return migration.OpenTargetPostgresForWorker(dCtx, pool, databaseID, testEncryptionKey)
		},
	})

	if ok, err := worker.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("attempt 1 ProcessOne: ok=%v err=%v", ok, err)
	}

	time.Sleep(50 * time.Millisecond)

	if ok, err := worker.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("attempt 2 ProcessOne: ok=%v err=%v", ok, err)
	}

	finalExec, err := fx.MigStore.GetExecutionByJobID(ctx, execRec.JobID)
	if err != nil {
		t.Fatalf("GetExecutionByJobID: %v", err)
	}
	if finalExec.Status != migration.ExecSuccess {
		t.Fatalf("expected execution to succeed on retry attempt 2, got %s", finalExec.Status)
	}

	history, err := svc.History(ctx, fx.EditorUserID, fx.DatabaseID, false)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var seenFailedAttempt1, seenSuccessAttempt2 bool
	for _, item := range history {
		if item.ExecutionID == execRec.ID && item.Attempt == 1 && item.Status == migration.ExecFailed {
			seenFailedAttempt1 = true
		}
		if item.ExecutionID == execRec.ID && item.Attempt == 2 && item.Status == migration.ExecSuccess {
			seenSuccessAttempt2 = true
		}
	}
	if !seenFailedAttempt1 || !seenSuccessAttempt2 {
		t.Fatalf("expected history to list both failed attempt 1 and succeeded attempt 2, got %+v", history)
	}

	logsText := logBuf.String()
	if !strings.Contains(logsText, expectedTraceID) {
		t.Fatalf("expected propagated trace_id %s in JSON logs:\n%s", expectedTraceID, logsText)
	}
	if connPass := pool.Config().ConnConfig.Password; connPass != "" && strings.Contains(logsText, connPass) {
		t.Fatalf("database password leaked in JSON logs!")
	}
}
