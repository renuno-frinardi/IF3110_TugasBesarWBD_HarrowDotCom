package async

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Queue struct {
	pool        *pgxpool.Pool
	logger      *slog.Logger
	maxAttempts int
}

func NewQueue(pool *pgxpool.Pool, logger *slog.Logger, maxAttempts int) *Queue {
	if logger == nil {
		logger = slog.Default()
	}
	if maxAttempts < 1 {
		maxAttempts = 3
	}
	return &Queue{
		pool:        pool,
		logger:      logger,
		maxAttempts: maxAttempts,
	}
}

func (q *Queue) PublishJob(ctx context.Context, job migration.JobMessage) (string, error) {
	if q.pool == nil {
		return "", errors.New("queue database pool is nil")
	}
	if strings.TrimSpace(job.JobID) == "" {
		return "", fmt.Errorf("%w: job_id is required", migration.InvalidInput)
	}

	if job.TraceContext != "" {
		ctx = telemetry.ExtractTraceContext(ctx, job.TraceContext)
	}
	ctx, span := telemetry.Tracer("queue").Start(ctx, "queue.publish")
	defer span.End()

	span.SetAttributes(
		attribute.String("job.id", job.JobID),
		attribute.Int64("execution.id", job.ExecutionID),
		attribute.Int("database.id", job.DatabaseID),
		attribute.Int("migration.id", job.MigrationID),
		attribute.String("migration.action", job.Action),
		attribute.String("migration.mode", job.Mode),
	)

	traceCtx := telemetry.InjectTraceContext(ctx)
	if traceCtx == "" {
		traceCtx = job.TraceContext
	}

	downSQL := job.DownSQL
	irreversible := job.Irreversible
	requestedBy := job.RequestedBy

	if downSQL == "" && !irreversible && job.RevisionID > 0 {
		var dbDown sql.NullString
		_ = q.pool.QueryRow(ctx, `SELECT down_sql, irreversible FROM migration_revisions WHERE id = $1`, job.RevisionID).Scan(&dbDown, &irreversible)
		downSQL = dbDown.String
	}
	if requestedBy <= 0 && job.ExecutionID > 0 {
		var reqBy sql.NullInt64
		_ = q.pool.QueryRow(ctx, `SELECT requested_by FROM executions WHERE id = $1`, job.ExecutionID).Scan(&reqBy)
		if reqBy.Valid {
			requestedBy = int(reqBy.Int64)
		}
	}

	mode := strings.ToLower(strings.TrimSpace(job.Mode))
	if mode == "" {
		mode = migration.ModeNormal
	}
	cfgBytes, err := json.Marshal(job.BatchConfig)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	var reqAny any
	if requestedBy > 0 {
		reqAny = requestedBy
	}

	_, err = q.pool.Exec(ctx, `
		INSERT INTO migration_jobs (
			job_id, execution_id, database_id, migration_id, revision_id,
			job_kind, action, mode, batch_config, sql_text, down_sql_text,
			irreversible, requested_by, trace_context, status, max_attempts
		) VALUES (
			$1, $2, $3, $4, $5,
			'execute', $6, $7, $8, $9, $10,
			$11, $12, $13, 'queued', $14
		)
		ON CONFLICT (job_id, job_kind) WHERE status IN ('queued', 'processing')
		DO NOTHING
	`, job.JobID, job.ExecutionID, job.DatabaseID, job.MigrationID, job.RevisionID,
		job.Action, mode, cfgBytes, job.SQL, downSQL,
		irreversible, reqAny, traceCtx, q.maxAttempts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("enqueue migration job: %w", err)
	}

	_, _ = q.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, job.JobID)

	q.logger.InfoContext(ctx, "queue.job.published",
		slog.String("job_id", job.JobID),
		slog.Int64("execution_id", job.ExecutionID),
		slog.Int("migration_id", job.MigrationID),
		slog.String("database", strconv.Itoa(job.DatabaseID)),
		slog.String("actor", strconv.Itoa(requestedBy)),
		slog.String("status", JobStatusQueued),
	)
	return job.JobID, nil
}

func (q *Queue) EnqueueResume(ctx context.Context, e *migration.Execution, rev *migration.Revision, userID int) (string, error) {
	if e == nil || rev == nil {
		return "", migration.ErrNotFound
	}
	if e.Status != migration.ExecPartial && e.Status != migration.ExecFailed && e.Status != migration.ExecPartiallyReverted {
		return "", fmt.Errorf("%w: execution in status %s cannot be resumed", migration.ErrInvalidState, e.Status)
	}

	ctx, span := telemetry.Tracer("queue").Start(ctx, "queue.publish.resume")
	defer span.End()

	kind := JobKindResume
	if e.Status == migration.ExecPartiallyReverted {
		kind = JobKindCancelBatches
	}

	cfgBytes, _ := json.Marshal(e.BatchConfig)
	traceCtx := telemetry.InjectTraceContext(ctx)

	var maxAttemptSoFar int
	_ = q.pool.QueryRow(ctx, `SELECT COALESCE(MAX(attempt_no), 0) FROM execution_attempts WHERE execution_id = $1`, e.ID).Scan(&maxAttemptSoFar)

	_, err := q.pool.Exec(ctx, `
		INSERT INTO migration_jobs (
			job_id, execution_id, database_id, migration_id, revision_id,
			job_kind, action, mode, batch_config, sql_text, down_sql_text,
			irreversible, requested_by, trace_context, status, attempt, max_attempts
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11,
			$12, $13, $14, 'queued', $15, $16
		)
		ON CONFLICT (job_id, job_kind) WHERE status IN ('queued', 'processing')
		DO NOTHING
	`, e.JobID, e.ID, e.DatabaseID, e.MigrationID, e.RevisionID,
		kind, e.Action, e.Mode, cfgBytes, e.SQLExecuted, rev.DownSQL,
		rev.Irreversible, userID, traceCtx, maxAttemptSoFar, maxAttemptSoFar+q.maxAttempts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("enqueue resume job: %w", err)
	}

	_, _ = q.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, e.JobID)
	q.logger.InfoContext(ctx, "queue.job.resumed",
		slog.String("job_id", e.JobID),
		slog.Int64("execution_id", e.ID),
		slog.Int("migration_id", e.MigrationID),
		slog.String("database", strconv.Itoa(e.DatabaseID)),
		slog.String("actor", strconv.Itoa(userID)),
		slog.String("status", JobStatusQueued),
	)
	return e.JobID, nil
}

func (q *Queue) EnqueueCancelBatches(ctx context.Context, e *migration.Execution, rev *migration.Revision, userID int) (string, error) {
	if e == nil || rev == nil {
		return "", migration.ErrNotFound
	}
	if strings.ToLower(strings.TrimSpace(e.Mode)) != migration.ModeBatch {
		return "", fmt.Errorf("%w: only batch executions support batch cancellation", ErrCancelUnsafe)
	}
	if rev.Irreversible || !CanSafelyRevertBatchSQL(rev.DownSQL) {
		return "", fmt.Errorf("%w: batch cancellation requires reversible down SQL scoped with {{batch_column}}, {{range_from}}, and {{range_to}}", ErrCancelUnsafe)
	}
	switch e.Status {
	case migration.ExecPending, migration.ExecRunning, migration.ExecPartial, migration.ExecPartiallyReverted:
	default:
		return "", fmt.Errorf("%w: execution in status %s cannot cancel batches", migration.ErrInvalidState, e.Status)
	}

	ctx, span := telemetry.Tracer("queue").Start(ctx, "queue.publish.cancel_batches")
	defer span.End()

	_, _ = q.pool.Exec(ctx, `
		UPDATE migration_jobs
		SET cancel_requested = TRUE,
			status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
			updated_at = NOW()
		WHERE job_id = $1 AND job_kind IN ('execute', 'resume') AND status IN ('queued', 'processing')
	`, e.JobID)

	cfgBytes, _ := json.Marshal(e.BatchConfig)
	traceCtx := telemetry.InjectTraceContext(ctx)

	var maxAttemptSoFar int
	_ = q.pool.QueryRow(ctx, `SELECT COALESCE(MAX(attempt_no), 0) FROM execution_attempts WHERE execution_id = $1`, e.ID).Scan(&maxAttemptSoFar)

	_, err := q.pool.Exec(ctx, `
		INSERT INTO migration_jobs (
			job_id, execution_id, database_id, migration_id, revision_id,
			job_kind, action, mode, batch_config, sql_text, down_sql_text,
			irreversible, requested_by, trace_context, status, attempt, max_attempts
		) VALUES (
			$1, $2, $3, $4, $5,
			'cancel_batches', $6, $7, $8, $9, $10,
			$11, $12, $13, 'queued', $14, $15
		)
		ON CONFLICT (job_id, job_kind) WHERE status IN ('queued', 'processing')
		DO NOTHING
	`, e.JobID, e.ID, e.DatabaseID, e.MigrationID, e.RevisionID,
		e.Action, e.Mode, cfgBytes, e.SQLExecuted, rev.DownSQL,
		rev.Irreversible, userID, traceCtx, maxAttemptSoFar, maxAttemptSoFar+q.maxAttempts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("enqueue cancel-batches job: %w", err)
	}

	_, _ = q.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, e.JobID)
	q.logger.InfoContext(ctx, "queue.job.cancel_requested",
		slog.String("job_id", e.JobID),
		slog.Int64("execution_id", e.ID),
		slog.Int("migration_id", e.MigrationID),
		slog.String("database", strconv.Itoa(e.DatabaseID)),
		slog.String("actor", strconv.Itoa(userID)),
		slog.String("status", JobStatusQueued),
	)
	return e.JobID, nil
}

func (q *Queue) ClaimNextJob(ctx context.Context, workerID string, staleProcessingTimeout time.Duration) (*QueueJob, error) {
	if staleProcessingTimeout <= 0 {
		staleProcessingTimeout = 30 * time.Second
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	staleCutoff := time.Now().Add(-staleProcessingTimeout)

	row := tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT mj.id
			FROM migration_jobs mj
			WHERE (
				(mj.status = 'queued' AND mj.next_run_at <= NOW())
				OR (
					mj.status = 'processing'
					AND mj.locked_at < $2
					AND NOT EXISTS (
						SELECT 1 FROM database_migration_locks l
						WHERE l.database_id = mj.database_id
						  AND l.job_id = mj.job_id
						  AND l.state = 'active'
						  AND l.lease_until > NOW()
					)
				)
			)
			AND NOT EXISTS (
				SELECT 1 FROM database_migration_locks l
				WHERE l.database_id = mj.database_id
				  AND (
					(l.state = 'active' AND l.lease_until > NOW() AND l.holder_worker_id <> $1)
					OR (l.state = 'blocked_uncertain' AND l.execution_id <> mj.execution_id)
					OR (l.state = 'blocked_partial' AND (l.execution_id <> mj.execution_id OR mj.job_kind = 'execute'))
				  )
			)
			ORDER BY mj.next_run_at ASC, mj.id ASC
			FOR UPDATE OF mj SKIP LOCKED
			LIMIT 1
		)
		UPDATE migration_jobs mj
		SET status = 'processing',
			attempt = mj.attempt + 1,
			locked_by = $1,
			locked_at = NOW(),
			updated_at = NOW()
		FROM candidate
		WHERE mj.id = candidate.id
		RETURNING
			mj.id, mj.job_id, mj.execution_id, mj.database_id, mj.migration_id, mj.revision_id,
			mj.job_kind, mj.action, mj.mode, mj.batch_config, mj.sql_text, mj.down_sql_text,
			mj.irreversible, mj.requested_by, mj.trace_context, mj.status, mj.attempt, mj.max_attempts,
			mj.next_run_at, mj.locked_by, mj.locked_at, mj.cancel_requested, mj.last_error,
			mj.created_at, mj.updated_at
	`, workerID, staleCutoff)

	job, err := scanQueueJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoJobAvailable
	}
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return job, nil
}

func scanQueueJob(row pgx.Row) (*QueueJob, error) {
	var j QueueJob
	var cfgRaw []byte
	var reqBy sql.NullInt64
	var lockedBy, lastErr sql.NullString
	var lockedAt sql.NullTime

	err := row.Scan(
		&j.ID, &j.JobID, &j.ExecutionID, &j.DatabaseID, &j.MigrationID, &j.RevisionID,
		&j.JobKind, &j.Action, &j.Mode, &cfgRaw, &j.SQL, &j.DownSQL,
		&j.Irreversible, &reqBy, &j.TraceContext, &j.Status, &j.Attempt, &j.MaxAttempts,
		&j.NextRunAt, &lockedBy, &lockedAt, &j.CancelRequested, &lastErr,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(cfgRaw, &j.BatchConfig)
	if reqBy.Valid {
		j.RequestedBy = int(reqBy.Int64)
	}
	if lockedBy.Valid {
		j.LockedBy = lockedBy.String
	}
	if lockedAt.Valid {
		t := lockedAt.Time
		j.LockedAt = &t
	}
	if lastErr.Valid {
		j.LastError = lastErr.String
	}
	return &j, nil
}

func (q *Queue) IsCancelRequested(ctx context.Context, queueRowID int64) (bool, error) {
	var cancel bool
	err := q.pool.QueryRow(ctx, `SELECT cancel_requested FROM migration_jobs WHERE id = $1`, queueRowID).Scan(&cancel)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return cancel, err
}

func (q *Queue) MarkCompleted(ctx context.Context, queueRowID int64) error {
	detCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := q.pool.Exec(detCtx, `
		UPDATE migration_jobs
		SET status = 'completed', locked_by = NULL, locked_at = NULL, updated_at = NOW()
		WHERE id = $1
	`, queueRowID)
	return err
}

func (q *Queue) MarkCancelled(ctx context.Context, queueRowID int64, reason string) error {
	detCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := q.pool.Exec(detCtx, `
		UPDATE migration_jobs
		SET status = 'cancelled', last_error = NULLIF($2, ''), locked_by = NULL, locked_at = NULL, updated_at = NOW()
		WHERE id = $1
	`, queueRowID, reason)
	return err
}

func (q *Queue) RequeueWithDelay(ctx context.Context, queueRowID int64, delay time.Duration, lastErr string, decrementAttempt bool) error {
	detCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	nextRun := time.Now().Add(delay)
	attemptDelta := 0
	if decrementAttempt {
		attemptDelta = -1
	}
	_, err := q.pool.Exec(detCtx, `
		UPDATE migration_jobs
		SET status = 'queued',
			attempt = GREATEST(0, attempt + $4),
			next_run_at = $2,
			last_error = NULLIF($3, ''),
			locked_by = NULL,
			locked_at = NULL,
			updated_at = NOW()
		WHERE id = $1
	`, queueRowID, nextRun, lastErr, attemptDelta)
	return err
}

func (q *Queue) MarkDead(ctx context.Context, queueRowID int64, lastErr string) error {
	detCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := q.pool.Exec(detCtx, `
		UPDATE migration_jobs
		SET status = 'dead', last_error = NULLIF($2, ''), locked_by = NULL, locked_at = NULL, updated_at = NOW()
		WHERE id = $1
	`, queueRowID, lastErr)
	return err
}
