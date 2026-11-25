package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) GetLatestRevision(ctx context.Context, migrationID int) (*Revision, error) {
	r, err := s.scanRevision(s.pool.QueryRow(ctx, `SELECT id,migration_id,revision_no,up_sql,down_sql,irreversible,
	          irreversible_reason,mode,batch_config,status,content_hash,created_by,created_at,approved_at,superseded_at
              FROM migration_revisions WHERE migration_id=$1 ORDER BY revision_no DESC LIMIT 1`, migrationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) scanRevision(row pgx.Row) (*Revision, error) {
	var r Revision
	var down sql.NullString
	var approved, superseded sql.NullTime
	var cfg []byte
	err := row.Scan(&r.ID, &r.MigrationID, &r.RevisionNo, &r.UpSQL, &down, &r.Irreversible, &r.IrreversibleReason, &r.Mode, &cfg, &r.Status, &r.ContentHash, &r.CreatedBy, &r.CreatedAt, &approved, &superseded)
	if err != nil {
		return nil, err
	}
	r.DownSQL = down.String
	r.BatchConfig = decodeBatch(cfg)
	if approved.Valid {
		r.ApprovedAt = &approved.Time
	}
	if superseded.Valid {
		r.SupersededAt = &superseded.Time
	}
	return &r, nil
}

func decodeBatch(raw []byte) BatchConfig {
	var cfg BatchConfig
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

func (s *Store) GetRevisionByID(ctx context.Context, revisionID int64) (*Revision, error) {
	r, err := s.scanRevision(s.pool.QueryRow(ctx, `SELECT id,migration_id,revision_no,up_sql,down_sql,irreversible,
	          irreversible_reason,mode,batch_config,status,content_hash,created_by,created_at,approved_at,superseded_at
              FROM migration_revisions WHERE id=$1`, revisionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) UpdateDraftRevision(ctx context.Context, migrationID int, title, upSQL, downSQL string, irreversible bool, reason, mode string, cfg BatchConfig, createdBy int) (*Revision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM migrations WHERE id=$1 FOR UPDATE`, migrationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != StatusDraft {
		return nil, fmt.Errorf("%w: only draft migrations can be edited", ErrInvalidState)
	}

	var revisionID int64
	var revisionNo int
	err = tx.QueryRow(ctx, `SELECT id, revision_no FROM migration_revisions WHERE migration_id=$1 ORDER BY revision_no DESC LIMIT 1 FOR UPDATE`, migrationID).Scan(&revisionID, &revisionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		revisionNo = 1
		cfgBytes, _ := json.Marshal(cfg)
		err = tx.QueryRow(ctx, `INSERT INTO migration_revisions (migration_id,revision_no,up_sql,down_sql,irreversible,irreversible_reason,mode,batch_config,status,content_hash,created_by)
              VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,'draft',$9,$10) RETURNING id`,
			migrationID, revisionNo, upSQL, downSQL, irreversible, reason, mode, cfgBytes, revisionHash(upSQL, downSQL, irreversible, reason, mode, cfg), createdBy).Scan(&revisionID)
	} else if err == nil {
		cfgBytes, _ := json.Marshal(cfg)
		_, err = tx.Exec(ctx, `UPDATE migration_revisions SET up_sql=$2,down_sql=NULLIF($3,''),irreversible=$4,irreversible_reason=$5,mode=$6,batch_config=$7,status='draft',content_hash=$8,approved_at=NULL,superseded_at=NULL
                 WHERE id=$1`, revisionID, upSQL, downSQL, irreversible, reason, mode, cfgBytes, revisionHash(upSQL, downSQL, irreversible, reason, mode, cfg))
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE migrations SET title=$2,sql=$3,status='draft',updated_at=NOW() WHERE id=$1`, migrationID, title, upSQL); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetRevisionByID(ctx, revisionID)
}

func (s *Store) GetApprovalSnapshot(ctx context.Context, databaseID int) ([]GroupSnapshot, error) {
	rows, err := s.pool.Query(ctx, `SELECT ug.id,ug.name,ug.leader_user_id FROM user_groups ug JOIN group_database_access gda ON gda.group_id=ug.id
                 WHERE gda.database_connection_id=$1 ORDER BY ug.name,ug.id`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupSnapshot
	for rows.Next() {
		var g GroupSnapshot
		var leader sql.NullInt64
		if err := rows.Scan(&g.GroupID, &g.GroupName, &leader); err != nil {
			return nil, err
		}
		if leader.Valid {
			v := int(leader.Int64)
			g.LeaderUserID = &v
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) CreateApprovalRequest(ctx context.Context, migrationID int, revisionID int64, action string, target *int64, requestedBy int, groups []GroupSnapshot, markWorkflow bool) (*ApprovalRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var targetAny any
	if target != nil {
		targetAny = *target
	}
	req := &ApprovalRequest{MigrationID: migrationID, RevisionID: revisionID, Action: action, TargetExecutionID: target, RequestedBy: requestedBy, Status: ApprovalPending}
	err = tx.QueryRow(ctx, `INSERT INTO approval_requests (migration_id,revision_id,action,target_execution_id,requested_by,status)
          VALUES ($1,$2,$3,$4,$5,'pending') RETURNING id,created_at`, migrationID, revisionID, action, targetAny, requestedBy).Scan(&req.ID, &req.CreatedAt)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		var leader any
		if g.LeaderUserID != nil {
			leader = *g.LeaderUserID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO approval_groups (approval_request_id,group_id,group_name,leader_user_id,status) VALUES ($1,$2,$3,$4,'pending')`, req.ID, g.GroupID, g.GroupName, leader); err != nil {
			return nil, err
		}
	}
	if markWorkflow {
		if _, err := tx.Exec(ctx, `UPDATE migration_revisions SET status='pending_review' WHERE id=$1`, revisionID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE migrations SET status='pending_review',updated_at=NOW() WHERE id=$1`, migrationID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetApprovalRequest(ctx, req.ID)
}

func (s *Store) GetApprovalRequest(ctx context.Context, requestID int64) (*ApprovalRequest, error) {
	var req ApprovalRequest
	var target sql.NullInt64
	var decided sql.NullTime
	err := s.pool.QueryRow(ctx, `SELECT id,migration_id,revision_id,action,target_execution_id,requested_by,status,created_at,decided_at
           FROM approval_requests WHERE id=$1`, requestID).Scan(&req.ID, &req.MigrationID, &req.RevisionID, &req.Action, &target, &req.RequestedBy, &req.Status, &req.CreatedAt, &decided)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if target.Valid {
		v := target.Int64
		req.TargetExecutionID = &v
	}
	if decided.Valid {
		req.DecidedAt = &decided.Time
	}
	rows, err := s.pool.Query(ctx, `SELECT ag.id,ag.group_id,ag.group_name,ag.leader_user_id,u.username,ag.status,ag.approved_by,ag.approved_at
FROM approval_groups ag LEFT JOIN users u ON u.id=ag.leader_user_id WHERE ag.approval_request_id=$1 ORDER BY ag.group_name`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g ApprovalGroup
		var leader, approvedBy sql.NullInt64
		var leaderName sql.NullString
		var approvedAt sql.NullTime
		if err := rows.Scan(&g.ID, &g.GroupID, &g.GroupName, &leader, &leaderName, &g.Status, &approvedBy, &approvedAt); err != nil {
			return nil, err
		}
		if leader.Valid {
			v := int(leader.Int64)
			g.LeaderUserID = &v
		}
		if leaderName.Valid {
			v := leaderName.String
			g.LeaderUsername = &v
		}
		if approvedBy.Valid {
			v := int(approvedBy.Int64)
			g.ApprovedBy = &v
		}
		if approvedAt.Valid {
			g.ApprovedAt = &approvedAt.Time
		}
		req.Groups = append(req.Groups, g)
	}
	return &req, rows.Err()
}

func (s *Store) LatestApprovalRequest(ctx context.Context, revisionID int64, action string, target *int64) (*ApprovalRequest, error) {
	var id int64
	var targetAny any
	if target != nil {
		targetAny = *target
	}
	err := s.pool.QueryRow(ctx, `SELECT id FROM approval_requests WHERE revision_id=$1 AND action=$2 AND COALESCE(target_execution_id,0)=COALESCE($3,0)
           ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID, action, targetAny).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrApprovalRequired
	}
	if err != nil {
		return nil, err
	}
	return s.GetApprovalRequest(ctx, id)
}

func (s *Store) ApproveApprovalGroups(ctx context.Context, requestID int64, userID int) (*ApprovalRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revisionID int64
	var migrationID int
	var status, action string
	if err := tx.QueryRow(ctx, `SELECT revision_id,migration_id,status,action FROM approval_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&revisionID, &migrationID, &status, &action); err != nil {
		return nil, err
	}
	if status != ApprovalPending {
		return nil, fmt.Errorf("%w: approval request is %s", ErrInvalidState, status)
	}
	tag, err := tx.Exec(ctx, `UPDATE approval_groups SET status='approved',approved_by=$2,approved_at=NOW()
                WHERE approval_request_id=$1 AND leader_user_id=$2 AND status='pending'`, requestID, userID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, Forbidden
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM approval_groups WHERE approval_request_id=$1 AND status<> 'approved'`, requestID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending == 0 {
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status='approved',decided_at=NOW() WHERE id=$1`, requestID); err != nil {
			return nil, err
		}
		if action == ActionUp {
			if _, err := tx.Exec(ctx, `UPDATE migration_revisions SET status='approved',approved_at=NOW() WHERE id=$1`, revisionID); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE migrations SET status='approved',updated_at=NOW() WHERE id=$1`, migrationID); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetApprovalRequest(ctx, requestID)
}

func (s *Store) RevokeApprovalRequest(ctx context.Context, requestID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revisionID int64
	var migrationID int
	err = tx.QueryRow(ctx, `UPDATE approval_requests SET status='revoked',decided_at=NOW()
          WHERE id=$1 AND status IN ('pending','approved') RETURNING revision_id,migration_id`, requestID).Scan(&revisionID, &migrationID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_groups SET status='revoked' WHERE approval_request_id=$1`, requestID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE migration_revisions SET status='pending_review',approved_at=NULL WHERE id=$1`, revisionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE migrations SET status='pending_review',updated_at=NOW() WHERE id=$1`, migrationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateExecution(ctx context.Context, jobID string, migrationID int, revisionID int64, databaseID int, action, mode string, cfg BatchConfig, approval *int64, rollbackOf *int64, sqlText string, requestedBy int) (*Execution, error) {
	cfgBytes, _ := json.Marshal(cfg)
	var approvalAny, rollbackAny any
	if approval != nil {
		approvalAny = *approval
	}
	if rollbackOf != nil {
		rollbackAny = *rollbackOf
	}
	e := &Execution{JobID: jobID, MigrationID: migrationID, RevisionID: revisionID, DatabaseID: databaseID, Action: action, Mode: mode, BatchConfig: cfg, ApprovalRequestID: approval, RollbackOf: rollbackOf, SQLExecuted: sqlText, Status: ExecPending, RequestedBy: &requestedBy}
	err := s.pool.QueryRow(ctx, `INSERT INTO executions (job_id,migration_id,revision_id,database_id,action,mode,batch_config,approval_request_id,rollback_of,sql_executed,status,requested_by)
           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11) RETURNING id,created_at,updated_at`,
		jobID, migrationID, revisionID, databaseID, action, mode, cfgBytes, approvalAny, rollbackAny, sqlText, requestedBy).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Store) RecordExecutionResult(ctx context.Context, result ExecutionResult) error {
	// Always use a separate metadata transaction detached from target execution cancellation/rollback.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(recCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(recCtx) }()

	var id, migrationID int64
	var databaseID int
	var action, status, sqlExecuted string
	err = tx.QueryRow(recCtx, `SELECT id,migration_id,database_id,action,status,sql_executed FROM executions WHERE job_id=$1 FOR UPDATE`, result.JobID).Scan(&id, &migrationID, &databaseID, &action, &status, &sqlExecuted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	isTerminal := status == ExecSuccess || status == ExecFailed || status == ExecPartial || status == ExecPartiallyReverted
	if isTerminal && !result.AllowReopen {
		if status == result.Status {
			return tx.Commit(recCtx)
		}
		return ErrInvalidState
	}
	if status == ExecSuccess && result.AllowReopen {
		return ErrInvalidState
	}

	attemptNo := result.Attempt
	if attemptNo < 1 {
		attemptNo = 1
	}

	var started, finished any
	if result.StartedAt != nil {
		started = *result.StartedAt
	}
	if result.FinishedAt != nil {
		finished = *result.FinishedAt
	}

	// Record append-only attempt row (guarded by SAVEPOINT so pre-000005 test DBs remain compatible).
	if _, spErr := tx.Exec(recCtx, `SAVEPOINT sp_attempt`); spErr == nil {
		jobKind := result.JobKind
		if jobKind == "" {
			jobKind = "execute"
		}
		attemptStatus := result.Status
		if attemptStatus == ExecPending {
			attemptStatus = ExecFailed
		}
		_, attErr := tx.Exec(recCtx, `
			INSERT INTO execution_attempts (execution_id, job_id, attempt_no, job_kind, status, rows_committed, error_message, sql_executed, worker_id, started_at, finished_at)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,''), $8, NULLIF($9,''), COALESCE($10, NOW()), $11)
			ON CONFLICT (execution_id, attempt_no) DO UPDATE SET
				status = EXCLUDED.status,
				rows_committed = GREATEST(execution_attempts.rows_committed, EXCLUDED.rows_committed),
				error_message = COALESCE(EXCLUDED.error_message, execution_attempts.error_message),
				finished_at = COALESCE(EXCLUDED.finished_at, execution_attempts.finished_at)
		`, id, result.JobID, attemptNo, jobKind, attemptStatus, result.RowsCommitted, result.ErrorMessage, sqlExecuted, result.WorkerID, started, finished)
		if attErr == nil {
			_, attErr = tx.Exec(recCtx, `UPDATE executions SET attempt = GREATEST(attempt, $2) WHERE id = $1`, id, attemptNo)
		}
		if attErr != nil {
			_, _ = tx.Exec(recCtx, `ROLLBACK TO SAVEPOINT sp_attempt`)
		} else {
			_, _ = tx.Exec(recCtx, `RELEASE SAVEPOINT sp_attempt`)
		}
	}

	nextExecStatus := result.Status
	if result.RetryScheduled {
		nextExecStatus = ExecPending
		finished = nil
	}

	if result.AllowReopen && nextExecStatus == ExecRunning {
		_, err = tx.Exec(recCtx, `UPDATE executions SET status=$2,rows_committed=GREATEST(rows_committed,$3),
             error_message=NULL,started_at=COALESCE(started_at,$4,NOW()),finished_at=NULL,updated_at=NOW() WHERE id=$1`,
			id, nextExecStatus, result.RowsCommitted, started)
	} else {
		_, err = tx.Exec(recCtx, `UPDATE executions SET status=$2,rows_committed=GREATEST(rows_committed,$3),
             error_message=NULLIF($4,''),started_at=COALESCE(started_at,$5),finished_at=CASE WHEN $7 THEN NULL ELSE COALESCE($6,finished_at) END,updated_at=NOW() WHERE id=$1`,
			id, nextExecStatus, result.RowsCommitted, result.ErrorMessage, started, finished, result.RetryScheduled)
	}
	if err != nil {
		return err
	}

	if !result.RetryScheduled {
		switch nextExecStatus {
		case ExecRunning:
			if action != ActionDown {
				_, err = tx.Exec(recCtx, `UPDATE migrations SET status='running',updated_at=NOW() WHERE id=$1`, migrationID)
			}
		case ExecSuccess:
			if action == ActionDown {
				_, err = tx.Exec(recCtx, `UPDATE migrations SET status='executed',active=false,finish_time=NOW(),updated_at=NOW() WHERE id=$1`, migrationID)
			} else {
				_, err = tx.Exec(recCtx, `UPDATE migrations m SET version=COALESCE(m.version,(SELECT COALESCE(MAX(version),0)+1 FROM migrations WHERE database_id=$2)),status='executed',active=true,error_message=NULL,finish_time=NOW(),updated_at=NOW() WHERE m.id=$1`, migrationID, databaseID)
			}
		case ExecFailed:
			// Failed rollback must not alter active position or mark an active migration as failed.
			if action != ActionDown {
				_, err = tx.Exec(recCtx, `UPDATE migrations SET status='failed',error_message=NULLIF($2,''),finish_time=NOW(),updated_at=NOW() WHERE id=$1`, migrationID, result.ErrorMessage)
			}
		case ExecPartial, ExecPartiallyReverted:
			if action != ActionDown {
				_, err = tx.Exec(recCtx, `UPDATE migrations SET status='failed',error_message=NULLIF($2,''),finish_time=NOW(),updated_at=NOW() WHERE id=$1`, migrationID, result.ErrorMessage)
			}
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(recCtx)
}

func (s *Store) RecordBatchProgress(ctx context.Context, batch BatchResult) error {
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(recCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(recCtx) }()

	status := batch.Status
	if status == "" {
		status = "committed"
	}
	_, err = tx.Exec(recCtx, `
		INSERT INTO execution_batches (execution_id, batch_no, cursor_from, cursor_to, rows_affected, status, committed_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (execution_id, batch_no) DO NOTHING
	`, batch.ExecutionID, batch.BatchNo, batch.CursorFrom, batch.CursorTo, batch.RowsAffected, status)
	if err != nil {
		return err
	}

	_, err = tx.Exec(recCtx, `
		UPDATE executions
		SET rows_committed = COALESCE((
			SELECT SUM(rows_affected) FROM execution_batches
			WHERE execution_id = $1 AND status = 'committed' AND reverted_at IS NULL
		), 0),
		updated_at = NOW()
		WHERE id = $1
	`, batch.ExecutionID)
	if err != nil {
		return err
	}
	return tx.Commit(recCtx)
}

func (s *Store) MarkBatchReverted(ctx context.Context, executionID int64, batchNo int) error {
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(recCtx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(recCtx) }()

	_, err = tx.Exec(recCtx, `
		UPDATE execution_batches
		SET status = 'reverted', reverted_at = COALESCE(reverted_at, NOW())
		WHERE execution_id = $1 AND batch_no = $2
	`, executionID, batchNo)
	if err != nil {
		return err
	}

	_, err = tx.Exec(recCtx, `
		UPDATE executions
		SET rows_committed = COALESCE((
			SELECT SUM(rows_affected) FROM execution_batches
			WHERE execution_id = $1 AND status = 'committed' AND reverted_at IS NULL
		), 0),
		updated_at = NOW()
		WHERE id = $1
	`, executionID)
	if err != nil {
		return err
	}
	return tx.Commit(recCtx)
}

func (s *Store) HasBlockingExecution(ctx context.Context, databaseID int) (bool, string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM executions WHERE database_id=$1 AND status IN ('pending','running','partial','partially_reverted') ORDER BY id DESC LIMIT 1`, databaseID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, status, nil
}

const executionSelectQuery = `SELECT e.id,e.job_id,e.migration_id,e.revision_id,COALESCE(r.revision_no,1),e.database_id,e.action,e.mode,e.batch_config,e.approval_request_id,e.rollback_of,e.sql_executed,e.status,e.rows_committed,e.error_message,e.requested_by,e.started_at,e.finished_at,e.created_at,e.updated_at,m.title,m.version,u.username
              FROM executions e
              JOIN migrations m ON m.id=e.migration_id
              LEFT JOIN migration_revisions r ON r.id=e.revision_id
              LEFT JOIN users u ON u.id=e.requested_by`

func (s *Store) GetExecution(ctx context.Context, executionID int64) (*Execution, error) {
	e, err := s.scanExecution(s.pool.QueryRow(ctx, executionSelectQuery+` WHERE e.id=$1`, executionID))
	return e, err
}

func (s *Store) GetExecutionByJobID(ctx context.Context, jobID string) (*Execution, error) {
	if strings.HasPrefix(jobID, "legacy-M") {
		id, err := strconv.Atoi(strings.TrimPrefix(jobID, "legacy-M"))
		if err != nil {
			return nil, ErrNotFound
		}
		m, err := s.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		status := ExecSuccess
		if m.Status == StatusFailed {
			status = ExecFailed
		}
		e := &Execution{
			ExecutionID: int64(m.ID),
			JobID:       jobID, MigrationID: m.ID, RevisionNo: 1, DatabaseID: m.DatabaseID,
			Action: ActionUp, Attempt: 1, Mode: ModeSingle, SQLExecuted: m.SQL, ExecutedSQL: m.SQL,
			Status: status, RowsCommitted: 0, Title: m.Title,
			CreatedAt: m.CreatedAt, ExecutedAt: m.FinishTime,
		}
		if m.ErrorMessage != nil {
			e.ErrorMessage = m.ErrorMessage
		}
		e.Batches = []ExecutionBatch{}
		return e, nil
	}
	e, err := s.scanExecution(s.pool.QueryRow(ctx, executionSelectQuery+` WHERE e.job_id=$1`, jobID))
	return e, err
}

func (s *Store) scanExecution(row pgx.Row) (*Execution, error) {
	var e Execution
	var cfg []byte
	var approval, rollback, requested, version sql.NullInt64
	var errorMessage, username sql.NullString
	var started, finished sql.NullTime
	err := row.Scan(&e.ID, &e.JobID, &e.MigrationID, &e.RevisionID, &e.RevisionNo, &e.DatabaseID, &e.Action, &e.Mode, &cfg, &approval, &rollback, &e.SQLExecuted, &e.Status, &e.RowsCommitted, &errorMessage, &requested, &started, &finished, &e.CreatedAt, &e.UpdatedAt, &e.Title, &version, &username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.ExecutionID = e.ID
	e.Attempt = 1
	e.ExecutedSQL = e.SQLExecuted
	e.BatchConfig = decodeBatch(cfg)
	if approval.Valid {
		v := approval.Int64
		e.ApprovalRequestID = &v
	}
	if rollback.Valid {
		v := rollback.Int64
		e.RollbackOf = &v
	}
	if requested.Valid {
		v := int(requested.Int64)
		e.RequestedBy = &v
	}
	if version.Valid {
		v := int(version.Int64)
		e.Version = &v
	}
	if errorMessage.Valid {
		v := errorMessage.String
		e.ErrorMessage = &v
	}
	if username.Valid {
		v := username.String
		e.RequestedByName = &v
	}
	if started.Valid {
		e.StartedAt = &started.Time
		e.ExecutedAt = &started.Time
	}
	if finished.Valid {
		e.FinishedAt = &finished.Time
		e.ExecutedAt = &finished.Time
	}
	if e.ExecutedAt == nil {
		created := e.CreatedAt
		e.ExecutedAt = &created
	}
	return &e, nil
}

func (s *Store) ListExecutionBatches(ctx context.Context, executionID int64) ([]ExecutionBatch, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,execution_id,batch_no,cursor_from,cursor_to,rows_affected,status,committed_at,reverted_at
              FROM execution_batches WHERE execution_id=$1 ORDER BY batch_no`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []ExecutionBatch
	for rows.Next() {
		var b ExecutionBatch
		var reverted sql.NullTime
		if err := rows.Scan(&b.ID, &b.ExecutionID, &b.BatchNo, &b.CursorFrom, &b.CursorTo, &b.RowsAffected, &b.Status, &b.CommittedAt, &reverted); err != nil {
			return nil, err
		}
		if reverted.Valid {
			b.RevertedAt = &reverted.Time
		}
		batches = append(batches, b)
	}
	return batches, rows.Err()
}

func (s *Store) ListExecutionHistory(ctx context.Context, databaseID int, hide bool) ([]Execution, error) {
	query := executionSelectQuery + ` WHERE e.database_id=$1`
	args := []any{databaseID}
	if hide {
		query += ` AND NOT (e.action='down' AND e.status='success') AND NOT (e.id IN (SELECT rollback_of FROM executions WHERE action='down' AND status='success' AND rollback_of IS NOT NULL))`
	}
	query += ` ORDER BY e.created_at DESC,e.id DESC`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var baseExecutions []Execution
	execIDs := make([]int64, 0)
	for rows.Next() {
		e, err := s.scanExecution(rows)
		if err != nil {
			return nil, err
		}
		baseExecutions = append(baseExecutions, *e)
		execIDs = append(execIDs, e.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// Expand append-only execution_attempts so every attempt (including failed retries) remains visible.
	attemptsByExec := make(map[int64][]Execution)
	if len(execIDs) > 0 {
		attRows, attErr := s.pool.Query(ctx, `
			SELECT execution_id, attempt_no, status, rows_committed, error_message, sql_executed, started_at, finished_at
			FROM execution_attempts
			WHERE execution_id = ANY($1)
			ORDER BY execution_id DESC, attempt_no DESC
		`, execIDs)
		if attErr == nil {
			for attRows.Next() {
				var execID int64
				var attemptNo int
				var attStatus, attSQL string
				var rowsCommitted int64
				var errMsg sql.NullString
				var startedAt time.Time
				var finishedAt sql.NullTime
				if scanErr := attRows.Scan(&execID, &attemptNo, &attStatus, &rowsCommitted, &errMsg, &attSQL, &startedAt, &finishedAt); scanErr == nil {
					item := Execution{
						ExecutionID:   execID,
						Attempt:       attemptNo,
						Status:        attStatus,
						RowsCommitted: rowsCommitted,
						SQLExecuted:   attSQL,
						ExecutedSQL:   attSQL,
						StartedAt:     &startedAt,
						ExecutedAt:    &startedAt,
					}
					if errMsg.Valid {
						v := errMsg.String
						item.ErrorMessage = &v
					}
					if finishedAt.Valid {
						t := finishedAt.Time
						item.FinishedAt = &t
						item.ExecutedAt = &t
					}
					attemptsByExec[execID] = append(attemptsByExec[execID], item)
				}
			}
			attRows.Close()
		}
	}

	history := make([]Execution, 0, len(baseExecutions))
	for _, base := range baseExecutions {
		atts := attemptsByExec[base.ID]
		if len(atts) <= 1 {
			if len(atts) == 1 {
				base.Attempt = atts[0].Attempt
			}
			history = append(history, base)
			continue
		}
		for _, att := range atts {
			entry := base
			entry.Attempt = att.Attempt
			entry.Status = att.Status
			entry.RowsCommitted = att.RowsCommitted
			entry.ErrorMessage = att.ErrorMessage
			entry.SQLExecuted = att.SQLExecuted
			entry.ExecutedSQL = att.ExecutedSQL
			entry.StartedAt = att.StartedAt
			entry.FinishedAt = att.FinishedAt
			entry.ExecutedAt = att.ExecutedAt
			history = append(history, entry)
		}
	}

	legacyRows, err := s.pool.Query(ctx, `SELECT m.id,m.database_id,m.title,m.status,m.sql,m.error_message,m.created_at,m.finish_time,m.executed_by
              FROM migrations m
              WHERE m.database_id=$1 AND m.status IN ('executed','failed')
              AND NOT EXISTS (SELECT 1 FROM executions e WHERE e.migration_id=m.id)`, databaseID)
	if err != nil {
		return nil, err
	}
	defer legacyRows.Close()
	for legacyRows.Next() {
		var e Execution
		var status, title, sqlText string
		var errorMessage sql.NullString
		var finished sql.NullTime
		var executedBy sql.NullInt64
		if err := legacyRows.Scan(&e.MigrationID, &e.DatabaseID, &title, &status, &sqlText, &errorMessage, &e.CreatedAt, &finished, &executedBy); err != nil {
			return nil, err
		}
		e.ExecutionID = int64(e.MigrationID)
		e.RevisionNo = 1
		e.Attempt = 1
		e.JobID = fmt.Sprintf("legacy-M%d", e.MigrationID)
		e.Title = title
		e.Action = ActionUp
		e.Mode = ModeSingle
		e.SQLExecuted = sqlText
		e.ExecutedSQL = sqlText
		created := e.CreatedAt
		e.ExecutedAt = &created
		if status == StatusFailed {
			e.Status = ExecFailed
		} else {
			e.Status = ExecSuccess
		}
		if errorMessage.Valid {
			v := errorMessage.String
			e.ErrorMessage = &v
		}
		if finished.Valid {
			e.FinishedAt = &finished.Time
			e.ExecutedAt = &finished.Time
		}
		if executedBy.Valid {
			v := int(executedBy.Int64)
			e.RequestedBy = &v
		}
		history = append(history, e)
	}
	return history, legacyRows.Err()
}

func (s *Store) GetDatabaseTarget(ctx context.Context, databaseID int) (string, bool, error) {
	var dbType string
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT db_type,is_active FROM database_connections WHERE id=$1`, databaseID).Scan(&dbType, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return dbType, active, err
}

func (s *Store) GetActiveMigration(ctx context.Context, databaseID int) (*Migration, error) {
	m := &Migration{}
	var version sql.NullInt64
	var superseded sql.NullTime
	var updated, started, finished sql.NullTime
	var executedBy sql.NullInt64
	var errorMessage sql.NullString
	err := s.pool.QueryRow(ctx, `SELECT id,database_id,version,title,sql,status,active,superseded_at,created_by,executed_by,created_at,updated_at,start_time,finish_time,error_message
           FROM migrations WHERE database_id=$1 AND active=true ORDER BY version DESC NULLS LAST,id DESC LIMIT 1`,
		databaseID).Scan(&m.ID, &m.DatabaseID, &version, &m.Title, &m.SQL, &m.Status, &m.Active, &superseded, &m.CreatedBy, &executedBy, &m.CreatedAt, &updated, &started, &finished, &errorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveMigration
	}
	if err != nil {
		return nil, err
	}
	if version.Valid {
		v := int(version.Int64)
		m.Version = &v
	}
	if executedBy.Valid {
		v := int(executedBy.Int64)
		m.ExecutedBy = &v
	}
	if superseded.Valid {
		m.SupersededAt = &superseded.Time
	}
	if updated.Valid {
		m.UpdatedAt = &updated.Time
	}
	if started.Valid {
		m.StartTime = &started.Time
	}
	if finished.Valid {
		m.FinishTime = &finished.Time
	}
	if errorMessage.Valid {
		m.ErrorMessage = &errorMessage.String
	}
	return m, nil
}

func (s *Store) GetLatestAppliedExecution(ctx context.Context, migrationID int) (*Execution, error) {
	e, err := s.scanExecution(s.pool.QueryRow(ctx, executionSelectQuery+`
              WHERE e.migration_id=$1 AND e.action IN ('up','forward') AND e.status='success'
              AND NOT EXISTS (SELECT 1 FROM executions d WHERE d.rollback_of=e.id AND d.action='down' AND d.status='success')
              ORDER BY e.finished_at DESC NULLS LAST,e.id DESC LIMIT 1`, migrationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveMigration
	}
	return e, err
}

func (s *Store) GetLatestDownExecution(ctx context.Context, migrationID int) (*Execution, error) {
	e, err := s.scanExecution(s.pool.QueryRow(ctx, executionSelectQuery+`
              WHERE e.migration_id=$1 AND e.action='down' AND e.status='success'
              AND NOT EXISTS (SELECT 1 FROM executions f WHERE f.rollback_of=e.id AND f.action='forward' AND f.status='success')
              ORDER BY e.finished_at DESC NULLS LAST,e.id DESC LIMIT 1`, migrationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveMigration
	}
	return e, err
}