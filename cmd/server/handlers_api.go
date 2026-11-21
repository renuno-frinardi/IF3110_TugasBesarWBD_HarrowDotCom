package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

type migrationDefinitionRequest struct {
	DatabaseID         int                   `json:"database_id"`
	Title              string                `json:"title"`
	UpSQL              string                `json:"up_sql"`
	SQL                string                `json:"sql"`
	DownSQL            string                `json:"down_sql"`
	Irreversible       bool                  `json:"irreversible"`
	IrreversibleReason string                `json:"irreversible_reason"`
	Mode               string                `json:"mode"`
	BatchConfig        migration.BatchConfig `json:"batch_config"`
}

type migrationSubmitRequest struct {
	DownSQL            string                `json:"down_sql"`
	Irreversible       bool                  `json:"irreversible"`
	IrreversibleReason string                `json:"irreversible_reason"`
	Mode               string                `json:"mode"`
	BatchConfig        migration.BatchConfig `json:"batch_config"`
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

func (app *application) migrationAPICreateHandler(w http.ResponseWriter, r *http.Request) {
	var req migrationDefinitionRequest
	if !app.decodeJSON(w, r, &req) {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	m, rn, err := app.migService.CreateWorkflowDraft(r.Context(), int(u.ID), req.DatabaseID, req.Title, firstNonEmpty(req.UpSQL, req.SQL), req.DownSQL, req.Irreversible, req.IrreversibleReason, req.Mode, req.BatchConfig)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"migration": m, "revision": rn})
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func (app *application) migrationAPIGetHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	m, v, err := app.migService.GetWorkflow(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"migration": m, "revision": v})
}
func (app *application) migrationAPIUpdateHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	var req migrationDefinitionRequest
	if !app.decodeJSON(w, r, &req) {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	m, v, err := app.migService.UpdateDefinition(r.Context(), int(u.ID), id, req.Title, firstNonEmpty(req.UpSQL, req.SQL), req.DownSQL, req.Irreversible, req.IrreversibleReason, req.Mode, req.BatchConfig)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"migration": m, "revision": v})
}

func (app *application) migrationAPISubmitHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	var req migrationSubmitRequest
	if !app.decodeJSON(w, r, &req) {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	approval, err := app.migService.Submit(r.Context(), int(u.ID), id, req.DownSQL, req.Irreversible, req.IrreversibleReason, req.Mode, req.BatchConfig)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, approval)
}

func (app *application) migrationAPIExecuteHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	e, err := app.migService.ExecuteUp(r.Context(), int(u.ID), id, false, "")
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, e)
}

func (app *application) migrationAPIRollbackRequestHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.RequestRollback(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (app *application) migrationAPIRollbackExecuteHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.ExecuteRollback(r.Context(), int(u.ID), id, "")
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (app *application) migrationAPIForwardRequestHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.RequestForward(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (app *application) migrationAPIForwardExecuteHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.ExecuteForward(r.Context(), int(u.ID), id, "")
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (app *application) approvalAPIGetHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt64(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.GetApproval(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (app *application) approvalAPIApproveHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt64(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.Approve(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (app *application) migrationHistoryAPIHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	hide := strings.EqualFold(r.URL.Query().Get("hide_rollback"), "true")
	v, err := app.migService.History(r.Context(), int(u.ID), id, hide)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	if v == nil {
		v = []migration.Execution{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": v})
}

func (app *application) executionAPIHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := app.pathInt64(w, r, "id")
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	v, err := app.migService.GetExecution(r.Context(), int(u.ID), id)
	if err != nil {
		app.writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (app *application) writeAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, migration.ErrNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, migration.Forbidden):
		status, message = http.StatusForbidden, err.Error()
	case errors.Is(err, migration.InvalidInput):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, migration.ErrApprovalPending), errors.Is(err, migration.ErrApprovalRequired), errors.Is(err, migration.ErrInvalidState), errors.Is(err, migration.ErrJobInProgress):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, migration.ErrIrreversible), errors.Is(err, migration.ErrNotReversible), errors.Is(err, migration.ErrTargetNotPostgres):
		status, message = http.StatusBadRequest, err.Error()
	}
	writeJSON(w, status, apiError{Error: message})
}