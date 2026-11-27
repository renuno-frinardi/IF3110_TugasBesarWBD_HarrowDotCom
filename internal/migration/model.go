package migration

import "time"

const (
	S_Draft    string = "draft"
	S_Running  string = "running"
	S_Executed string = "executed"
	S_Failed   string = "failed"

	StatusDraft         = S_Draft
	StatusRunning       = S_Running
	StatusExecuted      = S_Executed
	StatusFailed        = S_Failed
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusSuperseded    = "superseded"
)

type Migration struct {
	ID           int
	DatabaseID   int
	Version      *int
	Title        string
	SQL          string
	Status       string
	Active       bool
	SupersededAt *time.Time

	CreatedBy  int
	ExecutedBy *int
	CreatedAt  time.Time
	UpdatedAt  *time.Time
	StartTime  *time.Time
	FinishTime *time.Time

	ErrorMessage *string

	ExecutedByName *string
}
