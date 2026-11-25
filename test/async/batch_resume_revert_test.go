package async_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/async"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

func TestBatchPartialFailureBlocksNextMigrationAndResumesAfterRestart(t *testing.T) {
	pool := openAsyncTestPool(t)
	fx := newDatabaseFixture(t, pool, "batch_resume")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tbl := fmt.Sprintf("batch_items_%d", time.Now().UnixNano())
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE %s (
			id INT PRIMARY KEY,
			migrated_count INT NOT NULL DEFAULT 0
		);
		INSERT INTO %s (id, migrated_count)
		SELECT g, 0 FROM generate_series(1, 250) AS g;
	`, tbl, tbl))
	if err != nil {
		t.Fatalf("seed batch table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+tbl)
	})

	silentLogger := telemetry.NewJSONLogger(io.Discard, slog.LevelInfo)
	queue := async.NewQueue(pool, silentLogger, 2)
	svc := migration.NewServiceWithPublisher(fx.MigStore, fx.MigAccess, queue)

	upSQL := fmt.Sprintf(`
		UPDATE %s SET migrated_count = migrated_count + 1
		WHERE {{batch_column}} IN (
			SELECT {{batch_column}} FROM %s
			WHERE {{batch_column}} > {{last_id}}
			ORDER BY {{batch_column}} ASC
			LIMIT {{batch_size}}
		)
	`, tbl, tbl)
	downSQL := fmt.Sprintf(`
		UPDATE %s SET migrated_count = migrated_count - 1
		WHERE {{batch_column}} BETWEEN {{range_from}} AND {{range_to}}
	`, tbl)

	batchCfg := migration.BatchConfig{Column: "id", Size: 100}
	mig := createAndApproveMigration(t, ctx, svc, fx, "Batch backfill", upSQL, downSQL, false, "", migration.ModeBatch, batchCfg)

	execRec, err := svc.ExecuteUp(ctx, fx.EditorUserID, mig.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp: %v", err)
	}

	locks1 := async.NewLockManager(pool, silentLogger, 5*time.Second)
	worker1 := async.NewWorker(pool, queue, locks1, fx.MigStore, svc, silentLogger, async.WorkerConfig{
		WorkerID:      "worker-before-restart",
		Concurrency:   1,
		PollInterval:  30 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
	})
	worker1.SetHooks(func(_ context.Context, _ *async.QueueJob, completedBatchNo int) error {
		if completedBatchNo == 2 {
			return errors.New("injected non-transient failure before batch 3")
		}
		return nil
	}, nil)

	processed, err := worker1.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("worker1.ProcessOne failed: processed=%v err=%v", processed, err)
	}

	afterCrash, err := fx.MigStore.GetExecutionByJobID(ctx, execRec.JobID)
	if err != nil {
		t.Fatalf("GetExecutionByJobID: %v", err)
	}
	if afterCrash.Status != migration.ExecPartial {
		t.Fatalf("expected status=partial after mid-batch failure, got %s", afterCrash.Status)
	}
	if afterCrash.RowsCommitted != 200 {
		t.Fatalf("expected 200 committed rows recorded on partial execution, got %d", afterCrash.RowsCommitted)
	}

	migBlocked := createAndApproveMigration(t, ctx, svc, fx, "Blocked follow-up", "SELECT 1;", "SELECT 1;", false, "", migration.ModeNormal, migration.BatchConfig{})
	if _, err := svc.ExecuteUp(ctx, fx.EditorUserID, migBlocked.ID, true, ""); !errors.Is(err, migration.ErrJobInProgress) {
		t.Fatalf("expected follow-up migration on partial database to be blocked with ErrJobInProgress, got %v", err)
	}

	if err := svc.CheckExecutionAccess(ctx, fx.OutsiderID, afterCrash); !errors.Is(err, migration.Forbidden) {
		t.Fatalf("expected outsider access check to fail with Forbidden, got %v", err)
	}
	if err := svc.CheckExecutionEditAccess(ctx, fx.ViewerUserID, afterCrash); !errors.Is(err, migration.Forbidden) {
		t.Fatalf("expected view-only user edit access check to fail with Forbidden, got %v", err)
	}

	rev, err := fx.MigStore.GetRevisionByID(ctx, afterCrash.RevisionID)
	if err != nil {
		t.Fatalf("GetRevisionByID: %v", err)
	}
	if _, err := queue.EnqueueResume(ctx, afterCrash, rev, fx.EditorUserID); err != nil {
		t.Fatalf("EnqueueResume: %v", err)
	}

	locks2 := async.NewLockManager(pool, silentLogger, 5*time.Second)
	worker2 := async.NewWorker(pool, queue, locks2, fx.MigStore, svc, silentLogger, async.WorkerConfig{
		WorkerID:      "worker-after-restart",
		Concurrency:   1,
		PollInterval:  30 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
	})
	if ok, err := worker2.ProcessOne(ctx); err != nil || !ok {
		t.Fatalf("worker2.ProcessOne resume failed: ok=%v err=%v", ok, err)
	}

	resumedExec, err := fx.MigStore.GetExecutionByJobID(ctx, execRec.JobID)
	if err != nil {
		t.Fatalf("GetExecutionByJobID after resume: %v", err)
	}
	if resumedExec.Status != migration.ExecSuccess || resumedExec.RowsCommitted != 250 {
		t.Fatalf("expected resumed job status=success rows=250, got status=%s rows=%d", resumedExec.Status, resumedExec.RowsCommitted)
	}

	var countUpdatedOnce, countDoubleUpdated int
	_ = pool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FILTER (WHERE migrated_count = 1), COUNT(*) FILTER (WHERE migrated_count > 1) FROM %s`, tbl)).Scan(&countUpdatedOnce, &countDoubleUpdated)
	if countUpdatedOnce != 250 || countDoubleUpdated != 0 {
		t.Fatalf("checkpoint resume violated exactly-once batch progression: once=%d, double=%d", countUpdatedOnce, countDoubleUpdated)
	}
}

func TestBatchCancelReverseOrderPartiallyRevertedAndUnsafeReject(t *testing.T) {
	pool := openAsyncTestPool(t)
	fx := newDatabaseFixture(t, pool, "batch_revert")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tbl := fmt.Sprintf("revert_items_%d", time.Now().UnixNano())
	logTbl := fmt.Sprintf("revert_order_%d", time.Now().UnixNano())
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE %s (id INT PRIMARY KEY, state TEXT NOT NULL DEFAULT 'v1');
		CREATE TABLE %s (seq SERIAL PRIMARY KEY, min_id INT NOT NULL, max_id INT NOT NULL);
		INSERT INTO %s (id, state) SELECT g, 'v1' FROM generate_series(1, 250) AS g;
	`, tbl, logTbl, tbl))
	if err != nil {
		t.Fatalf("create revert test tables: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s, %s", tbl, logTbl))
	})

	silentLogger := telemetry.NewJSONLogger(io.Discard, slog.LevelInfo)
	queue := async.NewQueue(pool, silentLogger, 2)
	svc := migration.NewServiceWithPublisher(fx.MigStore, fx.MigAccess, queue)

	upSQL := fmt.Sprintf(`
		UPDATE %s SET state = 'v2'
		WHERE {{batch_column}} IN (
			SELECT {{batch_column}} FROM %s
			WHERE {{batch_column}} > {{last_id}}
			ORDER BY {{batch_column}} ASC
			LIMIT {{batch_size}}
		)
	`, tbl, tbl)
	downSQL := fmt.Sprintf(`
		WITH reverted AS (
			UPDATE %s SET state = 'v1'
			WHERE {{batch_column}} BETWEEN {{range_from}} AND {{range_to}}
			RETURNING {{batch_column}} AS cid
		)
		INSERT INTO %s (min_id, max_id)
		SELECT MIN(cid), MAX(cid) FROM reverted
	`, tbl, logTbl)

	cfg := migration.BatchConfig{Column: "id", Size: 100}
	mig := createAndApproveMigration(t, ctx, svc, fx, "Reversible batch op", upSQL, downSQL, false, "", migration.ModeBatch, cfg)
	execRec, err := svc.ExecuteUp(ctx, fx.EditorUserID, mig.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp: %v", err)
	}

	locks := async.NewLockManager(pool, silentLogger, 5*time.Second)
	worker := async.NewWorker(pool, queue, locks, fx.MigStore, svc, silentLogger, async.WorkerConfig{
		WorkerID:      "worker-revert",
		Concurrency:   1,
		PollInterval:  30 * time.Millisecond,
		LeaseTTL:      5 * time.Second,
		EncryptionKey: testEncryptionKey,
	})

	worker.SetHooks(func(_ context.Context, _ *async.QueueJob, completedBatchNo int) error {
		if completedBatchNo == 2 {
			return errors.New("stop after batch 2")
		}
		return nil
	}, nil)
	_, _ = worker.ProcessOne(ctx)

	partialExec, _ := fx.MigStore.GetExecutionByJobID(ctx, execRec.JobID)
	if partialExec.Status != migration.ExecPartial {
		t.Fatalf("expected partial status, got %s", partialExec.Status)
	}

	rev, _ := fx.MigStore.GetRevisionByID(ctx, partialExec.RevisionID)
	if _, err := queue.EnqueueCancelBatches(ctx, partialExec, rev, fx.EditorUserID); err != nil {
		t.Fatalf("EnqueueCancelBatches: %v", err)
	}

	worker.SetHooks(nil, func(_ context.Context, _ *async.QueueJob, revertingBatchNo int) error {
		if revertingBatchNo == 1 {
			return errors.New("injected failure partway through batch cancellation")
		}
		return nil
	})
	_, _ = worker.ProcessOne(ctx)

	partRevExec, _ := fx.MigStore.GetExecutionByJobID(ctx, execRec.JobID)
	if partRevExec.Status != migration.ExecPartiallyReverted {
		t.Fatalf("expected status=partially_reverted when revert fails midway, got %s", partRevExec.Status)
	}

	worker.SetHooks(nil, nil)
	if _, err := queue.EnqueueResume(ctx, partRevExec, rev, fx.EditorUserID); err != nil {
		t.Fatalf("EnqueueResume on partially_reverted: %v", err)
	}
	_, _ = worker.ProcessOne(ctx)

	finalExec, _ := fx.MigStore.GetExecutionByJobID(ctx, execRec.JobID)
	if finalExec.Status != migration.ExecFailed || finalExec.RowsCommitted != 0 {
		t.Fatalf("expected fully reverted batch job to end with status=failed rows_committed=0, got status=%s rows=%d", finalExec.Status, finalExec.RowsCommitted)
	}

	rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT min_id, max_id FROM %s ORDER BY seq ASC`, logTbl))
	if err != nil {
		t.Fatalf("query revert log: %v", err)
	}
	defer rows.Close()
	var ranges [][2]int
	for rows.Next() {
		var lo, hi int
		_ = rows.Scan(&lo, &hi)
		ranges = append(ranges, [2]int{lo, hi})
	}
	if len(ranges) != 2 || ranges[0] != [2]int{101, 200} || ranges[1] != [2]int{1, 100} {
		t.Fatalf("expected reverse batch revert order [[101 200] [1 100]], got %v", ranges)
	}

	migIrrev := createAndApproveMigration(t, ctx, svc, fx, "Irreversible batch", upSQL, "", true, "one-way hash", migration.ModeBatch, cfg)
	irrevExec, err := svc.ExecuteUp(ctx, fx.EditorUserID, migIrrev.ID, true, "")
	if err != nil {
		t.Fatalf("ExecuteUp irreversible: %v", err)
	}
	irrevRev, _ := fx.MigStore.GetRevisionByID(ctx, irrevExec.RevisionID)
	if _, err := queue.EnqueueCancelBatches(ctx, irrevExec, irrevRev, fx.EditorUserID); !errors.Is(err, async.ErrCancelUnsafe) {
		t.Fatalf("expected EnqueueCancelBatches on irreversible migration to fail with ErrCancelUnsafe, got %v", err)
	}
}
