package async

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type LockManager struct {
	pool              *pgxpool.Pool
	logger            *slog.Logger
	leaseTTL          time.Duration
	heartbeatInterval time.Duration
}

func NewLockManager(pool *pgxpool.Pool, logger *slog.Logger, leaseTTL time.Duration) *LockManager {
	if logger == nil {
		logger = slog.Default()
	}
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Second
	}
	hb := leaseTTL / 3
	if hb < 500*time.Millisecond {
		hb = 500 * time.Millisecond
	}
	if hb >= leaseTTL {
		hb = leaseTTL / 2
	}
	return &LockManager{
		pool:              pool,
		logger:            logger,
		leaseTTL:          leaseTTL,
		heartbeatInterval: hb,
	}
}

func (m *LockManager) LeaseTTL() time.Duration {
	return m.leaseTTL
}

type LeaseHandle struct {
	manager      *LockManager
	DatabaseID   int
	WorkerID     string
	JobID        string
	ExecutionID  int64
	FencingToken int64

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	mu      sync.RWMutex
	lostErr error
}

func (m *LockManager) Acquire(ctx context.Context, databaseID int, workerID, jobID string, executionID int64, jobKind string) (*LeaseHandle, error) {
	ctx, span := telemetry.Tracer("lock").Start(ctx, "db_lock.acquire")
	defer span.End()

	span.SetAttributes(
		attribute.Int("database.id", databaseID),
		attribute.String("worker.id", workerID),
		attribute.String("job.id", jobID),
		attribute.Int64("execution.id", executionID),
		attribute.String("job.kind", jobKind),
	)

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var connID int
	if err := tx.QueryRow(ctx, `SELECT id FROM database_connections WHERE id = $1 FOR UPDATE`, databaseID).Scan(&connID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, migration.ErrNotFound
		}
		return nil, err
	}

	var otherExecID int64
	var otherStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM executions
		WHERE database_id = $1 AND id <> $2 AND status IN ('partial', 'partially_reverted')
		ORDER BY id DESC LIMIT 1
	`, databaseID, executionID).Scan(&otherExecID, &otherStatus)
	if err == nil {
		msg := fmt.Errorf("%w: execution %d is %s", ErrDatabaseBlocked, otherExecID, otherStatus)
		span.RecordError(msg)
		span.SetStatus(codes.Error, msg.Error())
		return nil, msg
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	var curWorker, curJobID, curState string
	var curExecID, curToken int64
	var leaseUntil time.Time

	err = tx.QueryRow(ctx, `
		SELECT holder_worker_id, job_id, execution_id, fencing_token, state, lease_until
		FROM database_migration_locks
		WHERE database_id = $1
		FOR UPDATE
	`, databaseID).Scan(&curWorker, &curJobID, &curExecID, &curToken, &curState, &leaseUntil)

	now := time.Now()
	newLeaseUntil := now.Add(m.leaseTTL)
	var nextToken int64 = 1

	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO database_migration_locks (
				database_id, holder_worker_id, job_id, execution_id, fencing_token, state, acquired_at, heartbeat_at, lease_until
			) VALUES ($1, $2, $3, $4, 1, 'active', $5, $5, $6)
		`, databaseID, workerID, jobID, executionID, now, newLeaseUntil)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		switch curState {
		case LockStateBlockedUncertain:
			if curExecID != executionID {
				return nil, fmt.Errorf("%w: database %d held due to uncertain commit on execution %d", ErrDatabaseBlocked, databaseID, curExecID)
			}
		case LockStateBlockedPartial:
			if curExecID != executionID || (jobKind != JobKindResume && jobKind != JobKindCancelBatches) {
				return nil, fmt.Errorf("%w: database %d held by partial execution %d", ErrDatabaseBlocked, databaseID, curExecID)
			}
		case LockStateActive:
			if leaseUntil.After(now) && curWorker != workerID {
				return nil, ErrDatabaseLocked
			}
			if leaseUntil.After(now) && curWorker == workerID && curExecID != executionID {
				return nil, ErrDatabaseLocked
			}
			if !leaseUntil.After(now) && curExecID != executionID {
				var prevStatus string
				if qErr := tx.QueryRow(ctx, `SELECT status FROM executions WHERE id = $1`, curExecID).Scan(&prevStatus); qErr == nil {
					if prevStatus == migration.ExecPartial || prevStatus == migration.ExecPartiallyReverted {
						_, _ = tx.Exec(ctx, `
							UPDATE database_migration_locks
							SET state = 'blocked_partial', reason = 'abandoned partial execution after worker lease expiry'
							WHERE database_id = $1
						`, databaseID)
						_ = tx.Commit(ctx)
						return nil, fmt.Errorf("%w: previous execution %d is %s", ErrDatabaseBlocked, curExecID, prevStatus)
					}
					if prevStatus == migration.ExecRunning {
						_, _ = tx.Exec(ctx, `
							UPDATE database_migration_locks
							SET state = 'blocked_uncertain', reason = 'previous worker crashed while execution was running'
							WHERE database_id = $1
						`, databaseID)
						_ = tx.Commit(ctx)
						return nil, fmt.Errorf("%w: previous execution %d was still running when worker lease expired", ErrDatabaseBlocked, curExecID)
					}
				}
			}
			if !leaseUntil.After(now) {
				m.logger.WarnContext(ctx, "db_lock.lease_recovered",
					slog.String("database", strconv.Itoa(databaseID)),
					slog.String("previous_worker", curWorker),
					slog.String("new_worker", workerID),
					slog.String("job_id", jobID),
					slog.Int64("execution_id", executionID),
					slog.String("status", "recovered"),
				)
			}
		}

		nextToken = curToken + 1
		_, err = tx.Exec(ctx, `
			UPDATE database_migration_locks
			SET holder_worker_id = $2,
				job_id = $3,
				execution_id = $4,
				fencing_token = $5,
				state = 'active',
				reason = NULL,
				heartbeat_at = $6,
				lease_until = $7
			WHERE database_id = $1
		`, databaseID, workerID, jobID, executionID, nextToken, now, newLeaseUntil)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	handle := &LeaseHandle{
		manager:      m,
		DatabaseID:   databaseID,
		WorkerID:     workerID,
		JobID:        jobID,
		ExecutionID:  executionID,
		FencingToken: nextToken,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	go handle.runHeartbeat()

	m.logger.InfoContext(ctx, "db_lock.acquired",
		slog.String("database", strconv.Itoa(databaseID)),
		slog.String("job_id", jobID),
		slog.Int64("execution_id", executionID),
		slog.Int64("fencing_token", nextToken),
		slog.String("status", "locked"),
	)
	return handle, nil
}

func (h *LeaseHandle) runHeartbeat() {
	defer close(h.doneCh)
	ticker := time.NewTicker(h.manager.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), h.manager.heartbeatInterval)
			newUntil := time.Now().Add(h.manager.leaseTTL)
			tag, err := h.manager.pool.Exec(ctx, `
				UPDATE database_migration_locks
				SET heartbeat_at = NOW(), lease_until = $4
				WHERE database_id = $1 AND holder_worker_id = $2 AND fencing_token = $3 AND state = 'active'
			`, h.DatabaseID, h.WorkerID, h.FencingToken, newUntil)
			cancel()
			if err != nil {
				continue
			}
			if tag.RowsAffected() == 0 {
				h.mu.Lock()
				h.lostErr = errors.New("database migration lock lease was lost or fenced")
				h.mu.Unlock()
				return
			}
		}
	}
}

func (h *LeaseHandle) CheckValid() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lostErr
}

func (h *LeaseHandle) stopHeartbeat() {
	h.stopOnce.Do(func() {
		close(h.stopCh)
	})
	<-h.doneCh
}

func (h *LeaseHandle) Release(ctx context.Context) error {
	h.stopHeartbeat()
	detCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	_, err := h.manager.pool.Exec(detCtx, `
		DELETE FROM database_migration_locks
		WHERE database_id = $1 AND fencing_token = $2
	`, h.DatabaseID, h.FencingToken)

	h.manager.logger.InfoContext(ctx, "db_lock.released",
		slog.String("database", strconv.Itoa(h.DatabaseID)),
		slog.String("job_id", h.JobID),
		slog.Int64("execution_id", h.ExecutionID),
		slog.String("status", "released"),
	)
	return err
}

func (h *LeaseHandle) HoldBlocked(ctx context.Context, blockedState, reason string) error {
	h.stopHeartbeat()
	if blockedState != LockStateBlockedPartial && blockedState != LockStateBlockedUncertain {
		blockedState = LockStateBlockedPartial
	}
	detCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	_, err := h.manager.pool.Exec(detCtx, `
		UPDATE database_migration_locks
		SET state = $3,
			reason = NULLIF($4, ''),
			heartbeat_at = NOW(),
			lease_until = NOW() + INTERVAL '365 days'
		WHERE database_id = $1 AND fencing_token = $2
	`, h.DatabaseID, h.FencingToken, blockedState, reason)

	h.manager.logger.WarnContext(ctx, "db_lock.held_blocked",
		slog.String("database", strconv.Itoa(h.DatabaseID)),
		slog.String("job_id", h.JobID),
		slog.Int64("execution_id", h.ExecutionID),
		slog.String("lock_state", blockedState),
		slog.String("reason", reason),
		slog.String("status", "blocked"),
	)
	return err
}
