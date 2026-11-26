package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/async"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

type migrationDefinitionRequest struct {
	DatabaseID         int                   `json:"database_id"`
	Title              string                `json:"title"`
	Name               string                `json:"name"`
	UpSQL              string                `json:"up_sql"`
	SQL                string                `json:"sql"`
	DownSQL            string                `json:"down_sql"`
	SQLDown            string                `json:"sql_down"`
	Irreversible       bool                  `json:"irreversible"`
	IrreversibleReason string                `json:"irreversible_reason"`
	Mode               string                `json:"mode"`
	BatchConfig        migration.BatchConfig `json:"batch_config"`
	Revision           int                   `json:"revision"`
}

type migrationSubmitRequest struct {
	DownSQL            *string                `json:"down_sql"`
	SQLDown            *string                `json:"sql_down"`
	Irreversible       *bool                  `json:"irreversible"`
	IrreversibleReason *string                `json:"irreversible_reason"`
	Mode               string                 `json:"mode"`
	BatchConfig        *migration.BatchConfig `json:"batch_config"`
	Revision           int                    `json:"revision"`
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (app *application) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid JSON body"})
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "request body must contain one JSON object"})
		return false
	}
	return true
}

func (app *application) pathInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	v, e := strconv.Atoi(r.PathValue(name))
	if e != nil || v < 1 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid " + name})
		return 0, false
	}
	return v, true
}

func (app *application) pathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, e := strconv.ParseInt(r.PathValue(name), 10, 64)
	if e != nil || v < 1 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid " + name})
		return 0, false
	}
	return v, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (app *application) writeAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, migration.ErrNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, migration.Forbidden):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, migration.InvalidInput), errors.Is(err, async.ErrCancelUnsafe):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, migration.ErrApprovalPending), errors.Is(err, migration.ErrApprovalRequired), errors.Is(err, migration.ErrInvalidState), errors.Is(err, migration.ErrJobInProgress), errors.Is(err, async.ErrDatabaseBlocked):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, migration.ErrIrreversible), errors.Is(err, migration.ErrNotReversible), errors.Is(err, migration.ErrTargetNotPostgres):
		status, message = http.StatusBadRequest, err.Error()
	}
	writeJSON(w, status, apiError{Error: message})
}

type migrationListItem struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Revision int    `json:"revision"`
	Workflow string `json:"workflow"`
	Action   string `json:"action"`
}

type migrationRevision struct {
	Revision int    `json:"revision"`
	SQLUp    string `json:"sql_up"`
	SQLDown  string `json:"sql_down"`
}

type migrationApproval struct {
	GroupName  string  `json:"group_name"`
	LeaderName *string `json:"leader_name"`
	Status     string  `json:"status"`
}

type migrationDetail struct {
	ID                 int                   `json:"id"`
	DatabaseID         int                   `json:"database_id"`
	Name               string                `json:"name"`
	Revision           int                   `json:"revision"`
	Workflow           string                `json:"workflow"`
	Action             string                `json:"action"`
	SQLUp              string                `json:"sql_up"`
	SQLDown            string                `json:"sql_down"`
	Irreversible       bool                  `json:"irreversible"`
	IrreversibleReason string                `json:"irreversible_reason"`
	Mode               string                `json:"mode"`
	BatchConfig        migration.BatchConfig `json:"batch_config"`
	Revisions          []migrationRevision   `json:"revisions"`
	Approvals          []migrationApproval   `json:"approvals"`
	AllowedActions     []string              `json:"allowed_actions"`
	LatestJobID        *string               `json:"latest_job_id"`
}

func (app *application) migrationDatabaseListHandler(w http.ResponseWriter, r *http.Request) {
	dbID, ok := app.pathInt(w, r, "db")
	if !ok {
		return
	}
	if !app.checkDatabasePermission(w, r, int64(dbID), "", authz.ActionDBView) {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	principal := authz.Principal{UserID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin}
	canCreate := app.authorizer.Can(r.Context(), principal, authz.ActionMigrationCreate, authz.Resource{DatabaseID: int64(dbID)}) == nil

	rows, err := app.stores.pgPool.Query(r.Context(), `
		SELECT m.id,m.title,m.status,COALESCE(r.revision_no,1),COALESCE(a.action,'up')
		FROM migrations m LEFT JOIN LATERAL (
			SELECT revision_no FROM migration_revisions WHERE migration_id=m.id ORDER BY revision_no DESC LIMIT 1
		) r ON TRUE
		LEFT JOIN LATERAL (
			SELECT action FROM approval_requests WHERE migration_id=m.id ORDER BY created_at DESC,id DESC LIMIT 1
		) a ON TRUE
		WHERE m.database_id=$1 ORDER BY m.updated_at DESC NULLS LAST,m.id DESC`, dbID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	defer rows.Close()
	items := make([]migrationListItem, 0)
	for rows.Next() {
		var item migrationListItem
		var workflow, action string
		if err := rows.Scan(&item.ID, &item.Name, &workflow, &item.Revision, &action); err != nil {
			app.writeAPIError(w, err)
			return
		}
		item.Workflow = workflow
		item.Action = action
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "can_create": canCreate})
}

func (app *application) migrationDraftCreateHandler(w http.ResponseWriter, r *http.Request) {
	dbID, ok := app.pathInt(w, r, "db")
	if !ok {
		return
	}
	var req migrationDefinitionRequest
	if !app.decodeJSON(w, r, &req) {
		return
	}
	req.DatabaseID = dbID
	u, _ := auth.UserFromContext(r.Context())
	title := firstNonEmpty(req.Title, req.Name)
	downSQL := firstNonEmpty(req.DownSQL, req.SQLDown)
	m, _, err := app.migService.CreateWorkflowDraft(r.Context(), int(u.ID), dbID, title,
		firstNonEmpty(req.UpSQL, req.SQL), downSQL, req.Irreversible, req.IrreversibleReason, req.Mode, req.BatchConfig)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": m.ID})
}

func (app *application) migrationDetailHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	m, latest, err := app.migService.GetWorkflow(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	app.writeMigrationDetail(w, r, m, latest)
}

func (app *application) writeMigrationDetail(w http.ResponseWriter, r *http.Request, m *migration.Migration, latest *migration.Revision) {
	u, _ := auth.UserFromContext(r.Context())
	displayMode := latest.Mode
	if displayMode == migration.ModeSingle || displayMode == "" {
		displayMode = migration.ModeNormal
	}
	detail := migrationDetail{
		ID: m.ID, DatabaseID: m.DatabaseID, Name: m.Title, Revision: latest.RevisionNo,
		Workflow: m.Status, Action: migration.ActionUp, SQLUp: latest.UpSQL, SQLDown: latest.DownSQL,
		Irreversible: latest.Irreversible, IrreversibleReason: latest.IrreversibleReason,
		Mode: displayMode, BatchConfig: latest.BatchConfig,
		Revisions: []migrationRevision{}, Approvals: []migrationApproval{}, AllowedActions: []string{}, LatestJobID: nil,
	}
	rows, err := app.stores.pgPool.Query(r.Context(), `SELECT revision_no,up_sql,COALESCE(down_sql,'') FROM migration_revisions WHERE migration_id=$1 ORDER BY revision_no`, m.ID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	for rows.Next() {
		var rev migrationRevision
		if err := rows.Scan(&rev.Revision, &rev.SQLUp, &rev.SQLDown); err != nil {
			rows.Close()
			app.writeAPIError(w, err)
			return
		}
		detail.Revisions = append(detail.Revisions, rev)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		app.writeAPIError(w, err)
		return
	}
	rows.Close()
	var latestReq *migration.ApprovalRequest
	var latestReqID int64
	err = app.stores.pgPool.QueryRow(r.Context(), `SELECT id FROM approval_requests WHERE migration_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, m.ID).Scan(&latestReqID)
	if err == nil {
		req, reqErr := app.migStore.GetApprovalRequest(r.Context(), latestReqID)
		if reqErr != nil {
			app.writeAPIError(w, reqErr)
			return
		}
		latestReq = req
		detail.Action = req.Action
		for _, g := range req.Groups {
			detail.Approvals = append(detail.Approvals, migrationApproval{GroupName: g.GroupName, LeaderName: g.LeaderUsername, Status: g.Status})
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		app.writeAPIError(w, err)
		return
	}

	var pendingMyApprove int
	if err := app.stores.pgPool.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM approval_groups ag
		JOIN approval_requests ar ON ar.id=ag.approval_request_id
		WHERE ar.migration_id=$1 AND ar.status='pending' AND ag.status='pending' AND ag.leader_user_id=$2
	`, m.ID, u.ID).Scan(&pendingMyApprove); err != nil {
		app.writeAPIError(w, err)
		return
	}

	allowed := []string{}
	if pendingMyApprove > 0 {
		allowed = append(allowed, "approve")
	}

	switch m.Status {
	case migration.StatusDraft:
		allowed = append(allowed, "edit", "submit")
	case migration.StatusApproved:
		allowed = append(allowed, "execute")
	case migration.StatusExecuted:
		if latestReq != nil && latestReq.Status == migration.ApprovalApproved && (latestReq.Action == migration.ActionDown || latestReq.Action == migration.ActionForward) {
			var usedCount int
			if err := app.stores.pgPool.QueryRow(r.Context(), `SELECT COUNT(*) FROM executions WHERE approval_request_id=$1`, latestReq.ID).Scan(&usedCount); err != nil {
				app.writeAPIError(w, err)
				return
			}
			if usedCount == 0 {
				allowed = append(allowed, "execute")
			}
		}
		if m.Active && !latest.Irreversible {
			activeMig, activeErr := app.migStore.GetActiveMigration(r.Context(), m.DatabaseID)
			if activeErr != nil {
				app.writeAPIError(w, activeErr)
				return
			}
			if activeMig.ID == m.ID {
				allowed = append(allowed, "rollback")
			}
		}
	case migration.StatusFailed:
		allowed = append(allowed, "resume")
	}
	var latestJob string
	if err := app.stores.pgPool.QueryRow(r.Context(), `SELECT job_id FROM executions WHERE migration_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, m.ID).Scan(&latestJob); err == nil {
		detail.LatestJobID = &latestJob
	} else if errors.Is(err, pgx.ErrNoRows) && (m.Status == migration.StatusExecuted || m.Status == migration.StatusFailed) {
		legacyJob := fmt.Sprintf("legacy-M%d", m.ID)
		detail.LatestJobID = &legacyJob
	} else if !errors.Is(err, pgx.ErrNoRows) {
		app.writeAPIError(w, err)
		return
	}
	if m.Status == migration.StatusExecuted && !m.Active {
		var downCount int
		if err := app.stores.pgPool.QueryRow(r.Context(), `SELECT COUNT(*) FROM executions WHERE migration_id=$1 AND action='down' AND status='success' AND NOT EXISTS (SELECT 1 FROM executions f WHERE f.rollback_of=executions.id AND f.action='forward' AND f.status='success')`, m.ID).Scan(&downCount); err != nil {
			app.writeAPIError(w, err)
			return
		}
		if downCount > 0 {
			allowed = append(allowed, "forward")
		}
	}
	detail.AllowedActions = allowed
	writeJSON(w, http.StatusOK, detail)
}

func (app *application) migrationDraftUpdateHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	var req migrationDefinitionRequest
	if !app.decodeJSON(w, r, &req) {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	m, _, err := app.migService.GetWorkflow(r.Context(), int(u.ID), id)
	if err == nil {
		title := firstNonEmpty(req.Title, req.Name)
		if strings.TrimSpace(title) == "" {
			title = m.Title
		}
		downSQL := firstNonEmpty(req.DownSQL, req.SQLDown)
		m, _, err = app.migService.UpdateDefinition(r.Context(), int(u.ID), id, title,
			firstNonEmpty(req.UpSQL, req.SQL), downSQL, req.Irreversible, req.IrreversibleReason, req.Mode, req.BatchConfig)
	}
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": m.ID})
}

func (app *application) migrationReviewSubmitHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	var req migrationSubmitRequest
	if !app.decodeJSON(w, r, &req) {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	_, latest, err := app.migService.GetWorkflow(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}

	downSQL := latest.DownSQL
	if req.DownSQL != nil {
		downSQL = *req.DownSQL
	} else if req.SQLDown != nil {
		downSQL = *req.SQLDown
	}
	irreversible := latest.Irreversible
	if req.Irreversible != nil {
		irreversible = *req.Irreversible
	}
	reason := latest.IrreversibleReason
	if req.IrreversibleReason != nil {
		reason = *req.IrreversibleReason
	}
	mode := req.Mode
	if strings.TrimSpace(mode) == "" {
		mode = latest.Mode
	}
	cfg := latest.BatchConfig
	if req.BatchConfig != nil {
		cfg = *req.BatchConfig
	}

	_, err = app.migService.Submit(r.Context(), int(u.ID), id, downSQL, irreversible, reason, mode, cfg)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
}

func (app *application) migrationApprovalHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	var reqID int64
	err := app.stores.pgPool.QueryRow(r.Context(), `SELECT id FROM approval_requests WHERE migration_id=$1 AND status='pending' ORDER BY created_at DESC,id DESC LIMIT 1`, id).Scan(&reqID)
	if err != nil {
		app.writeAPIError(w, migration.ErrNotFound)
		return
	}
	_, err = app.migService.Approve(r.Context(), int(u.ID), reqID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (app *application) migrationExecutionHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	traceCtx := telemetry.InjectTraceContext(r.Context())

	var action string
	var reqID int64
	err := app.stores.pgPool.QueryRow(r.Context(), `
		SELECT id, action FROM approval_requests
		WHERE migration_id = $1 AND status = 'approved'
		  AND NOT EXISTS (SELECT 1 FROM executions e WHERE e.approval_request_id = approval_requests.id)
		ORDER BY created_at DESC, id DESC LIMIT 1
	`, id).Scan(&reqID, &action)

	var e *migration.Execution
	if err == nil && action == migration.ActionDown {
		e, err = app.migService.ExecuteRollback(r.Context(), int(u.ID), id, traceCtx)
	} else if err == nil && action == migration.ActionForward {
		e, err = app.migService.ExecuteForward(r.Context(), int(u.ID), id, traceCtx)
	} else {
		e, err = app.migService.ExecuteUp(r.Context(), int(u.ID), id, true, traceCtx)
	}
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": e.JobID, "execution_id": e.ID, "status": e.Status})
}

func (app *application) migrationRollbackRequestHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	req, err := app.migService.RequestRollback(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": req.ID})
}

func (app *application) migrationForwardRequestHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	req, err := app.migService.RequestForward(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": req.ID})
}

func (app *application) reviewListHandler(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFromContext(r.Context())
	rows, err := app.stores.pgPool.Query(r.Context(), `
		SELECT ar.id,ar.migration_id,ar.revision_id,ar.action,ar.status,ar.created_at,m.title,COALESCE(mr.revision_no,1)
		FROM approval_requests ar
		JOIN approval_groups ag ON ag.approval_request_id=ar.id
		JOIN migrations m ON m.id=ar.migration_id
		LEFT JOIN migration_revisions mr ON mr.id=ar.revision_id
		WHERE ar.status='pending' AND ag.status='pending' AND ag.leader_user_id=$1
		GROUP BY ar.id,m.title,mr.revision_no ORDER BY ar.created_at DESC`, u.ID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var reqID int64
		var migrationID, revNo int
		var revisionID int64
		var action, status, title string
		var createdAt any
		if err := rows.Scan(&reqID, &migrationID, &revisionID, &action, &status, &createdAt, &title, &revNo); err != nil {
			app.writeAPIError(w, err)
			return
		}
		items = append(items, map[string]any{
			"id":                  migrationID,
			"approval_request_id": reqID,
			"migration_id":        migrationID,
			"revision_id":         revisionID,
			"revision":            revNo,
			"name":                title,
			"action":              action,
			"workflow":            status,
			"status":              status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (app *application) migrationHistoryHandler(w http.ResponseWriter, r *http.Request) {
	dbID, ok := app.pathInt(w, r, "db")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	hide := strings.EqualFold(r.URL.Query().Get("hide_rollback"), "true")
	items, err := app.migService.History(r.Context(), int(u.ID), dbID, hide)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	if items == nil {
		items = []migration.Execution{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "database_id": dbID})
}

func (app *application) jobDetailHandler(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if strings.TrimSpace(jobID) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid id"})
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	e, err := app.migStore.GetExecutionByJobID(r.Context(), jobID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	if err := app.migService.CheckExecutionAccess(r.Context(), int(u.ID), e); err != nil {
		app.writeAPIError(w, err)
		return
	}
	batches, err := app.migStore.ListExecutionBatches(r.Context(), e.ID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	if batches == nil {
		batches = []migration.ExecutionBatch{}
	}

	committedBatches := 0
	var lastActive *migration.ExecutionBatch
	for i := range batches {
		if batches[i].Status == "committed" && batches[i].RevertedAt == nil {
			committedBatches++
			lastActive = &batches[i]
		}
	}

	canEdit := app.migService.CheckExecutionEditAccess(r.Context(), int(u.ID), e) == nil
	canRevert := false
	if strings.EqualFold(e.Mode, migration.ModeBatch) && e.RevisionID > 0 {
		if rev, revErr := app.migStore.GetRevisionByID(r.Context(), e.RevisionID); revErr == nil {
			canRevert = !rev.Irreversible && async.CanSafelyRevertBatchSQL(rev.DownSQL)
		}
	}

	allowed := []string{}
	if canEdit {
		switch e.Status {
		case migration.ExecFailed, migration.ExecPartial, migration.ExecPartiallyReverted:
			allowed = append(allowed, "resume")
			if canRevert && committedBatches > 0 && (e.Status == migration.ExecPartial || e.Status == migration.ExecPartiallyReverted) {
				allowed = append(allowed, "cancel-batches")
			}
		case migration.ExecRunning, migration.ExecPending:
			if canRevert {
				allowed = append(allowed, "cancel-batches")
			}
		}
	}

	checkpoint := any(nil)
	if lastActive != nil {
		checkpoint = map[string]any{
			"batch_no":  lastActive.BatchNo,
			"cursor":    lastActive.CursorTo,
			"cursor_to": lastActive.CursorTo,
			"status":    lastActive.Status,
		}
	} else if len(batches) > 0 {
		last := batches[len(batches)-1]
		checkpoint = map[string]any{
			"batch_no":  last.BatchNo,
			"cursor":    last.CursorTo,
			"cursor_to": last.CursorTo,
			"status":    last.Status,
		}
	}

	errText := ""
	if e.ErrorMessage != nil {
		errText = *e.ErrorMessage
	}

	payload := map[string]any{
		"id":                e.JobID,
		"job_id":            e.JobID,
		"status":            e.Status,
		"committed_batches": committedBatches,
		"committed_rows":    e.RowsCommitted,
		"progress":          map[string]any{"committed": e.RowsCommitted, "batches": committedBatches},
		"checkpoint":        checkpoint,
		"allowed_actions":   allowed,
		"execution_id":      e.ID,
		"migration_id":      e.MigrationID,
		"database_id":       e.DatabaseID,
		"action":            e.Action,
		"mode":              e.Mode,
		"attempt":           e.Attempt,
		"batches":           batches,
		"created_at":        e.CreatedAt,
		"started_at":        e.StartedAt,
		"finished_at":       e.FinishedAt,
	}
	if errText != "" {
		// Gunakan error_message (bukan "error") karena api.js menganggap field "error" sebagai kegagalan HTTP.
		payload["error_message"] = errText
	}
	writeJSON(w, http.StatusOK, payload)
}

func (app *application) jobResumeHandler(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	u, _ := auth.UserFromContext(r.Context())
	e, err := app.migStore.GetExecutionByJobID(r.Context(), jobID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	if err := app.migService.CheckExecutionEditAccess(r.Context(), int(u.ID), e); err != nil {
		app.writeAPIError(w, err)
		return
	}
	if e.Status != migration.ExecFailed && e.Status != migration.ExecPartial && e.Status != migration.ExecPartiallyReverted {
		app.writeAPIError(w, fmt.Errorf("%w: job cannot be resumed", migration.ErrInvalidState))
		return
	}
	if app.jobQueue != nil {
		rev, revErr := app.migStore.GetRevisionByID(r.Context(), e.RevisionID)
		if revErr != nil {
			app.writeAPIError(w, revErr)
			return
		}
		if _, pubErr := app.jobQueue.EnqueueResume(r.Context(), e, rev, int(u.ID)); pubErr != nil {
			app.writeAPIError(w, pubErr)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

func (app *application) jobBatchCancelHandler(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	u, _ := auth.UserFromContext(r.Context())
	e, err := app.migStore.GetExecutionByJobID(r.Context(), jobID)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	if err := app.migService.CheckExecutionEditAccess(r.Context(), int(u.ID), e); err != nil {
		app.writeAPIError(w, err)
		return
	}
	if app.jobQueue != nil {
		rev, revErr := app.migStore.GetRevisionByID(r.Context(), e.RevisionID)
		if revErr != nil {
			app.writeAPIError(w, revErr)
			return
		}
		if _, pubErr := app.jobQueue.EnqueueCancelBatches(r.Context(), e, rev, int(u.ID)); pubErr != nil {
			app.writeAPIError(w, pubErr)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

func (app *application) groupLeaderAPIHandler(w http.ResponseWriter, r *http.Request) {
	groupID, ok := app.pathInt64(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		UserID int64 `json:"user_id"`
	}
	if !app.decodeJSON(w, r, &body) {
		return
	}
	if body.UserID < 1 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid user_id"})
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	if err := app.adminStore.SetGroupLeader(r.Context(), u.ID, groupID, body.UserID); err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": groupID, "user_id": body.UserID})
}
