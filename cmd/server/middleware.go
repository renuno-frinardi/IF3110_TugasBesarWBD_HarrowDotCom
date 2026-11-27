package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session", app.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.UserFromContext(r.Context())
		session, _ := auth.SessionFromContext(r.Context())

		writeJSON(w, http.StatusOK, map[string]any{
			"id":         user.ID,
			"username":   user.Username,
			"is_admin":   user.IsAdmin,
			"csrf_token": auth.CSRFToken(session),
		})
	}))

	// Public authentication endpoints
	mux.HandleFunc("/login", app.loginHandler)
	mux.HandleFunc("/signup", app.signupHandler)
	mux.HandleFunc("/logout", app.requireAuth(app.logoutHandler))

	// Protected workspace endpoints
	mux.HandleFunc("/", app.requireAuth(app.dashboardHandler))
	mux.HandleFunc("/connections", app.requireAuth(app.connectionsHandler))
	mux.HandleFunc("/connections/view", app.requireAuth(app.connectionDetailHandler))
	mux.HandleFunc("/connections/explore", app.requireAuth(app.exploreConnectionHandler))
	mux.HandleFunc("/connections/explore/insert", app.requireAuth(app.exploreInsertRowHandler))
	mux.HandleFunc("/connections/explore/update", app.requireAuth(app.exploreUpdateRowHandler))
	mux.HandleFunc("/connections/explore/delete", app.requireAuth(app.exploreDeleteRowHandler))

	// Protected connection management endpoints (PlatformAdmin only)
	mux.HandleFunc("/connections/new", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.newConnectionHandler)))
	mux.HandleFunc("/connections/edit", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.editConnectionHandler)))
	mux.HandleFunc("/connections/delete", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.deleteConnectionHandler)))
	mux.HandleFunc("/connections/toggle-active", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.toggleConnectionActiveHandler)))
	mux.HandleFunc("/connections/test", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.testConnectionHandler)))

	// Protected migration endpoints (DBView / MigrationCreate / MigrationExecute)
	mux.HandleFunc("/connections/migrations", app.requireAuth(app.migrationsListHandler))
	mux.HandleFunc("/connections/migrations/create", app.requireAuth(app.migrationCreateHandler))
	mux.HandleFunc("/connections/migrations/update", app.requireAuth(app.migrationUpdateHandler))
	mux.HandleFunc("/connections/migrations/explain", app.requireAuth(app.migrationExplainHandler))
	mux.HandleFunc("/connections/migrations/execute", app.requireAuth(app.migrationExecuteHandler))

	// Protected Platform Admin UI endpoints
	mux.HandleFunc("/users", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.usersHandler)))
	mux.HandleFunc("/users/create", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.userCreateHandler)))
	mux.HandleFunc("/users/toggle-active", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.userToggleActiveHandler)))
	mux.HandleFunc("/users/reset-password", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.userResetPasswordHandler)))
	mux.HandleFunc("/users/toggle-admin", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.userToggleAdminHandler)))

	mux.HandleFunc("/groups", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupsHandler)))
	mux.HandleFunc("/groups/create", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupCreateHandler)))
	mux.HandleFunc("/groups/update", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupUpdateHandler)))
	mux.HandleFunc("/groups/delete", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupDeleteHandler)))
	mux.HandleFunc("/groups/members/add", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupAddMemberHandler)))
	mux.HandleFunc("/groups/members/remove", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupRemoveMemberHandler)))
	mux.HandleFunc("/groups/leader", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupSetLeaderHandler)))

	mux.HandleFunc("/access", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.accessHandler)))
	mux.HandleFunc("/access/grant", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.accessGrantHandler)))
	mux.HandleFunc("/access/revoke", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.accessRevokeHandler)))

	mux.HandleFunc("/audit-logs", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.auditLogsHandler)))

	// Migration workflow JSON API
	mux.HandleFunc("GET /v2/databases/{db}/migrations", app.requireAuth(app.migrationDatabaseListHandler))
	mux.HandleFunc("POST /v2/databases/{db}/migrations", app.requireAuth(app.migrationDraftCreateHandler))
	mux.HandleFunc("GET /v2/migrations/{id}", app.requireAuth(app.migrationDetailHandler))
	mux.HandleFunc("PATCH /v2/migrations/{id}", app.requireAuth(app.migrationDraftUpdateHandler))
	mux.HandleFunc("POST /v2/migrations/{id}/submit", app.requireAuth(app.migrationReviewSubmitHandler))
	mux.HandleFunc("POST /v2/migrations/{id}/approve", app.requireAuth(app.migrationApprovalHandler))
	mux.HandleFunc("POST /v2/migrations/{id}/execute", app.requireAuth(app.migrationExecutionHandler))
	mux.HandleFunc("POST /v2/migrations/{id}/rollback", app.requireAuth(app.migrationRollbackRequestHandler))
	mux.HandleFunc("POST /v2/migrations/{id}/forward", app.requireAuth(app.migrationForwardRequestHandler))
	mux.HandleFunc("GET /v2/reviews", app.requireAuth(app.reviewListHandler))
	mux.HandleFunc("GET /v2/databases/{db}/history", app.requireAuth(app.migrationHistoryHandler))
	mux.HandleFunc("GET /v2/jobs/{id}", app.requireAuth(app.jobDetailHandler))
	mux.HandleFunc("POST /v2/jobs/{id}/resume", app.requireAuth(app.jobResumeHandler))
	mux.HandleFunc("POST /v2/jobs/{id}/cancel-batches", app.requireAuth(app.jobBatchCancelHandler))
	mux.HandleFunc("PUT /v2/groups/{id}/leader", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.groupLeaderAPIHandler)))

	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corrID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
		if corrID == "" {
			corrID = telemetry.NewCorrelationID()
		}
		w.Header().Set("X-Correlation-ID", corrID)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		ctx := telemetry.WithCorrelationID(r.Context(), corrID)
		if incomingTrace := strings.TrimSpace(r.Header.Get("traceparent")); incomingTrace != "" {
			ctx = telemetry.ExtractTraceContext(ctx, incomingTrace)
		}

		ctx, span := telemetry.Tracer("api").Start(ctx, r.Method+" "+r.URL.Path)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		mux.ServeHTTP(rec, r.WithContext(ctx))
		dur := time.Since(start)

		span.SetAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.target", auth.SanitizeURL(r.URL)),
			attribute.Int("http.status_code", rec.status),
		)

		if app.slogLogger != nil {
			app.slogLogger.InfoContext(ctx, "http.request",
				"method", r.Method,
				"path", auth.SanitizeURL(r.URL),
				"status", strconv.Itoa(rec.status),
				"duration_ms", float64(dur.Microseconds())/1000.0,
			)
		}
	})
	return auth.SecurityHeadersMiddleware(app.logger, api)
}

func (app *application) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawToken := app.sessions.ReadCookie(r)
		sess, rotatedToken, err := app.sessions.ValidateAndTouch(r.Context(), rawToken)
		if err != nil || sess == nil {
			app.sessions.ClearCookie(w)
			app.handleUnauthenticated(w, r)
			return
		}

		user, err := app.authSvc.GetUserByID(r.Context(), sess.UserID)
		if err != nil || user == nil || !user.IsActive {
			_ = app.sessions.Revoke(r.Context(), rawToken)
			app.sessions.ClearCookie(w)
			app.handleUnauthenticated(w, r)
			return
		}

		if rotatedToken != "" {
			app.sessions.SetCookie(w, rotatedToken)
		}

		if err := auth.ValidateRequestCSRF(r, sess); err != nil {
			ctx := auth.WithUserAndSession(r.Context(), user, sess)
			ctx = telemetry.WithActorID(ctx, int(user.ID))
			app.renderForbidden(w, r.WithContext(ctx), "Invalid or missing CSRF token.")
			return
		}

		ctx := auth.WithUserAndSession(r.Context(), user, sess)
		ctx = telemetry.WithActorID(ctx, int(user.ID))
		next(w, r.WithContext(ctx))
	}
}

func (app *application) requirePermission(action authz.Action, resourceExtractor func(*http.Request) (authz.Resource, error), next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			app.handleUnauthenticated(w, r)
			return
		}

		var res authz.Resource
		if resourceExtractor != nil {
			var err error
			res, err = resourceExtractor(r)
			if err != nil {
				http.Error(w, "invalid resource identifier", http.StatusBadRequest)
				return
			}
		}

		principal := authz.Principal{
			UserID:   user.ID,
			Username: user.Username,
			IsAdmin:  user.IsAdmin,
		}

		if err := app.authorizer.Can(r.Context(), principal, action, res); err != nil {
			if errors.Is(err, authz.ErrUnauthenticated) {
				app.handleUnauthenticated(w, r)
				return
			}
			app.renderForbidden(w, r, "Access denied: you do not have permission to perform this action.")
			return
		}

		next(w, r)
	}
}

func (app *application) checkDatabasePermission(w http.ResponseWriter, r *http.Request, id int64, source string, action authz.Action) bool {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		app.handleUnauthenticated(w, r)
		return false
	}
	principal := authz.Principal{
		UserID:   user.ID,
		Username: user.Username,
		IsAdmin:  user.IsAdmin,
	}
	if err := app.authorizer.Can(r.Context(), principal, action, authz.Resource{DatabaseID: id, Source: source}); err != nil {
		app.renderForbidden(w, r, "Access denied: you do not have permission to access this database.")
		return false
	}
	return true
}

func (app *application) handleUnauthenticated(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") || strings.HasPrefix(r.URL.Path, "/v2/") {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Silakan login kembali."})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *application) renderForbidden(w http.ResponseWriter, r *http.Request, message string) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "application/json") || strings.HasPrefix(r.URL.Path, "/v2/") {
		writeJSON(w, http.StatusForbidden, apiError{Error: message})
		return
	}
	w.WriteHeader(http.StatusForbidden)
	app.renderWithContext(w, r, "error", pageData{
		Title:        "403 Forbidden",
		ErrorMessage: message,
	})
}

func (app *application) renderWithContext(w http.ResponseWriter, r *http.Request, page string, data pageData) {
	if u, ok := auth.UserFromContext(r.Context()); ok {
		data.CurrentUser = u
		data.IsAdmin = u.IsAdmin
	}
	if s, ok := auth.SessionFromContext(r.Context()); ok {
		data.CSRFToken = auth.CSRFToken(s)
	}
	render(w, page, data)
}
