package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/web"
)

func (app *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS()))))

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

	mux.HandleFunc("/access", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.accessHandler)))
	mux.HandleFunc("/access/grant", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.accessGrantHandler)))
	mux.HandleFunc("/access/revoke", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.accessRevokeHandler)))

	mux.HandleFunc("/audit-logs", app.requireAuth(app.requirePermission(authz.ActionPlatformAdmin, nil, app.auditLogsHandler)))

	return auth.SecurityHeadersMiddleware(app.logger, mux)
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
			app.renderForbidden(w, r.WithContext(ctx), "Invalid or missing CSRF token.")
			return
		}

		ctx := auth.WithUserAndSession(r.Context(), user, sess)
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
	if user.IsAdmin {
		return true
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
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") || strings.HasPrefix(r.URL.Path, "/api/") {
		w.WriteHeader(http.StatusUnauthorized)
		app.renderWithContext(w, r, "error", pageData{
			Title:        "401 Unauthorized",
			Active:       "login",
			ErrorMessage: "Authentication is required to access this resource.",
		})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *application) renderForbidden(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		message = "Access denied: you do not have permission to perform this action."
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
