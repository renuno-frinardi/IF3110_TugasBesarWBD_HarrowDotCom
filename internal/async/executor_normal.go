package async

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

type executionOutcome struct {
	Status        string
	RowsCommitted int64
	Err           error
	Uncertain     bool
	Cancelled     bool
}

func (w *Worker) executeNormalMode(ctx context.Context, job *QueueJob, lease *LeaseHandle, target *pgx.Conn) executionOutcome {
	actorStr := strconv.Itoa(job.RequestedBy)
	dbStr := strconv.Itoa(job.DatabaseID)

	if err := EnsureTargetLedgerTables(ctx, target); err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("prepare target ledger: %w", err)}
	}

	alreadyCommitted, priorRows, err := CheckNormalExecutionProof(ctx, target, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("verify execution proof: %w", err)}
	}
	if DecideNormalIdempotency("", alreadyCommitted) == NormalDecisionSkipCompleted {
		w.logger.InfoContext(ctx, "worker.idempotency.skip_committed",
			"job_id", job.JobID,
			"execution_id", job.ExecutionID,
			"migration_id", job.MigrationID,
			"database", dbStr,
			"actor", actorStr,
			"rows", priorRows,
			"status", migration.ExecSuccess,
		)
		return executionOutcome{Status: migration.ExecSuccess, RowsCommitted: priorRows}
	}

	if err := lease.CheckValid(); err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: err}
	}

	tx, err := target.Begin(ctx)
	if err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("begin target transaction: %w", err)}
	}
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	baseEntry := telemetry.SQLLogEntry{
		Actor:       actorStr,
		Database:    dbStr,
		MigrationID: job.MigrationID,
		JobID:       job.JobID,
		ExecutionID: job.ExecutionID,
		Attempt:     job.Attempt,
		BatchNo:     0,
		Query:       job.SQL,
	}
	telemetry.LogSQLStart(ctx, w.logger, baseEntry)

	execStart := time.Now()
	tag, execErr := tx.Exec(ctx, job.SQL)
	execDur := time.Since(execStart)

	resEntry := baseEntry
	resEntry.Duration = execDur
	if execErr == nil {
		resEntry.RowsAffected = tag.RowsAffected()
	}
	resEntry.Err = execErr
	telemetry.LogSQLResult(ctx, w.logger, resEntry)

	if execErr != nil {
		rbStart := time.Now()
		_ = tx.Rollback(context.Background())
		rbEntry := baseEntry
		rbEntry.Duration = time.Since(rbStart)
		telemetry.LogTxOutcome(ctx, w.logger, false, rbEntry)

		return executionOutcome{Status: migration.ExecFailed, Err: execErr}
	}

	if err := RecordNormalExecutionProofTx(ctx, tx, job.ExecutionID, job.JobID, job.Action, tag.RowsAffected()); err != nil {
		_ = tx.Rollback(context.Background())
		telemetry.LogTxOutcome(ctx, w.logger, false, telemetry.SQLLogEntry{
			Actor: actorStr, Database: dbStr, MigrationID: job.MigrationID,
			JobID: job.JobID, ExecutionID: job.ExecutionID, Attempt: job.Attempt, Err: err,
		})
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("record target execution proof: %w", err)}
	}

	commitStart := time.Now()
	commitErr := tx.Commit(ctx)
	commitDur := time.Since(commitStart)

	txEntry := baseEntry
	txEntry.Duration = commitDur
	txEntry.RowsAffected = tag.RowsAffected()
	txEntry.Err = commitErr
	telemetry.LogTxOutcome(ctx, w.logger, commitErr == nil, txEntry)

	if commitErr != nil {
		if IsCommitUncertain(target, commitErr) {
			reconciled, rows, recErr := w.verifyUncertainNormalCommit(ctx, job.DatabaseID, job.ExecutionID)
			if recErr == nil && reconciled {
				return executionOutcome{Status: migration.ExecSuccess, RowsCommitted: rows}
			}
			if recErr == nil && !reconciled {
				return executionOutcome{Status: migration.ExecFailed, Err: commitErr}
			}
			return executionOutcome{
				Status:    migration.ExecRunning,
				Err:       fmt.Errorf("%w: %v", ErrUncertainCommit, commitErr),
				Uncertain: true,
			}
		}
		return executionOutcome{Status: migration.ExecFailed, Err: commitErr}
	}

	return executionOutcome{
		Status:        migration.ExecSuccess,
		RowsCommitted: tag.RowsAffected(),
	}
}

func (w *Worker) verifyUncertainNormalCommit(ctx context.Context, databaseID int, executionID int64) (bool, int64, error) {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()

	conn, err := w.openTargetConn(checkCtx, databaseID)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = conn.Close(checkCtx) }()

	return CheckNormalExecutionProof(checkCtx, conn, executionID)
}
