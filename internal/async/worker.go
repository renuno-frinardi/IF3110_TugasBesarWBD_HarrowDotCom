package async

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type TargetDialer func(ctx context.Context, databaseID int) (*pgx.Conn, error)

type WorkerConfig struct {
	WorkerID      string
	Concurrency   int
	PollInterval  time.Duration
	LeaseTTL      time.Duration
	EncryptionKey string
	RetryPolicy   RetryPolicy
	TargetDialer  TargetDialer
}

type Worker struct {
	id            string
	pool          *pgxpool.Pool
	queue         *Queue
	locks         *LockManager
	migStore      *migration.Store
	migService    *migration.Service
	logger        *slog.Logger
	concurrency   int
	pollInterval  time.Duration
	encryptionKey string
	retryPolicy   RetryPolicy
	targetDialer  TargetDialer

	afterBatchHook        func(ctx context.Context, job *QueueJob, completedBatchNo int) error
	beforeRevertBatchHook func(ctx context.Context, job *QueueJob, revertingBatchNo int) error
}

func NewWorker(pool *pgxpool.Pool, queue *Queue, locks *LockManager, migStore *migration.Store, migService *migration.Service, logger *slog.Logger, cfg WorkerConfig) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	wID := strings.TrimSpace(cfg.WorkerID)
	if wID == "" {
		host, _ := os.Hostname()
		var rnd [4]byte
		_, _ = rand.Read(rnd[:])
		wID = fmt.Sprintf("worker-%s-%s", host, hex.EncodeToString(rnd[:]))
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 4
	}
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	rp := cfg.RetryPolicy
	if rp.MaxAttempts < 1 {
		rp = DefaultRetryPolicy()
	}
	if locks == nil {
		locks = NewLockManager(pool, logger, cfg.LeaseTTL)
	}
	if queue == nil {
		queue = NewQueue(pool, logger, rp.MaxAttempts)
	}

	w := &Worker{
		id:            wID,
		pool:          pool,
		queue:         queue,
		locks:         locks,
		migStore:      migStore,
		migService:    migService,
		logger:        logger,
		concurrency:   conc,
		pollInterval:  poll,
		encryptionKey: cfg.EncryptionKey,
		retryPolicy:   rp,
		targetDialer:  cfg.TargetDialer,
	}
	return w
}

func (w *Worker) ID() string {
	return w.id
}

func (w *Worker) SetHooks(
	afterBatch func(ctx context.Context, job *QueueJob, completedBatchNo int) error,
	beforeRevert func(ctx context.Context, job *QueueJob, revertingBatchNo int) error,
) {
	w.afterBatchHook = afterBatch
	w.beforeRevertBatchHook = beforeRevert
}

func (w *Worker) openTargetConn(ctx context.Context, databaseID int) (*pgx.Conn, error) {
	if w.targetDialer != nil {
		return w.targetDialer(ctx, databaseID)
	}
	return migration.OpenTargetPostgresForWorker(ctx, w.pool, databaseID, w.encryptionKey)
}

func (w *Worker) Run(ctx context.Context) error {
	w.logger.InfoContext(ctx, "worker.started",
		slog.String("worker_id", w.id),
		slog.Int("concurrency", w.concurrency),
		slog.String("status", "ready"),
	)

	wakeCh := make(chan struct{}, w.concurrency*2)
	go w.listenNotifications(ctx, wakeCh)

	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.workerLoop(ctx, wakeCh)
		}()
	}

	wg.Wait()
	w.logger.InfoContext(context.Background(), "worker.stopped",
		slog.String("worker_id", w.id),
		slog.String("status", "stopped"),
	)
	return nil
}

func (w *Worker) listenNotifications(ctx context.Context, wakeCh chan<- struct{}) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := w.pool.Acquire(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		_, err = conn.Exec(ctx, "LISTEN "+NotifyChannel)
		if err != nil {
			conn.Release()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}

		for {
			_, waitErr := conn.Conn().WaitForNotification(ctx)
			if waitErr != nil {
				conn.Release()
				break
			}
			select {
			case wakeCh <- struct{}{}:
			default:
			}
		}
	}
}

func (w *Worker) workerLoop(ctx context.Context, wakeCh <-chan struct{}) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}
		processed, _ := w.ProcessOne(ctx)
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wakeCh:
		case <-ticker.C:
		}
	}
}

func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	staleTimeout := w.locks.LeaseTTL() * 2
	if staleTimeout < 2*time.Second {
		staleTimeout = 2 * time.Second
	}

	job, err := w.queue.ClaimNextJob(ctx, w.id, staleTimeout)
	if errors.Is(err, ErrNoJobAvailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	w.processClaimedJob(ctx, job)
	return true, nil
}

func (w *Worker) processClaimedJob(parentCtx context.Context, job *QueueJob) {
	actorStr := strconv.Itoa(job.RequestedBy)
	dbStr := strconv.Itoa(job.DatabaseID)

	ctx := telemetry.ExtractTraceContext(parentCtx, job.TraceContext)
	ctx = telemetry.WithCorrelationID(ctx, job.JobID)
	ctx = telemetry.WithActor(ctx, actorStr)
	ctx = telemetry.WithDatabase(ctx, dbStr)

	ctx, span := telemetry.Tracer("worker").Start(ctx, "worker.process_job")
	defer span.End()

	span.SetAttributes(
		attribute.String("worker.id", w.id),
		attribute.String("job.id", job.JobID),
		attribute.String("job.kind", job.JobKind),
		attribute.Int64("execution.id", job.ExecutionID),
		attribute.Int("database.id", job.DatabaseID),
		attribute.Int("migration.id", job.MigrationID),
		attribute.String("migration.action", job.Action),
		attribute.String("migration.mode", job.Mode),
		attribute.Int("job.attempt", job.Attempt),
	)

	lease, lockErr := w.locks.Acquire(ctx, job.DatabaseID, w.id, job.JobID, job.ExecutionID, job.JobKind)
	if lockErr != nil {
		span.RecordError(lockErr)
		_ = w.queue.RequeueWithDelay(ctx, job.ID, w.pollInterval, lockErr.Error(), true)
		return
	}

	existingExec, getErr := w.migStore.GetExecution(ctx, job.ExecutionID)
	if getErr == nil && existingExec.Status == migration.ExecSuccess && job.JobKind == JobKindExecute {
		w.logger.InfoContext(ctx, "worker.duplicate_job.ignored",
			slog.String("job_id", job.JobID),
			slog.Int64("execution_id", job.ExecutionID),
			slog.String("status", migration.ExecSuccess),
		)
		_ = w.queue.MarkCompleted(ctx, job.ID)
		_ = lease.Release(ctx)
		return
	}

	allowReopen := job.JobKind == JobKindResume || job.JobKind == JobKindCancelBatches
	startedAt := time.Now()
	_ = w.migService.RecordExecution(ctx, migration.ExecutionResult{
		JobID:       job.JobID,
		ExecutionID: job.ExecutionID,
		Attempt:     job.Attempt,
		JobKind:     job.JobKind,
		WorkerID:    w.id,
		Status:      migration.ExecRunning,
		StartedAt:   &startedAt,
		AllowReopen: allowReopen,
	})

	w.logger.InfoContext(ctx, "worker.job.running",
		slog.String("worker_id", w.id),
		slog.String("job_id", job.JobID),
		slog.Int64("execution_id", job.ExecutionID),
		slog.Int("migration_id", job.MigrationID),
		slog.Int("attempt", job.Attempt),
		slog.String("job_kind", job.JobKind),
		slog.String("status", migration.ExecRunning),
	)

	targetConn, dialErr := w.openTargetConn(ctx, job.DatabaseID)
	var outcome executionOutcome
	if dialErr != nil {
		var prevRows int64
		if existingExec != nil {
			prevRows = existingExec.RowsCommitted
		}
		statusOnDialErr := migration.ExecFailed
		if prevRows > 0 {
			statusOnDialErr = migration.ExecPartial
		}
		if job.JobKind == JobKindCancelBatches && existingExec != nil && existingExec.Status == migration.ExecPartiallyReverted {
			statusOnDialErr = migration.ExecPartiallyReverted
		}
		outcome = executionOutcome{
			Status:        statusOnDialErr,
			RowsCommitted: prevRows,
			Err:           dialErr,
		}
	} else {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = targetConn.Close(closeCtx)
		}()

		switch {
		case job.JobKind == JobKindCancelBatches:
			outcome = w.executeCancelBatches(ctx, job, lease, targetConn)
		case strings.EqualFold(job.Mode, migration.ModeBatch):
			outcome = w.executeBatchMode(ctx, job, lease, targetConn)
		default:
			outcome = w.executeNormalMode(ctx, job, lease, targetConn)
		}
	}

	finishedAt := time.Now()

	if outcome.Uncertain {
		errMsg := ""
		if outcome.Err != nil {
			errMsg = outcome.Err.Error()
		}
		span.RecordError(outcome.Err)
		span.SetStatus(codes.Error, errMsg)
		_ = lease.HoldBlocked(ctx, LockStateBlockedUncertain, errMsg)
		_ = w.queue.MarkDead(ctx, job.ID, errMsg)
		w.logger.ErrorContext(ctx, "worker.job.uncertain_commit",
			slog.String("job_id", job.JobID),
			slog.Int64("execution_id", job.ExecutionID),
			slog.Int("attempt", job.Attempt),
			slog.String("error", errMsg),
			slog.String("status", "uncertain"),
		)
		return
	}

	if outcome.Cancelled {
		_ = w.migService.RecordExecution(ctx, migration.ExecutionResult{
			JobID:         job.JobID,
			ExecutionID:   job.ExecutionID,
			Attempt:       job.Attempt,
			JobKind:       job.JobKind,
			WorkerID:      w.id,
			Status:        outcome.Status,
			RowsCommitted: outcome.RowsCommitted,
			ErrorMessage:  "batch execution stopped for cancellation",
			StartedAt:     &startedAt,
			FinishedAt:    &finishedAt,
			AllowReopen:   true,
		})
		_ = w.queue.MarkCancelled(ctx, job.ID, "stopped at batch boundary for cancellation")
		if outcome.Status == migration.ExecPartial {
			_ = lease.HoldBlocked(ctx, LockStateBlockedPartial, "awaiting reverse batch cancellation")
		} else {
			_ = lease.Release(ctx)
		}
		return
	}

	maxAttempts := job.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = w.retryPolicy.MaxAttempts
	}
	effectivePolicy := w.retryPolicy
	effectivePolicy.MaxAttempts = maxAttempts

	isCancelSuccess := job.JobKind == JobKindCancelBatches && outcome.Status == migration.ExecFailed && outcome.RowsCommitted == 0
	if outcome.Err != nil && !isCancelSuccess && effectivePolicy.ShouldRetry(job.Attempt, outcome.Err) {
		backoff := effectivePolicy.NextBackoff(job.Attempt)
		_ = w.migService.RecordExecution(ctx, migration.ExecutionResult{
			JobID:          job.JobID,
			ExecutionID:    job.ExecutionID,
			Attempt:        job.Attempt,
			JobKind:        job.JobKind,
			WorkerID:       w.id,
			Status:         outcome.Status,
			RowsCommitted:  outcome.RowsCommitted,
			ErrorMessage:   outcome.Err.Error(),
			StartedAt:      &startedAt,
			FinishedAt:     &finishedAt,
			RetryScheduled: true,
			AllowReopen:    allowReopen,
		})
		_ = w.queue.RequeueWithDelay(ctx, job.ID, backoff, outcome.Err.Error(), false)
		if outcome.Status == migration.ExecPartial || outcome.Status == migration.ExecPartiallyReverted {
			_ = lease.HoldBlocked(ctx, LockStateBlockedPartial, "transient failure mid-batch; retry scheduled")
		} else {
			_ = lease.Release(ctx)
		}
		w.logger.WarnContext(ctx, "worker.job.retry_scheduled",
			slog.String("job_id", job.JobID),
			slog.Int64("execution_id", job.ExecutionID),
			slog.Int("attempt", job.Attempt),
			slog.Int("max_attempts", maxAttempts),
			slog.Int64("backoff_ms", backoff.Milliseconds()),
			slog.String("error", outcome.Err.Error()),
			slog.String("status", "retrying"),
		)
		return
	}

	errMsg := ""
	if outcome.Err != nil {
		errMsg = outcome.Err.Error()
	}

	_ = w.migService.RecordExecution(ctx, migration.ExecutionResult{
		JobID:         job.JobID,
		ExecutionID:   job.ExecutionID,
		Attempt:       job.Attempt,
		JobKind:       job.JobKind,
		WorkerID:      w.id,
		Status:        outcome.Status,
		RowsCommitted: outcome.RowsCommitted,
		ErrorMessage:  errMsg,
		StartedAt:     &startedAt,
		FinishedAt:    &finishedAt,
		AllowReopen:   true,
	})

	switch outcome.Status {
	case migration.ExecSuccess:
		_ = w.queue.MarkCompleted(ctx, job.ID)
		_ = lease.Release(ctx)
		w.logger.InfoContext(ctx, "worker.job.completed",
			slog.String("job_id", job.JobID),
			slog.Int64("execution_id", job.ExecutionID),
			slog.Int("migration_id", job.MigrationID),
			slog.Int("attempt", job.Attempt),
			slog.Int64("rows_committed", outcome.RowsCommitted),
			slog.String("status", migration.ExecSuccess),
		)
	case migration.ExecPartial, migration.ExecPartiallyReverted:
		span.RecordError(outcome.Err)
		span.SetStatus(codes.Error, errMsg)
		_ = w.queue.MarkDead(ctx, job.ID, errMsg)
		_ = lease.HoldBlocked(ctx, LockStateBlockedPartial, errMsg)
		w.logger.ErrorContext(ctx, "worker.job.partial",
			slog.String("job_id", job.JobID),
			slog.Int64("execution_id", job.ExecutionID),
			slog.Int("migration_id", job.MigrationID),
			slog.Int("attempt", job.Attempt),
			slog.Int64("rows_committed", outcome.RowsCommitted),
			slog.String("error", errMsg),
			slog.String("status", outcome.Status),
		)
	default:
		if isCancelSuccess {
			_ = w.queue.MarkCompleted(ctx, job.ID)
			_ = lease.Release(ctx)
			w.logger.InfoContext(ctx, "worker.job.batches_reverted",
				slog.String("job_id", job.JobID),
				slog.Int64("execution_id", job.ExecutionID),
				slog.Int("migration_id", job.MigrationID),
				slog.Int("attempt", job.Attempt),
				slog.String("status", migration.ExecFailed),
			)
		} else {
			span.RecordError(outcome.Err)
			span.SetStatus(codes.Error, errMsg)
			_ = w.queue.MarkDead(ctx, job.ID, errMsg)
			_ = lease.Release(ctx)
			w.logger.ErrorContext(ctx, "worker.job.failed",
				slog.String("job_id", job.JobID),
				slog.Int64("execution_id", job.ExecutionID),
				slog.Int("migration_id", job.MigrationID),
				slog.Int("attempt", job.Attempt),
				slog.String("error", errMsg),
				slog.String("status", migration.ExecFailed),
			)
		}
	}
}
