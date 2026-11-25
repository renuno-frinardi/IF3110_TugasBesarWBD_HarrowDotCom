package async

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func (w *Worker) executeBatchMode(ctx context.Context, job *QueueJob, lease *LeaseHandle, target *pgx.Conn) executionOutcome {
	if job.Action == migration.ActionDown {
		return w.executeFullBatchRollback(ctx, job, lease, target)
	}

	ctx, span := telemetry.Tracer("worker").Start(ctx, "migration.batch_execution")
	defer span.End()

	actorStr := strconv.Itoa(job.RequestedBy)
	dbStr := strconv.Itoa(job.DatabaseID)

	if err := EnsureTargetLedgerTables(ctx, target); err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("prepare target checkpoint table: %w", err)}
	}

	targetCPs, err := LoadTargetBatchCheckpoints(ctx, target, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("load target batch checkpoints: %w", err)}
	}
	metaBatches, err := w.migStore.ListExecutionBatches(ctx, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: fmt.Errorf("load metadata batches: %w", err)}
	}

	metaMap := make(map[int]migration.ExecutionBatch, len(metaBatches))
	for _, b := range metaBatches {
		metaMap[b.BatchNo] = b
	}

	var totalRowsCommitted int64
	committedBatches := 0
	lastCursor := ""
	nextBatchNo := 1

	maxSeenBatch := 0
	for bNo := range targetCPs {
		if bNo > maxSeenBatch {
			maxSeenBatch = bNo
		}
	}
	for bNo := range metaMap {
		if bNo > maxSeenBatch {
			maxSeenBatch = bNo
		}
	}

	for bNo := 1; bNo <= maxSeenBatch; bNo++ {
		var tcpPtr *TargetBatchCheckpoint
		if tcp, ok := targetCPs[bNo]; ok {
			tcpCopy := tcp
			tcpPtr = &tcpCopy
		}
		mb, metaExists := metaMap[bNo]
		metaCommitted := metaExists && mb.Status == "committed" && mb.RevertedAt == nil

		decision := DecideBatchIdempotency(tcpPtr, metaCommitted)
		if decision == BatchDecisionSyncMetadataOnly && tcpPtr != nil {
			_ = w.migStore.RecordBatchProgress(ctx, migration.BatchResult{
				JobID:        job.JobID,
				ExecutionID:  job.ExecutionID,
				BatchNo:      tcpPtr.BatchNo,
				CursorFrom:   tcpPtr.CursorFrom,
				CursorTo:     tcpPtr.CursorTo,
				RowsAffected: tcpPtr.RowsAffected,
				Status:       "committed",
			})
			committedBatches++
			totalRowsCommitted += tcpPtr.RowsAffected
			lastCursor = tcpPtr.CursorTo
			nextBatchNo = bNo + 1
		} else if decision == BatchDecisionSkipAlreadyRecorded {
			committedBatches++
			if tcpPtr != nil {
				totalRowsCommitted += tcpPtr.RowsAffected
				lastCursor = tcpPtr.CursorTo
			} else {
				totalRowsCommitted += mb.RowsAffected
				lastCursor = mb.CursorTo
			}
			nextBatchNo = bNo + 1
		}
	}

	for {
		if err := lease.CheckValid(); err != nil {
			return w.batchFailureOutcome(committedBatches, totalRowsCommitted, err, false)
		}

		cancelled, _ := w.queue.IsCancelRequested(ctx, job.ID)
		if cancelled {
			return executionOutcome{
				Status:        batchStatusWhenStopped(committedBatches),
				RowsCommitted: totalRowsCommitted,
				Cancelled:     true,
			}
		}

		if w.afterBatchHook != nil && nextBatchNo > 1 {
			if hookErr := w.afterBatchHook(ctx, job, nextBatchNo-1); hookErr != nil {
				return w.batchFailureOutcome(committedBatches, totalRowsCommitted, hookErr, false)
			}
		}

		batchCtx, batchSpan := telemetry.Tracer("worker").Start(ctx, "migration.batch")
		batchSpan.SetAttributes(
			attribute.Int("batch.no", nextBatchNo),
			attribute.String("batch.last_cursor", lastCursor),
			attribute.Int("batch.size", job.BatchConfig.Size),
		)

		wrappedSQL, renderedDML, buildErr := BuildBatchUpQuery(job.SQL, job.BatchConfig, lastCursor)
		if buildErr != nil {
			batchSpan.RecordError(buildErr)
			batchSpan.SetStatus(codes.Error, buildErr.Error())
			batchSpan.End()
			return w.batchFailureOutcome(committedBatches, totalRowsCommitted, buildErr, false)
		}

		tx, txErr := target.Begin(batchCtx)
		if txErr != nil {
			batchSpan.RecordError(txErr)
			batchSpan.SetStatus(codes.Error, txErr.Error())
			batchSpan.End()
			return w.batchFailureOutcome(committedBatches, totalRowsCommitted, txErr, false)
		}

		logBase := telemetry.SQLLogEntry{
			Actor:       actorStr,
			Database:    dbStr,
			MigrationID: job.MigrationID,
			JobID:       job.JobID,
			ExecutionID: job.ExecutionID,
			Attempt:     job.Attempt,
			BatchNo:     nextBatchNo,
			Query:       renderedDML,
			Params:      []any{lastCursor, job.BatchConfig.Size},
		}
		telemetry.LogSQLStart(batchCtx, w.logger, logBase)

		qStart := time.Now()
		var rowsAffected int64
		var cursorFrom, cursorTo string
		qErr := tx.QueryRow(batchCtx, wrappedSQL).Scan(&rowsAffected, &cursorFrom, &cursorTo)
		qDur := time.Since(qStart)

		resLog := logBase
		resLog.Duration = qDur
		resLog.RowsAffected = rowsAffected
		resLog.Err = qErr
		telemetry.LogSQLResult(batchCtx, w.logger, resLog)

		if qErr != nil {
			rbStart := time.Now()
			_ = tx.Rollback(context.Background())
			rbLog := logBase
			rbLog.Duration = time.Since(rbStart)
			telemetry.LogTxOutcome(batchCtx, w.logger, false, rbLog)

			batchSpan.RecordError(qErr)
			batchSpan.SetStatus(codes.Error, qErr.Error())
			batchSpan.End()
			return w.batchFailureOutcome(committedBatches, totalRowsCommitted, qErr, false)
		}

		if rowsAffected == 0 {
			rbStart := time.Now()
			_ = tx.Rollback(context.Background())
			rbLog := logBase
			rbLog.Duration = time.Since(rbStart)
			rbLog.Status = "empty_batch_done"
			telemetry.LogTxOutcome(batchCtx, w.logger, false, rbLog)
			batchSpan.End()
			break
		}

		cp := TargetBatchCheckpoint{
			ExecutionID:  job.ExecutionID,
			BatchNo:      nextBatchNo,
			CursorFrom:   cursorFrom,
			CursorTo:     cursorTo,
			RowsAffected: rowsAffected,
			Status:       "committed",
		}
		if cpErr := SaveTargetBatchCheckpointTx(batchCtx, tx, cp); cpErr != nil {
			_ = tx.Rollback(context.Background())
			telemetry.LogTxOutcome(batchCtx, w.logger, false, telemetry.SQLLogEntry{
				Actor: actorStr, Database: dbStr, MigrationID: job.MigrationID,
				JobID: job.JobID, ExecutionID: job.ExecutionID, Attempt: job.Attempt, BatchNo: nextBatchNo, Err: cpErr,
			})
			batchSpan.RecordError(cpErr)
			batchSpan.SetStatus(codes.Error, cpErr.Error())
			batchSpan.End()
			return w.batchFailureOutcome(committedBatches, totalRowsCommitted, cpErr, false)
		}

		commitStart := time.Now()
		commitErr := tx.Commit(batchCtx)
		commitDur := time.Since(commitStart)

		txLog := logBase
		txLog.Duration = commitDur
		txLog.RowsAffected = rowsAffected
		txLog.Err = commitErr
		telemetry.LogTxOutcome(batchCtx, w.logger, commitErr == nil, txLog)

		if commitErr != nil {
			batchSpan.RecordError(commitErr)
			batchSpan.SetStatus(codes.Error, commitErr.Error())
			batchSpan.End()

			if IsCommitUncertain(target, commitErr) {
				verifiedCP, verErr := w.verifyUncertainBatchCommit(ctx, job.DatabaseID, job.ExecutionID, nextBatchNo)
				if verErr == nil && verifiedCP != nil {
					_ = w.migStore.RecordBatchProgress(ctx, migration.BatchResult{
						JobID: job.JobID, ExecutionID: job.ExecutionID, BatchNo: nextBatchNo,
						CursorFrom: verifiedCP.CursorFrom, CursorTo: verifiedCP.CursorTo,
						RowsAffected: verifiedCP.RowsAffected, Status: "committed",
					})
					committedBatches++
					totalRowsCommitted += verifiedCP.RowsAffected
					return w.batchFailureOutcome(committedBatches, totalRowsCommitted, commitErr, false)
				}
				if verErr != nil {
					return w.batchFailureOutcome(committedBatches, totalRowsCommitted, fmt.Errorf("%w: %v", ErrUncertainCommit, commitErr), true)
				}
			}
			return w.batchFailureOutcome(committedBatches, totalRowsCommitted, commitErr, false)
		}

		_ = w.migStore.RecordBatchProgress(ctx, migration.BatchResult{
			JobID:        job.JobID,
			ExecutionID:  job.ExecutionID,
			BatchNo:      nextBatchNo,
			CursorFrom:   cursorFrom,
			CursorTo:     cursorTo,
			RowsAffected: rowsAffected,
			Status:       "committed",
		})

		committedBatches++
		totalRowsCommitted += rowsAffected
		lastCursor = cursorTo
		batchSpan.SetAttributes(
			attribute.Int64("batch.rows_affected", rowsAffected),
			attribute.String("batch.cursor_to", cursorTo),
		)
		batchSpan.End()
		nextBatchNo++

	}

	return executionOutcome{
		Status:        migration.ExecSuccess,
		RowsCommitted: totalRowsCommitted,
	}
}

func batchStatusWhenStopped(committedBatches int) string {
	if committedBatches > 0 {
		return migration.ExecPartial
	}
	return migration.ExecFailed
}

func (w *Worker) batchFailureOutcome(committedBatches int, rowsCommitted int64, err error, uncertain bool) executionOutcome {
	status := migration.ExecFailed
	if committedBatches > 0 {
		status = migration.ExecPartial
	}
	return executionOutcome{
		Status:        status,
		RowsCommitted: rowsCommitted,
		Err:           err,
		Uncertain:     uncertain,
	}
}

func (w *Worker) verifyUncertainBatchCommit(ctx context.Context, databaseID int, executionID int64, batchNo int) (*TargetBatchCheckpoint, error) {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()

	conn, err := w.openTargetConn(checkCtx, databaseID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(checkCtx) }()

	cps, err := LoadTargetBatchCheckpoints(checkCtx, conn, executionID)
	if err != nil {
		return nil, err
	}
	if cp, ok := cps[batchNo]; ok && cp.Status == "committed" {
		return &cp, nil
	}
	return nil, nil
}

func (w *Worker) executeFullBatchRollback(ctx context.Context, job *QueueJob, lease *LeaseHandle, target *pgx.Conn) executionOutcome {
	execRecord, err := w.migStore.GetExecution(ctx, job.ExecutionID)
	if err != nil || execRecord.RollbackOf == nil {
		return w.executeNormalMode(ctx, job, lease, target)
	}
	parentBatches, err := w.migStore.ListExecutionBatches(ctx, *execRecord.RollbackOf)
	if err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: err}
	}
	if len(parentBatches) == 0 {
		return executionOutcome{Status: migration.ExecSuccess, RowsCommitted: 0}
	}
	if err := EnsureTargetLedgerTables(ctx, target); err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: err}
	}

	existingCPs, err := LoadTargetBatchCheckpoints(ctx, target, job.ExecutionID)
	if err != nil {
		return executionOutcome{Status: migration.ExecFailed, Err: err}
	}

	actorStr := strconv.Itoa(job.RequestedBy)
	dbStr := strconv.Itoa(job.DatabaseID)
	var totalReverted int64
	revertedCount := 0

	for i := len(parentBatches) - 1; i >= 0; i-- {
		pb := parentBatches[i]
		if pb.Status != "committed" {
			continue
		}
		stepNo := len(parentBatches) - i
		if cp, ok := existingCPs[stepNo]; ok && cp.Status == "committed" {
			revertedCount++
			totalReverted += cp.RowsAffected
			continue
		}
		if err := lease.CheckValid(); err != nil {
			return w.batchFailureOutcome(revertedCount, totalReverted, err, false)
		}

		downSQL, buildErr := BuildBatchDownQuery(job.SQL, job.BatchConfig, pb.CursorFrom, pb.CursorTo)
		if buildErr != nil {
			return w.batchFailureOutcome(revertedCount, totalReverted, buildErr, false)
		}

		tx, txErr := target.Begin(ctx)
		if txErr != nil {
			return w.batchFailureOutcome(revertedCount, totalReverted, txErr, false)
		}

		logEntry := telemetry.SQLLogEntry{
			Actor: actorStr, Database: dbStr, MigrationID: job.MigrationID,
			JobID: job.JobID, ExecutionID: job.ExecutionID, Attempt: job.Attempt, BatchNo: stepNo,
			Query: downSQL, Params: []any{pb.CursorFrom, pb.CursorTo},
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
			return w.batchFailureOutcome(revertedCount, totalReverted, qErr, false)
		}

		cp := TargetBatchCheckpoint{
			ExecutionID: job.ExecutionID, BatchNo: stepNo,
			CursorFrom: pb.CursorFrom, CursorTo: pb.CursorTo,
			RowsAffected: tag.RowsAffected(), Status: "committed",
		}
		if cpErr := SaveTargetBatchCheckpointTx(ctx, tx, cp); cpErr != nil {
			_ = tx.Rollback(context.Background())
			telemetry.LogTxOutcome(ctx, w.logger, false, logEntry)
			return w.batchFailureOutcome(revertedCount, totalReverted, cpErr, false)
		}

		cStart := time.Now()
		cErr := tx.Commit(ctx)
		logEntry.Duration = time.Since(cStart)
		logEntry.Err = cErr
		telemetry.LogTxOutcome(ctx, w.logger, cErr == nil, logEntry)
		if cErr != nil {
			return w.batchFailureOutcome(revertedCount, totalReverted, cErr, IsCommitUncertain(target, cErr))
		}

		_ = w.migStore.RecordBatchProgress(ctx, migration.BatchResult{
			JobID: job.JobID, ExecutionID: job.ExecutionID, BatchNo: stepNo,
			CursorFrom: pb.CursorFrom, CursorTo: pb.CursorTo, RowsAffected: tag.RowsAffected(), Status: "committed",
		})
		revertedCount++
		totalReverted += tag.RowsAffected()
	}

	return executionOutcome{Status: migration.ExecSuccess, RowsCommitted: totalReverted}
}
