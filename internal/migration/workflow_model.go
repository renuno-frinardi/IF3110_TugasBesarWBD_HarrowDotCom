package migration

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("migration not found")
	ErrInvalidState      = errors.New("migration is not in a valid state")
	ErrApprovalRequired  = errors.New("migration approval is required")
	ErrApprovalPending   = errors.New("migration approval is still pending")
	ErrApprovalRevoked   = errors.New("migration approval was revoked")
	ErrNotReversible     = errors.New("migration is not reversible")
	ErrIrreversible      = errors.New("migration is irreversible")
	ErrNoActiveMigration = errors.New("no active migration to roll back")
	ErrJobInProgress     = errors.New("another migration job is still active")
	ErrForwardBlocked    = errors.New("a newer migration is already active")
	ErrTargetNotPostgres = errors.New("migration target must be PostgreSQL")
)

const (
	ActionUp      = "up"
	ActionDown    = "down"
	ActionForward = "forward"
	ModeSingle    = "single"
	ModeNormal    = "normal"
	ModeBatch     = "batch"

	ExecPending           = "pending"
	ExecRunning           = "running"
	ExecSuccess           = "success"
	ExecFailed            = "failed"
	ExecPartial           = "partial"
	ExecPartiallyReverted = "partially_reverted"

	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalRevoked  = "revoked"
)

type BatchConfig struct {
	Column string `json:"cursor_column"`
	Size   int    `json:"batch_size"`
}

func (c *BatchConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		CursorColumn string `json:"cursor_column"`
		BatchSize    int    `json:"batch_size"`
		Column       string `json:"column"`
		Size         int    `json:"size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Column = raw.CursorColumn
	if c.Column == "" {
		c.Column = raw.Column
	}
	c.Size = raw.BatchSize
	if c.Size == 0 {
		c.Size = raw.Size
	}
	return nil
}

type Revision struct {
	ID                 int64       `json:"id"`
	MigrationID        int         `json:"migration_id"`
	RevisionNo         int         `json:"revision_no"`
	UpSQL              string      `json:"up_sql"`
	DownSQL            string      `json:"down_sql,omitempty"`
	Irreversible       bool        `json:"irreversible"`
	IrreversibleReason string      `json:"irreversible_reason,omitempty"`
	Mode               string      `json:"mode"`
	BatchConfig        BatchConfig `json:"batch_config"`
	Status             string      `json:"status"`
	ContentHash        string      `json:"content_hash"`
	CreatedBy          int         `json:"created_by"`
	CreatedAt          time.Time   `json:"created_at"`
	ApprovedAt         *time.Time  `json:"approved_at,omitempty"`
	SupersededAt       *time.Time  `json:"superseded_at,omitempty"`
}

type ApprovalGroup struct {
	ID             int64      `json:"id"`
	GroupID        int64      `json:"group_id"`
	GroupName      string     `json:"group_name"`
	LeaderUserID   *int       `json:"leader_user_id,omitempty"`
	LeaderUsername *string    `json:"leader_username,omitempty"`
	Status         string     `json:"status"`
	ApprovedBy     *int       `json:"approved_by,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
}

type ApprovalRequest struct {
	ID                int64           `json:"id"`
	MigrationID       int             `json:"migration_id"`
	RevisionID        int64           `json:"revision_id"`
	Action            string          `json:"action"`
	TargetExecutionID *int64          `json:"target_execution_id,omitempty"`
	RequestedBy       int             `json:"requested_by"`
	Status            string          `json:"status"`
	CreatedAt         time.Time       `json:"created_at"`
	DecidedAt         *time.Time      `json:"decided_at,omitempty"`
	Groups            []ApprovalGroup `json:"groups"`
}

type GroupSnapshot struct {
	GroupID      int64  `json:"group_id"`
	GroupName    string `json:"group_name"`
	LeaderUserID *int   `json:"leader_user_id,omitempty"`
}

type ExecutionBatch struct {
	ID           int64      `json:"id"`
	ExecutionID  int64      `json:"execution_id"`
	BatchNo      int        `json:"batch_no"`
	CursorFrom   string     `json:"cursor_from"`
	CursorTo     string     `json:"cursor_to"`
	RowsAffected int64      `json:"rows_affected"`
	Status       string     `json:"status"`
	CommittedAt  time.Time  `json:"committed_at"`
	RevertedAt   *time.Time `json:"reverted_at,omitempty"`
}

type Execution struct {
	ID                int64            `json:"id"`
	ExecutionID       int64            `json:"execution_id"`
	JobID             string           `json:"job_id"`
	MigrationID       int              `json:"migration_id"`
	RevisionID        int64            `json:"revision_id"`
	RevisionNo        int              `json:"revision"`
	DatabaseID        int              `json:"database_id"`
	Action            string           `json:"action"`
	Attempt           int              `json:"attempt"`
	Mode              string           `json:"mode"`
	BatchConfig       BatchConfig      `json:"batch_config"`
	ApprovalRequestID *int64           `json:"approval_request_id,omitempty"`
	RollbackOf        *int64           `json:"rollback_of,omitempty"`
	SQLExecuted       string           `json:"sql_executed"`
	ExecutedSQL       string           `json:"executed_sql"`
	Status            string           `json:"status"`
	RowsCommitted     int64            `json:"rows_committed"`
	ErrorMessage      *string          `json:"error_message,omitempty"`
	RequestedBy       *int             `json:"requested_by,omitempty"`
	StartedAt         *time.Time       `json:"started_at,omitempty"`
	FinishedAt        *time.Time       `json:"finished_at,omitempty"`
	ExecutedAt        *time.Time       `json:"executed_at,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	Title             string           `json:"title"`
	Version           *int             `json:"version,omitempty"`
	RequestedByName   *string          `json:"requested_by_name,omitempty"`
	Batches           []ExecutionBatch `json:"batches,omitempty"`
}

type ExecutionResult struct {
	JobID          string
	ExecutionID    int64
	Attempt        int
	JobKind        string
	WorkerID       string
	Status         string
	RowsCommitted  int64
	ErrorMessage   string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	RetryScheduled bool
	AllowReopen    bool
}

type BatchResult struct {
	JobID        string
	ExecutionID  int64
	BatchNo      int
	CursorFrom   string
	CursorTo     string
	RowsAffected int64
	Status       string
}

type JobMessage struct {
	JobID        string
	ExecutionID  int64
	DatabaseID   int
	MigrationID  int
	RevisionID   int64
	RevisionNo   int
	Action       string
	Mode         string
	BatchConfig  BatchConfig
	SQL          string
	DownSQL      string
	Irreversible bool
	RequestedBy  int
	TraceContext string
}