package async

import (
	"errors"
	"time"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

const (
	JobKindExecute       = "execute"
	JobKindResume        = "resume"
	JobKindCancelBatches = "cancel_batches"

	JobStatusQueued     = "queued"
	JobStatusProcessing = "processing"
	JobStatusCompleted  = "completed"
	JobStatusDead       = "dead"
	JobStatusCancelled  = "cancelled"

	LockStateActive           = "active"
	LockStateBlockedPartial   = "blocked_partial"
	LockStateBlockedUncertain = "blocked_uncertain"

	NotifyChannel = "harrow_jobs"
)

var (
	ErrCancelUnsafe    = errors.New("batch cancellation is unsafe or migration is irreversible")
	ErrDatabaseLocked  = errors.New("database migration lock is currently held by another worker")
	ErrDatabaseBlocked = errors.New("database is blocked by a partial or uncertain migration")
	ErrUncertainCommit = errors.New("transaction commit outcome is uncertain")
	ErrNoJobAvailable  = errors.New("no eligible job in queue")
)

type QueueJob struct {
	ID              int64
	JobID           string
	ExecutionID     int64
	DatabaseID      int
	MigrationID     int
	RevisionID      int64
	JobKind         string
	Action          string
	Mode            string
	BatchConfig     migration.BatchConfig
	SQL             string
	DownSQL         string
	Irreversible    bool
	RequestedBy     int
	TraceContext    string
	Status          string
	Attempt         int
	MaxAttempts     int
	NextRunAt       time.Time
	LockedBy        string
	LockedAt        *time.Time
	CancelRequested bool
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type TargetBatchCheckpoint struct {
	ExecutionID  int64
	BatchNo      int
	CursorFrom   string
	CursorTo     string
	RowsAffected int64
	Status       string
	UpdatedAt    time.Time
}
