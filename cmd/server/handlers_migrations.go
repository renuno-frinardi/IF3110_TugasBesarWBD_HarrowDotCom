package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/explain"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func (app *application) migrationsListHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid connection id", http.StatusBadRequest)
		return
	}
	source := r.URL.Query().Get("source")
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionDBView) {
		return
	}

	app.renderMigrationsPage(w, r, id, source, "", "", nil, "", "")
}

func (app *application) renderMigrationsPage(w http.ResponseWriter, r *http.Request, id int64, source, draftTitle, draftSQL string, explainRes *explain.Result, errMsg, successMsg string) {
	db := app.stores.forType(source)
	connection, err := store.GetDatabaseConnection(r.Context(), db, id)
	if err != nil {
		http.Error(w, "database connection not found", http.StatusNotFound)
		return
	}
	if source == "" {
		source = connection.DBType
	}
	connection.Source = source

	user, _ := auth.UserFromContext(r.Context())
	principal := authz.Principal{UserID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}
	canEdit := app.authorizer.Can(r.Context(), principal, authz.ActionMigrationCreate, authz.Resource{DatabaseID: id, Source: source}) == nil

	var migrationsList []migration.Migration
	if app.migStore != nil {
		migrationsList, _ = app.migStore.GetByDatabase(r.Context(), int(id))
	} else {
		migrationsList, _ = app.queryMigrationsSQL(r.Context(), int(id))
	}

	app.renderWithContext(w, r, "migrations", pageData{
		Title:            "Migrations · " + connection.Name,
		Active:           "connections",
		Connection:       connection,
		Migrations:       migrationsList,
		CanEditMigration: canEdit,
		DraftTitle:       draftTitle,
		DraftSQL:         draftSQL,
		ExplainResult:    explainRes,
		Error:            errMsg,
		Success:          successMsg,
	})
}

func (app *application) queryMigrationsSQL(ctx context.Context, databaseID int) ([]migration.Migration, error) {
	rows, err := app.stores.postgres.DB.QueryContext(ctx, `
		SELECT id, database_id, version, title, sql, status,
		       created_by, executed_by, created_at, updated_at,
		       start_time, finish_time, error_message
		FROM migrations
		WHERE database_id = $1
		ORDER BY version, created_at, id
	`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []migration.Migration
	for rows.Next() {
		var m migration.Migration
		if err := rows.Scan(&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQL, &m.Status,
			&m.CreatedBy, &m.ExecutedBy, &m.CreatedAt, &m.UpdatedAt, &m.StartTime, &m.FinishTime, &m.ErrorMessage); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (app *application) migrationCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("database_id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid database_id", http.StatusBadRequest)
		return
	}
	source := r.FormValue("source")
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionMigrationCreate) {
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	if title == "" || sqlText == "" {
		app.renderMigrationsPage(w, r, id, source, title, sqlText, nil, "Migration title and SQL statement are required.", "")
		return
	}

	user, _ := auth.UserFromContext(r.Context())
	if app.migStore != nil {
		_, err = app.migStore.CreateDraft(r.Context(), int(id), title, sqlText, int(user.ID))
	} else {
		_, err = app.stores.postgres.DB.ExecContext(r.Context(), `
			INSERT INTO migrations (database_id, title, sql, status, created_by, created_at)
			VALUES ($1, $2, $3, 'draft', $4, NOW())
		`, id, title, sqlText, user.ID)
	}
	if err != nil {
		app.renderMigrationsPage(w, r, id, source, title, sqlText, nil, "Failed to save draft migration.", "")
		return
	}

	_ = app.adminStore.RecordAuditLog(r.Context(), &user.ID, "migration.create_draft", "migration", "", &id, map[string]any{
		"title": title,
	})
	http.Redirect(w, r, fmt.Sprintf("/connections/migrations?id=%d&source=%s", id, source), http.StatusSeeOther)
}

func (app *application) migrationUpdateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("database_id"), 10, 64)
	migID, migErr := strconv.Atoi(r.FormValue("migration_id"))
	if err != nil || id < 1 || migErr != nil || migID < 1 {
		http.Error(w, "invalid migration or database id", http.StatusBadRequest)
		return
	}
	source := r.FormValue("source")
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionMigrationCreate) {
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	if title == "" || sqlText == "" {
		app.renderMigrationsPage(w, r, id, source, "", "", nil, "Title and SQL are required.", "")
		return
	}

	if app.migStore != nil {
		_, err = app.migStore.Update(r.Context(), migID, title, sqlText)
	} else {
		res, execErr := app.stores.postgres.DB.ExecContext(r.Context(), `
			UPDATE migrations SET title = $1, sql = $2, updated_at = NOW()
			WHERE id = $3 AND database_id = $4 AND status = 'draft'
		`, title, sqlText, migID, id)
		err = execErr
		if err == nil {
			if aff, _ := res.RowsAffected(); aff == 0 {
				err = errors.New("draft not found or not editable")
			}
		}
	}
	if err != nil {
		app.renderMigrationsPage(w, r, id, source, "", "", nil, "Unable to update migration (only draft migrations can be edited).", "")
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/connections/migrations?id=%d&source=%s", id, source), http.StatusSeeOther)
}

func (app *application) migrationExplainHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("database_id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid database_id", http.StatusBadRequest)
		return
	}
	source := r.FormValue("source")
	// EXPLAIN / EXPLAIN ANALYZE is restricted to View+Edit
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionMigrationCreate) {
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	analyze := r.FormValue("analyze") == "true"

	if explain.IsPureDDL(sqlText) {
		res, _ := explain.ExplainPostgres(r.Context(), nil, sqlText, analyze)
		app.renderMigrationsPage(w, r, id, source, title, sqlText, res, "", "")
		return
	}

	targetPool, err := app.openTargetPostgresPool(r.Context(), id, source)
	if err != nil {
		app.renderMigrationsPage(w, r, id, source, title, sqlText, nil, "Unable to connect to target database for EXPLAIN.", "")
		return
	}
	defer targetPool.Close()

	res, err := explain.ExplainPostgres(r.Context(), targetPool, sqlText, analyze)
	if err != nil {
		app.renderMigrationsPage(w, r, id, source, title, sqlText, nil, "EXPLAIN failed on target database.", "")
		return
	}
	app.renderMigrationsPage(w, r, id, source, title, sqlText, res, "", "")
}

func (app *application) migrationExecuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("database_id"), 10, 64)
	migID, migErr := strconv.Atoi(r.FormValue("migration_id"))
	if err != nil || id < 1 || migErr != nil || migID < 1 {
		http.Error(w, "invalid migration or database id", http.StatusBadRequest)
		return
	}
	source := r.FormValue("source")
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionMigrationExecute) {
		return
	}

	targetPool, err := app.openTargetPostgresPool(r.Context(), id, source)
	if err != nil {
		app.renderMigrationsPage(w, r, id, source, "", "", nil, "Unable to connect to target database.", "")
		return
	}
	defer targetPool.Close()

	user, _ := auth.UserFromContext(r.Context())
	if err := explain.ExecuteMigrationTransactional(r.Context(), app.stores.postgres.DB, targetPool, migID, user.ID); err != nil {
		app.renderMigrationsPage(w, r, id, source, "", "", nil, "Migration failed and was rolled back.", "")
		return
	}

	_ = app.adminStore.RecordAuditLog(r.Context(), &user.ID, "migration.execute", "migration", fmt.Sprintf("%d", migID), &id, nil)
	app.renderMigrationsPage(w, r, id, source, "", "", nil, "", "Migration executed and committed successfully.")
}
