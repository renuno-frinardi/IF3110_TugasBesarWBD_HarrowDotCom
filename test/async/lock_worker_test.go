package async_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/async"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

func TestCrossWorkerDatabaseLockSerialOnSameDBAndParallelAcrossDBs(t *testing.T) {
	pool := openAsyncTestPool(t)
	fx1 := newDatabaseFixture(t, pool, "db1")
	fx2 := newDatabaseFixture(t, pool, "db2")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tbl1 := fmt.Sprintf("lock_tbl1_%d", time.Now().UnixNano())
	tbl2 := fmt.Sprintf("lock_tbl2_%d", time.Now().UnixNano())
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE %s (id INT PRIMARY KEY, v INT);
		CREATE TABLE %s (id INT PRIMARY KEY, v INT);
	`, tbl1, tbl2))
	if err != nil {
		t.Fatalf("create test tables: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s, %s", tbl1, tbl2))
	})

	silentLogger := telemetry.NewJSONLogger(io.Discard, slog.LevelInfo)
	queue := async.NewQueue(pool, silentLogger, 3)
	svc := migration.NewServiceWithPublisher(fx1.MigStore, fx1.MigAccess, queue)

	locks1 := async.NewLockManager(pool, silentLogger, 5*time.Second)
	locks2 := async.NewLockManager(pool, silentLogger, 5*time.Second)

	workerA := async.NewWorker(pool, queue, locks1, fx1.MigStore, svc, silentLogger, async.WorkerConfig{
		WorkerID:      "worker-A",
		Concurrency:   1,
		PollInterval:  40 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
	})
	workerB := async.NewWorker(pool, queue, locks2, fx1.MigStore, svc, silentLogger, async.WorkerConfig{
		WorkerID:      "worker-B",
		Concurrency:   1,
		PollInterval:  40 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
	})

	runCtx, stopWorkers := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = workerA.Run(runCtx) }()
	go func() { defer wg.Done(); _ = workerB.Run(runCtx) }()
	defer func() {
		stopWorkers()
		wg.Wait()
	}()

	migDB1 := createAndApproveMigration(t, ctx, svc, fx1, "DB1 slow op",
		fmt.Sprintf("SELECT pg_sleep(0.3); INSERT INTO %s (id, v) VALUES (1, 10);", tbl1),
		fmt.Sprintf("DELETE FROM %s WHERE id = 1;", tbl1),
		false, "", migration.ModeNormal, migration.BatchConfig{})
	migDB2 := createAndApproveMigration(t, ctx, svc, fx2, "DB2 slow op",
		fmt.Sprintf("SELECT pg_sleep(0.3); INSERT INTO %s (id, v) VALUES (1, 20);", tbl2),
		fmt.Sprintf("DELETE FROM %s WHERE id = 1;", tbl2),
		false, "", migration.ModeNormal, migration.BatchConfig{})

	parStart := time.Now()
	exec1, err := svc.ExecuteUp(ctx, fx1.EditorUserID, migDB1.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp DB1: %v", err)
	}
	exec2, err := svc.ExecuteUp(ctx, fx2.EditorUserID, migDB2.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp DB2: %v", err)
	}

	res1 := waitForJobTerminal(t, ctx, fx1.MigStore, exec1.JobID, 10*time.Second)
	res2 := waitForJobTerminal(t, ctx, fx2.MigStore, exec2.JobID, 10*time.Second)
	parElapsed := time.Since(parStart)

	if res1.Status != migration.ExecSuccess || res2.Status != migration.ExecSuccess {
		t.Fatalf("expected both parallel DB jobs to succeed, got %s and %s", res1.Status, res2.Status)
	}
	if res1.StartedAt != nil && res2.FinishedAt != nil && res2.StartedAt != nil && res1.FinishedAt != nil {
		overlapped := res1.StartedAt.Before(*res2.FinishedAt) && res2.StartedAt.Before(*res1.FinishedAt)
		if !overlapped {
			t.Errorf("expected migrations on different databases to overlap in time (elapsed=%v)", parElapsed)
		}
	}

	leaseA, err := locks1.Acquire(ctx, fx1.DatabaseID, "worker-A", "manual-job-1", exec1.ID, async.JobKindExecute)
	if err != nil {
		t.Fatalf("worker-A Acquire failed: %v", err)
	}
	if _, err := locks2.Acquire(ctx, fx1.DatabaseID, "worker-B", "manual-job-2", exec2.ID, async.JobKindExecute); !errors.Is(err, async.ErrDatabaseLocked) {
		t.Fatalf("expected worker-B Acquire on locked DB to fail with ErrDatabaseLocked, got %v", err)
	}
	if err := leaseA.Release(ctx); err != nil {
		t.Fatalf("leaseA.Release failed: %v", err)
	}
}

func TestExpiredLeaseRecoveryAfterWorkerCrash(t *testing.T) {
	pool := openAsyncTestPool(t)
	fx := newDatabaseFixture(t, pool, "lease_crash")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	silentLogger := telemetry.NewJSONLogger(io.Discard, slog.LevelInfo)
	queue := async.NewQueue(pool, silentLogger, 3)
	svc := migration.NewServiceWithPublisher(fx.MigStore, fx.MigAccess, queue)

	mig := createAndApproveMigration(t, ctx, svc, fx, "Lease recovery test",
		"SELECT 1;", "SELECT 1;", false, "", migration.ModeNormal, migration.BatchConfig{})
	execRec, err := svc.ExecuteUp(ctx, fx.EditorUserID, mig.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO database_migration_locks (
			database_id, holder_worker_id, job_id, execution_id, fencing_token, state, acquired_at, heartbeat_at, lease_until
		) VALUES ($1, 'crashed-worker', $2, $3, 4, 'active', NOW() - INTERVAL '10 seconds', NOW() - INTERVAL '10 seconds', NOW() - INTERVAL '1 second')
	`, fx.DatabaseID, execRec.JobID, execRec.ID)
	if err != nil {
		t.Fatalf("seed expired lock: %v", err)
	}

	locks := async.NewLockManager(pool, silentLogger, 5*time.Second)
	recoveredLease, err := locks.Acquire(ctx, fx.DatabaseID, "survivor-worker", execRec.JobID, execRec.ID, async.JobKindExecute)
	if err != nil {
		t.Fatalf("expected survivor-worker to recover expired lease, got: %v", err)
	}
	if recoveredLease.FencingToken != 5 {
		t.Errorf("expected fencing token to increment from 4 to 5, got %d", recoveredLease.FencingToken)
	}
	_ = recoveredLease.Release(ctx)
}
