package migration

import "time"

const (
	S_Draft    string = "draft"
	S_Running  string = "running"
	S_Executed string = "executed"
	S_Failed   string = "failed"

	StatusDraft    = S_Draft
	StatusRunning  = S_Running
	StatusExecuted = S_Executed
	StatusFailed   = S_Failed
)

type Migration struct {
	ID         int
	DatabaseID int
	Version    *int
	Title      string
	SQL        string
	Status     string

	CreatedBy  int
	ExecutedBy *int
	CreatedAt  time.Time
	UpdatedAt  *time.Time
	StartTime  *time.Time
	FinishTime *time.Time

	ErrorMessage *string

	ExecutedByName *string
}
