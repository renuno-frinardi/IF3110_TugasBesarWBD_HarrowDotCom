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

func (w *Worker) executeCancelBatches(ctx context.Context, job *QueueJob, lease *LeaseHandle, target *pgx.Conn) executionOutcome {
	actorStr := strconv.Itoa(job.RequestedBy)
	dbStr := strconv.Itoa(job.DatabaseID)

	if job.Irreversible || !CanSafelyRevertBatchSQL(job.DownSQL) {
		err := fmt.Errorf("%w: cannot revert committed batches without safe range-scoped down SQL", ErrCancelUnsafe)
		return executionOutcome{Status: migration.ExecPartial, Err: err}
	}

	if err := EnsureTargetLedgerTables(ctx, target); err != nil {
		return executionOutcome{Status: migration.ExecPartiallyReverted, Err: err}
	}

	targetCPs, err := LoadTargetBatchCheckpoints(ctx, target, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecPartiallyReverted, Err: err}
	}

	metaBatches, err := w.migStore.ListExecutionBatches(ctx, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecPartiallyReverted, Err: err}
	}

	metaByNo := make(map[int]migration.ExecutionBatch, len(metaBatches))
	for _, b := range metaBatches {
		metaByNo[b.BatchNo] = b
	}
	for bNo, tcp := range targetCPs {
		if _, exists := metaByNo[bNo]; !exists && tcp.Status == "committed" {
			_ = w.migStore.RecordBatchProgress(ctx, migration.BatchResult{
				JobID: job.JobID, ExecutionID: job.ExecutionID, BatchNo: tcp.BatchNo,
				CursorFrom: tcp.CursorFrom, CursorTo: tcp.CursorTo, RowsAffected: tcp.RowsAffected, Status: "committed",
			})
		} else if exists && tcp.Status == "reverted" && metaByNo[bNo].RevertedAt == nil {
			_ = w.migStore.MarkBatchReverted(ctx, job.ExecutionID, bNo)
		}
	}

	metaBatches, err = w.migStore.ListExecutionBatches(ctx, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecPartiallyReverted, Err: err}
	}

	pendingRevert := make([]migration.ExecutionBatch, 0)
	revertedSoFar := 0
	var remainingRows int64
	for i := len(metaBatches) - 1; i >= 0; i-- {
		b := metaBatches[i]
		if tcp, ok := targetCPs[b.BatchNo]; ok && tcp.Status == "reverted" {
			if b.RevertedAt == nil {
				_ = w.migStore.MarkBatchReverted(ctx, job.ExecutionID, b.BatchNo)
			}
			revertedSoFar++
			continue
		}
		if b.Status == "reverted" || b.RevertedAt != nil {
			revertedSoFar++
			continue
		}
		pendingRevert = append(pendingRevert, b)
		remainingRows += b.RowsAffected
	}

	if len(pendingRevert) == 0 {
		return executionOutcome{
			Status:        migration.ExecFailed,
			RowsCommitted: 0,
			Err:           fmt.Errorf("batch execution cancelled and all committed batches reverted"),
		}
	}

	for _, b := range pendingRevert {
		if err := lease.CheckValid(); err != nil {
			return w.revertFailureOutcome(revertedSoFar, remainingRows, err, false)
		}

		if w.beforeRevertBatchHook != nil {
			if hookErr := w.beforeRevertBatchHook(ctx, job, b.BatchNo); hookErr != nil {
				return w.revertFailureOutcome(revertedSoFar, remainingRows, hookErr, false)
			}
		}

		downSQL, buildErr := BuildBatchDownQuery(job.DownSQL, job.BatchConfig, b.CursorFrom, b.CursorTo)
		if buildErr != nil {
			return w.revertFailureOutcome(revertedSoFar, remainingRows, buildErr, false)
		}

		tx, txErr := target.Begin(ctx)
		if txErr != nil {
			return w.revertFailureOutcome(revertedSoFar, remainingRows, txErr, false)
		}

		logEntry := telemetry.SQLLogEntry{
			Actor: actorStr, Database: dbStr, MigrationID: job.MigrationID,
			JobID: job.JobID, ExecutionID: job.ExecutionID, Attempt: job.Attempt, BatchNo: b.BatchNo,
			Query: downSQL, Params: []any{b.CursorFrom, b.CursorTo},
		}
		telemetry.LogSQLStart(ctx, w.logger, logEntry)

		qStart := time.Now()
		tag, qErr := tx.Exec(ctx, downSQL)
		logEntry.Duration = time.Since(qStart)
		if qErr == nil {
			logEntry.RowsAffected = tag.RowsAffected()
		}
		logEntry.Err = qErr
		telemetry.LogSQLResult(ctx, w.logger, logEntry)

		if qErr != nil {
			_ = tx.Rollback(context.Background())
			telemetry.LogTxOutcome(ctx, w.logger, false, logEntry)
			return w.revertFailureOutcome(revertedSoFar, remainingRows, qErr, false)
		}

		if markErr := MarkTargetBatchRevertedTx(ctx, tx, job.ExecutionID, b.BatchNo); markErr != nil {
			_ = tx.Rollback(context.Background())
			telemetry.LogTxOutcome(ctx, w.logger, false, logEntry)
			return w.revertFailureOutcome(revertedSoFar, remainingRows, markErr, false)
		}

		cStart := time.Now()
		cErr := tx.Commit(ctx)
		logEntry.Duration = time.Since(cStart)
		logEntry.Err = cErr
		telemetry.LogTxOutcome(ctx, w.logger, cErr == nil, logEntry)

		if cErr != nil {
			return w.revertFailureOutcome(revertedSoFar, remainingRows, cErr, IsCommitUncertain(target, cErr))
		}

		_ = w.migStore.MarkBatchReverted(ctx, job.ExecutionID, b.BatchNo)
		revertedSoFar++
		remainingRows -= b.RowsAffected
		if remainingRows < 0 {
			remainingRows = 0
		}
	}

	return executionOutcome{
		Status:        migration.ExecFailed,
		RowsCommitted: 0,
		Err:           fmt.Errorf("batch execution cancelled and %d committed batches reverted", revertedSoFar),
	}
}

func (w *Worker) revertFailureOutcome(revertedSoFar int, remainingRows int64, err error, uncertain bool) executionOutcome {
	status := migration.ExecPartial
	if revertedSoFar > 0 {
		status = migration.ExecPartiallyReverted
	}
	return executionOutcome{
		Status:        status,
		RowsCommitted: remainingRows,
		Err:           err,
		Uncertain:     uncertain,
	}
}
