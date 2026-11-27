package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

func newJobID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) CreateWorkflowDraft(ctx context.Context, userID, databaseID int, title, upSQL, downSQL string, irreversible bool, reason, mode string, cfg BatchConfig) (*Migration, *Revision, error) {
	if err := s.checkAccess(ctx, userID, databaseID, true); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(upSQL) == "" {
		return nil, nil, fmt.Errorf("%w: title and up SQL are required", InvalidInput)
	}
	if mode == "" {
		mode = ModeSingle
	}
	cfg, err := normalizeBatchConfig(mode, cfg)
	if err != nil {
		return nil, nil, err
	}
	return s.store.CreateDraftWithRevision(ctx, databaseID, title, upSQL, downSQL, irreversible, reason, mode, cfg, userID)
}

func (s *Service) UpdateDefinition(ctx context.Context, userID, migrationID int, title, upSQL, downSQL string, irreversible bool, reason, mode string, cfg BatchConfig) (*Migration, *Revision, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, nil, err
	}
	if mode == "" {
		mode = ModeSingle
	}
	cfg, err = normalizeBatchConfig(mode, cfg)
	if err != nil {
		return nil, nil, err
	}
	r, err := s.store.UpdateDraftRevision(ctx, migrationID, title, upSQL, downSQL, irreversible, reason, mode, cfg, userID)
	if err != nil {
		return nil, nil, err
	}
	m, err = s.store.GetByID(ctx, migrationID)
	return m, r, err
}

func (s *Service) Submit(ctx context.Context, userID, migrationID int, downSQL string, irreversible bool, reason, mode string, cfg BatchConfig) (*ApprovalRequest, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	r, err := s.store.GetLatestRevision(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if r.Status != StatusDraft && r.Status != StatusPendingReview {
		return nil, fmt.Errorf("%w: revision is %s", ErrInvalidState, r.Status)
	}
	if mode == "" {
		mode = r.Mode
	}
	if cfg.Column == "" && cfg.Size == 0 {
		cfg = r.BatchConfig
	}
	cfg, err = normalizeBatchConfig(mode, cfg)
	if err != nil {
		return nil, err
	}
	if irreversible {
		if strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("%w: irreversible migrations require a reason", InvalidInput)
		}
		downSQL = ""
	} else if strings.TrimSpace(downSQL) == "" {
		return nil, fmt.Errorf("%w: reversible migrations require down SQL", InvalidInput)
	}
	if err := validateBatchSQL(mode, r.UpSQL, downSQL); err != nil {
		return nil, err
	}
	if err := ValidateWorkflowSQL(mode, r.UpSQL, downSQL, cfg); err != nil {
		return nil, err
	}
	r, err = s.store.UpdateDraftRevision(ctx, migrationID, m.Title, r.UpSQL, downSQL, irreversible, reason, mode, cfg, userID)
	if err != nil {
		return nil, err
	}
	groups, err := s.store.GetApprovalSnapshot(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("%w: no groups have access", InvalidInput)
	}
	req, err := s.store.CreateApprovalRequest(ctx, migrationID, r.ID, ActionUp, nil, userID, groups, true)
	if err == nil {
		s.logger.InfoContext(ctx, "migration.submit",
			"migration_id", migrationID, "revision_id", r.ID, "database", fmt.Sprintf("%d", m.DatabaseID),
			"actor", fmt.Sprintf("%d", userID), "status", StatusPendingReview)
	}
	return req, err
}

func (s *Service) Approve(ctx context.Context, userID int, requestID int64) (*ApprovalRequest, error) {
	req, err := s.store.GetApprovalRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	m, err := s.store.GetByID(ctx, req.MigrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, false); err != nil {
		return nil, err
	}
	updated, err := s.store.ApproveApprovalGroups(ctx, requestID, userID)
	if err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "migration.approve",
		"migration_id", m.ID, "approval_request_id", requestID, "database", fmt.Sprintf("%d", m.DatabaseID),
		"actor", fmt.Sprintf("%d", userID), "status", updated.Status)
	return updated, nil
}

func (s *Service) GetApproval(ctx context.Context, userID int, requestID int64) (*ApprovalRequest, error) {
	req, err := s.store.GetApprovalRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	m, err := s.store.GetByID(ctx, req.MigrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, false); err != nil {
		return nil, err
	}
	return req, nil
}

func (s *Service) GetWorkflow(ctx context.Context, userID, migrationID int) (*Migration, *Revision, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, false); err != nil {
		return nil, nil, err
	}
	r, err := s.store.GetLatestRevision(ctx, migrationID)
	return m, r, err
}

func (s *Service) ExecuteUp(ctx context.Context, userID, migrationID int, confirm bool, trace string) (*Execution, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	r, err := s.store.GetLatestRevision(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if r.Status != StatusApproved {
		return nil, ErrApprovalRequired
	}
	if r.Irreversible && !confirm {
		return nil, ErrIrreversible
	}
	req, err := s.store.LatestApprovalRequest(ctx, r.ID, ActionUp, nil)
	if err != nil {
		return nil, err
	}
	if req.Status != ApprovalApproved {
		return nil, ErrApprovalPending
	}
	dbType, active, err := s.store.GetDatabaseTarget(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("%w: database inactive", InvalidInput)
	}
	if !strings.EqualFold(dbType, "postgresql") {
		return nil, ErrTargetNotPostgres
	}
	blocked, status, err := s.store.HasBlockingExecution(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, fmt.Errorf("%w: execution is %s", ErrJobInProgress, status)
	}
	return s.executeJob(ctx, m, r, ActionUp, req.ID, nil, r.UpSQL, userID, trace)
}

func (s *Service) executeJob(ctx context.Context, m *Migration, r *Revision, action string, approval int64, rollbackOf *int64, sqlText string, userID int, trace string) (*Execution, error) {
	jobID, err := newJobID()
	if err != nil {
		return nil, err
	}
	e, err := s.store.CreateExecution(ctx, jobID, m.ID, r.ID, m.DatabaseID, action, r.Mode, r.BatchConfig, &approval, rollbackOf, sqlText, userID)
	if err != nil {
		return nil, err
	}
	job := JobMessage{
		JobID:        jobID,
		ExecutionID:  e.ID,
		DatabaseID:   m.DatabaseID,
		MigrationID:  m.ID,
		RevisionID:   r.ID,
		RevisionNo:   r.RevisionNo,
		Action:       action,
		Mode:         r.Mode,
		BatchConfig:  r.BatchConfig,
		SQL:          sqlText,
		DownSQL:      r.DownSQL,
		Irreversible: r.Irreversible,
		RequestedBy:  userID,
		TraceContext: trace,
	}
	if _, err := s.publisher.PublishJob(ctx, job); err != nil {
		_ = s.store.RecordExecutionResult(context.Background(), ExecutionResult{JobID: jobID, Attempt: 1, Status: ExecFailed, ErrorMessage: err.Error()})
		return e, fmt.Errorf("publish migration job: %w", err)
	}
	eventName := "migration.execute"
	switch action {
	case ActionDown:
		eventName = "migration.rollback"
	case ActionForward:
		eventName = "migration.forward"
	}
	s.logger.InfoContext(ctx, eventName,
		"job_id", jobID, "execution_id", e.ID, "migration_id", m.ID,
		"database", fmt.Sprintf("%d", m.DatabaseID), "actor", fmt.Sprintf("%d", userID), "status", ExecPending)
	return e, nil
}

func (s *Service) RecordExecution(ctx context.Context, result ExecutionResult) error {
	return s.store.RecordExecutionResult(ctx, result)
}

func (s *Service) History(ctx context.Context, userID, databaseID int, hide bool) ([]Execution, error) {
	if err := s.checkAccess(ctx, userID, databaseID, false); err != nil {
		return nil, err
	}
	return s.store.ListExecutionHistory(ctx, databaseID, hide)
}

func (s *Service) GetExecution(ctx context.Context, userID int, executionID int64) (*Execution, error) {
	e, err := s.store.GetExecution(ctx, executionID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, e.DatabaseID, false); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Service) CheckExecutionAccess(ctx context.Context, userID int, e *Execution) error {
	return s.checkAccess(ctx, userID, e.DatabaseID, false)
}

func (s *Service) CheckExecutionEditAccess(ctx context.Context, userID int, e *Execution) error {
	return s.checkAccess(ctx, userID, e.DatabaseID, true)
}

func (s *Service) RequestRollback(ctx context.Context, userID, migrationID int) (*ApprovalRequest, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	active, err := s.store.GetActiveMigration(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if active.ID != m.ID {
		return nil, fmt.Errorf("%w: only the latest active migration can be rolled back", ErrInvalidState)
	}
	applied, err := s.store.GetLatestAppliedExecution(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	r, err := s.store.GetRevisionByID(ctx, applied.RevisionID)
	if err != nil {
		return nil, err
	}
	if r.Irreversible || strings.TrimSpace(r.DownSQL) == "" {
		return nil, ErrNotReversible
	}
	groups, err := s.store.GetApprovalSnapshot(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	req, err := s.store.CreateApprovalRequest(ctx, migrationID, r.ID, ActionDown, &applied.ID, userID, groups, false)
	if err == nil {
		s.logger.InfoContext(ctx, "migration.rollback.requested",
			"migration_id", migrationID, "revision_id", r.ID, "database", fmt.Sprintf("%d", m.DatabaseID),
			"actor", fmt.Sprintf("%d", userID), "status", ApprovalPending)
	}
	return req, err
}

func (s *Service) ExecuteRollback(ctx context.Context, userID, migrationID int, trace string) (*Execution, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	blocked, status, err := s.store.HasBlockingExecution(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, fmt.Errorf("%w: execution is %s", ErrJobInProgress, status)
	}
	active, err := s.store.GetActiveMigration(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if active.ID != m.ID {
		return nil, fmt.Errorf("%w: only the latest active migration can be rolled back", ErrInvalidState)
	}
	applied, err := s.store.GetLatestAppliedExecution(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	r, err := s.store.GetRevisionByID(ctx, applied.RevisionID)
	if err != nil {
		return nil, err
	}
	req, err := s.store.LatestApprovalRequest(ctx, r.ID, ActionDown, &applied.ID)
	if err != nil {
		return nil, err
	}
	if req.Status != ApprovalApproved {
		return nil, ErrApprovalPending
	}
	return s.executeJob(ctx, m, r, ActionDown, req.ID, &applied.ID, r.DownSQL, userID, trace)
}

func (s *Service) RequestForward(ctx context.Context, userID, migrationID int) (*ApprovalRequest, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	olderInactive, err := s.store.HasOlderInactiveMigration(ctx, m.DatabaseID, m.ID)
	if err != nil {
		return nil, err
	}
	if olderInactive {
		return nil, fmt.Errorf("%w: cannot forward out of order; older migrations must be forwarded first", ErrInvalidState)
	}
	down, err := s.store.GetLatestDownExecution(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	r, err := s.store.GetRevisionByID(ctx, down.RevisionID)
	if err != nil {
		return nil, err
	}
	groups, err := s.store.GetApprovalSnapshot(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	req, err := s.store.CreateApprovalRequest(ctx, migrationID, r.ID, ActionForward, &down.ID, userID, groups, false)
	if err == nil {
		s.logger.InfoContext(ctx, "migration.forward.requested",
			"migration_id", migrationID, "revision_id", r.ID, "database", fmt.Sprintf("%d", m.DatabaseID),
			"actor", fmt.Sprintf("%d", userID), "status", ApprovalPending)
	}
	return req, err
}

func (s *Service) ExecuteForward(ctx context.Context, userID, migrationID int, trace string) (*Execution, error) {
	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}
	olderInactive, err := s.store.HasOlderInactiveMigration(ctx, m.DatabaseID, m.ID)
	if err != nil {
		return nil, err
	}
	if olderInactive {
		return nil, fmt.Errorf("%w: cannot forward out of order; older migrations must be forwarded first", ErrInvalidState)
	}
	blocked, status, err := s.store.HasBlockingExecution(ctx, m.DatabaseID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, fmt.Errorf("%w: execution is %s", ErrJobInProgress, status)
	}
	down, err := s.store.GetLatestDownExecution(ctx, migrationID)
	if err != nil {
		return nil, err
	}
	r, err := s.store.GetRevisionByID(ctx, down.RevisionID)
	if err != nil {
		return nil, err
	}
	req, err := s.store.LatestApprovalRequest(ctx, r.ID, ActionForward, &down.ID)
	if err != nil {
		return nil, err
	}
	if req.Status != ApprovalApproved {
		return nil, ErrApprovalPending
	}
	return s.executeJob(ctx, m, r, ActionForward, req.ID, &down.ID, r.UpSQL, userID, trace)
}