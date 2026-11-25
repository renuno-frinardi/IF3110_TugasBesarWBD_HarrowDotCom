package async

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

type NormalIdempotencyDecision int

const (
	NormalDecisionExecute NormalIdempotencyDecision = iota
	NormalDecisionSkipCompleted
)

type BatchIdempotencyDecision int

const (
	BatchDecisionExecute BatchIdempotencyDecision = iota
	BatchDecisionSkipAlreadyRecorded
	BatchDecisionSyncMetadataOnly
)

func DecideNormalIdempotency(metadataStatus string, targetLedgerExists bool) NormalIdempotencyDecision {
	if metadataStatus == migration.ExecSuccess || targetLedgerExists {
		return NormalDecisionSkipCompleted
	}
	return NormalDecisionExecute
}

func DecideBatchIdempotency(targetCP *TargetBatchCheckpoint, metadataCommitted bool) BatchIdempotencyDecision {
	if targetCP != nil && targetCP.Status == "committed" {
		if metadataCommitted {
			return BatchDecisionSkipAlreadyRecorded
		}
		return BatchDecisionSyncMetadataOnly
	}
	if metadataCommitted {
		return BatchDecisionSkipAlreadyRecorded
	}
	return BatchDecisionExecute
}

func EnsureTargetLedgerTables(ctx context.Context, target *pgx.Conn) error {
	_, err := target.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _harrow_migration_ledger (
			execution_id BIGINT PRIMARY KEY,
			job_id TEXT NOT NULL,
			action TEXT NOT NULL,
			rows_affected BIGINT NOT NULL DEFAULT 0,
			committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS _harrow_batch_checkpoints (
			execution_id BIGINT NOT NULL,
			batch_no INT NOT NULL,
			cursor_from TEXT NOT NULL DEFAULT '',
			cursor_to TEXT NOT NULL DEFAULT '',
			rows_affected BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'committed',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (execution_id, batch_no),
			CHECK (status IN ('committed', 'reverted'))
		);
	`)
	return err
}

func CheckNormalExecutionProof(ctx context.Context, target *pgx.Conn, executionID int64) (bool, int64, error) {
	var rows int64
	err := target.QueryRow(ctx, `
		SELECT rows_affected FROM _harrow_migration_ledger WHERE execution_id = $1
	`, executionID).Scan(&rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, rows, nil
}

func RecordNormalExecutionProofTx(ctx context.Context, tx pgx.Tx, executionID int64, jobID, action string, rowsAffected int64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO _harrow_migration_ledger (execution_id, job_id, action, rows_affected, committed_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (execution_id) DO NOTHING
	`, executionID, jobID, action, rowsAffected)
	return err
}

func LoadTargetBatchCheckpoints(ctx context.Context, target *pgx.Conn, executionID int64) (map[int]TargetBatchCheckpoint, error) {
	rows, err := target.Query(ctx, `
		SELECT execution_id, batch_no, cursor_from, cursor_to, rows_affected, status, updated_at
		FROM _harrow_batch_checkpoints
		WHERE execution_id = $1
		ORDER BY batch_no ASC
	`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]TargetBatchCheckpoint)
	for rows.Next() {
		var cp TargetBatchCheckpoint
		if err := rows.Scan(&cp.ExecutionID, &cp.BatchNo, &cp.CursorFrom, &cp.CursorTo, &cp.RowsAffected, &cp.Status, &cp.UpdatedAt); err != nil {
			return nil, err
		}
		out[cp.BatchNo] = cp
	}
	return out, rows.Err()
}

func SaveTargetBatchCheckpointTx(ctx context.Context, tx pgx.Tx, cp TargetBatchCheckpoint) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO _harrow_batch_checkpoints (execution_id, batch_no, cursor_from, cursor_to, rows_affected, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'committed', NOW())
		ON CONFLICT (execution_id, batch_no) DO UPDATE SET
			cursor_from = EXCLUDED.cursor_from,
			cursor_to = EXCLUDED.cursor_to,
			rows_affected = EXCLUDED.rows_affected,
			status = 'committed',
			updated_at = NOW()
	`, cp.ExecutionID, cp.BatchNo, cp.CursorFrom, cp.CursorTo, cp.RowsAffected)
	return err
}

func MarkTargetBatchRevertedTx(ctx context.Context, tx pgx.Tx, executionID int64, batchNo int) error {
	_, err := tx.Exec(ctx, `
		UPDATE _harrow_batch_checkpoints
		SET status = 'reverted', updated_at = NOW()
		WHERE execution_id = $1 AND batch_no = $2
	`, executionID, batchNo)
	return err
}
