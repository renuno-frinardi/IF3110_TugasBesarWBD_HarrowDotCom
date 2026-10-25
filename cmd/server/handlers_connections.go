package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func (app *application) listAllowedConnections(ctx context.Context, userID int64) (pgConns, mariaConns []store.DatabaseConnection, err error) {
	allowedMap, err := app.dbAuthorizer.ListAccessibleDatabases(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	allPG, err := store.ListDatabaseConnections(ctx, app.stores.postgres)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range allPG {
		if _, ok := allowedMap[c.ID]; ok {
			c.Source = c.DBType
			if c.Source == "" {
				c.Source = "postgresql"
			}
			if c.DBType == "mariadb" || c.DBType == "mysql" {
				mariaConns = append(mariaConns, c)
			} else {
				pgConns = append(pgConns, c)
			}
		}
	}

	if app.stores.mariaDB != nil {
		if allMaria, mErr := store.ListDatabaseConnections(ctx, app.stores.mariaDB); mErr == nil {
			for _, c := range allMaria {
				if _, ok := allowedMap[c.ID]; ok {
					c.Source = "mariadb"
					if c.DBType == "mariadb" {
						mariaConns = append(mariaConns, c)
					}
				}
			}
		}
	}
	return pgConns, mariaConns, nil
}

func (app *application) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	user, _ := auth.UserFromContext(r.Context())
	postgresConnections, mariaConnections, err := app.listAllowedConnections(r.Context(), user.ID)
	if err != nil {
		app.renderWithContext(w, r, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: "Unable to load database connections."})
		return
	}
	connections := append(postgresConnections, mariaConnections...)

	stats := dashboardStats{Connections: len(connections)}
	stats.Users, _ = store.CountRecords(r.Context(), app.stores.postgres, "users")
	stats.Groups, _ = store.CountRecords(r.Context(), app.stores.postgres, "user_groups")
	stats.AuditLogs, _ = store.CountRecords(r.Context(), app.stores.postgres, "audit_logs")

	if len(connections) > 4 {
		connections = connections[:4]
	}

	app.renderWithContext(w, r, "dashboard", pageData{
		Title:       "Dashboard",
		Active:      "dashboard",
		Stats:       stats,
		Connections: connections,
	})
}

func (app *application) connectionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, _ := auth.UserFromContext(r.Context())
	postgresqlConnections, mariaDBConnections, err := app.listAllowedConnections(r.Context(), user.ID)
	if err != nil {
		app.renderWithContext(w, r, "connections", pageData{Title: "Connections", Active: "connections", Error: "Unable to load connections."})
		return
	}

	app.renderWithContext(w, r, "connections", pageData{
		Title:                 "Connections",
		Active:                "connections",
		PostgreSQLConnections: postgresqlConnections,
		MariaDBConnections:    mariaDBConnections,
	})
}

func (app *application) connectionDetailHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid connection id", http.StatusBadRequest)
		return
	}
	source := r.URL.Query().Get("source")
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionDBView) {
		return
	}

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
	app.renderWithContext(w, r, "connection-detail", pageData{Title: "Connection details", Active: "connections", Connection: connection})
}

func (app *application) exploreConnectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid connection id", http.StatusBadRequest)
		return
	}

	source := r.URL.Query().Get("source")
	if !app.checkDatabasePermission(w, r, id, source, authz.ActionDBView) {
		return
	}

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

	encryptedPassword, err := store.GetEncryptedPassword(r.Context(), db, id)
	if err != nil {
		app.renderExplorerError(w, r, connection, err)
		return
	}

	if connection.DBType == "mariadb" {
		app.exploreMariaDBConnection(w, r, connection, encryptedPassword)
		return
	}

	config, err := app.postgresTargetPoolConfig(connection, encryptedPassword)
	if err != nil {
		app.renderExplorerError(w, r, connection, err)
		return
	}

	target, err := pgxpool.NewWithConfig(r.Context(), config)
	if err != nil {
		app.renderExplorerError(w, r, connection, err)
		return
	}
	defer target.Close()

	if err := target.Ping(r.Context()); err != nil {
		app.renderExplorerError(w, r, connection, err)
		return
	}

	explorer := store.ExplorerData{Page: store.ParsePageNumber(r.URL.Query().Get("page"))}
	explorer.Schemas, err = store.ListPostgresSchemas(r.Context(), target)
	if err == nil && len(explorer.Schemas) > 0 {
		explorer.SelectedSchema = r.URL.Query().Get("schema")
		if explorer.SelectedSchema == "" {
			explorer.SelectedSchema = explorer.Schemas[0]
		}
		if !contains(explorer.Schemas, explorer.SelectedSchema) {
			http.Error(w, "invalid schema", http.StatusBadRequest)
			return
		}
		explorer.Tables, err = store.ListPostgresTables(r.Context(), target, explorer.SelectedSchema)
	}

	if err == nil {
		explorer.SelectedTable = r.URL.Query().Get("table")
		if explorer.SelectedTable != "" {
			if !contains(explorer.Tables, explorer.SelectedTable) {
				http.Error(w, "invalid table", http.StatusBadRequest)
				return
			}
			explorer.Columns, explorer.Rows, explorer.HasNextPage, err = store.ReadPostgresTable(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable, explorer.Page)
		}
	}

	if err != nil {
		app.logger.Printf("Read target database metadata failed for connection %d: %v", connection.ID, err)
		explorerError := pageData{
			Title:      "Database explorer",
			Active:     "connections",
			Connection: connection,
			Explorer:   explorer,
			Error:      "Unable to read the target database, please check the connection settings and permissions",
		}
		app.renderWithContext(w, r, "connection-explorer", explorerError)
		return
	}
	app.renderWithContext(w, r, "connection-explorer", pageData{Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer})
}

func (app *application) exploreMariaDBConnection(w http.ResponseWriter, r *http.Request, connection store.DatabaseConnection, encryptedPassword []byte) {
	target, err := store.OpenMariaDBTarget(r.Context(), connection, encryptedPassword, app.encryptionKeyOrEnv())
	if err != nil {
		app.renderExplorerError(w, r, connection, err)
		return
	}
	defer target.Close()

	explorer := store.ExplorerData{Page: store.ParsePageNumber(r.URL.Query().Get("page"))}
	explorer.Schemas, err = store.ListMariaDBSchemas(r.Context(), target)
	if err == nil && len(explorer.Schemas) > 0 {
		explorer.SelectedSchema = r.URL.Query().Get("schema")
		if explorer.SelectedSchema == "" {
			explorer.SelectedSchema = explorer.Schemas[0]
		}
		if !contains(explorer.Schemas, explorer.SelectedSchema) {
			http.Error(w, "invalid schema", http.StatusBadRequest)
			return
		}
		explorer.Tables, err = store.ListMariaDBTables(r.Context(), target, explorer.SelectedSchema)
	}

	if err == nil {
		explorer.SelectedTable = r.URL.Query().Get("table")
		if explorer.SelectedTable != "" {
			if !contains(explorer.Tables, explorer.SelectedTable) {
				http.Error(w, "invalid table", http.StatusBadRequest)
				return
			}
			explorer.Columns, explorer.Rows, explorer.HasNextPage, err = store.ReadMariaDBTable(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable, explorer.Page)
		}
	}

	if err != nil {
		app.logger.Printf("Read MariaDB target metadata failed for connection %d: %v", connection.ID, err)
		app.renderWithContext(w, r, "connection-explorer", pageData{
			Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer,
			Error: "Unable to read the target database, please check the connection settings and permissions",
		})
		return
	}

	app.renderWithContext(w, r, "connection-explorer", pageData{Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer})
}

func (app *application) renderExplorerError(w http.ResponseWriter, r *http.Request, connection store.DatabaseConnection, err error) {
	app.logger.Printf("Target database connection failed for connection %d: %v", connection.ID, err)
	app.renderWithContext(w, r, "connection-explorer", pageData{
		Title:      "Database explorer",
		Active:     "connections",
		Connection: connection,
		Error:      "Unable to connect to the target database, check the connection settings and try again",
	})
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (app *application) encryptionKeyOrEnv() string {
	if app.encryptionKey != "" {
		return app.encryptionKey
	}
	return os.Getenv("ENCRYPTION_KEY")
}

func (app *application) deleteConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid connection id", http.StatusBadRequest)
		return
	}
	db := app.stores.forType(r.FormValue("source"))
	if err := store.DeleteDatabaseConnection(r.Context(), db, id); err != nil {
		http.Error(w, "unable to delete database connection", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (app *application) editConnectionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid connection id", http.StatusBadRequest)
		return
	}

	source := r.URL.Query().Get("source")
	db := app.stores.forType(source)
	connection, err := store.GetDatabaseConnection(r.Context(), db, id)
	if err != nil {
		http.Error(w, "database connection not found", http.StatusNotFound)
		return
	}
	connection.Source = source

	data := pageData{Title: "Edit connection", Active: "connections", Connection: connection}
	if r.Method == http.MethodGet {
		app.renderWithContext(w, r, "edit-connection", data)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		data.Error = "Invalid form submission."
		app.renderWithContext(w, r, "edit-connection", data)
		return
	}

	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil {
		data.Error = "Port must be a number."
		app.renderWithContext(w, r, "edit-connection", data)
		return
	}

	input := store.CreateDatabaseConnectionInput{
		Name:         strings.TrimSpace(r.FormValue("name")),
		Host:         strings.TrimSpace(r.FormValue("host")),
		Port:         port,
		DatabaseName: strings.TrimSpace(r.FormValue("database_name")),
		Username:     strings.TrimSpace(r.FormValue("username")),
		DBType:       r.FormValue("db_type"),
		SSLMode:      r.FormValue("ssl_mode"),
	}

	if input.Name == "" || input.Host == "" {
		data.Error = "Connection name and host are required"
		app.renderWithContext(w, r, "edit-connection", data)
		return
	}

	if password := r.FormValue("password"); password != "" {
		input.Password, err = store.EncryptSecret(password, app.encryptionKeyOrEnv())
		if err != nil {
			data.Error = "Connection encryption is not configured correctly"
			app.renderWithContext(w, r, "edit-connection", data)
			return
		}
	}

	if err := store.UpdateDatabaseConnection(r.Context(), db, id, input); err != nil {
		data.Error = "Unable to update connection, check that the name is unique"
		app.renderWithContext(w, r, "edit-connection", data)
		return
	}

	http.Redirect(w, r, "/connections/view?id="+strconv.FormatInt(id, 10)+"&source="+input.DBType, http.StatusSeeOther)
}

func (app *application) newConnectionHandler(w http.ResponseWriter, r *http.Request) {
	data := pageData{Title: "Add connection", Active: "connections"}
	if r.Method == http.MethodGet {
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		data.Error = "Invalid form submission."
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	input := store.CreateDatabaseConnectionInput{
		Name:         strings.TrimSpace(r.FormValue("name")),
		Host:         strings.TrimSpace(r.FormValue("host")),
		DatabaseName: strings.TrimSpace(r.FormValue("database_name")),
		Username:     strings.TrimSpace(r.FormValue("username")),
		DBType:       r.FormValue("db_type"),
		SSLMode:      r.FormValue("ssl_mode"),
	}
	data.FormConnection = input
	data.FormPassword = r.FormValue("password")

	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil {
		data.Error = "Port must be a number."
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	input.Port = port
	data.FormConnection = input

	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" {
		data.Error = "Connection name and host are required"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	input.Password, err = store.EncryptSecret(r.FormValue("password"), app.encryptionKeyOrEnv())
	if err != nil {
		data.Error = "Connection encryption is not configured correctly"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	if err := store.CreateDatabaseConnection(r.Context(), app.stores.forType(input.DBType), input); err != nil {
		data.Error = "Unable to save connection, check that the name is unique"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (app *application) testConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := pageData{Title: "Add connection", Active: "connections"}
	if err := r.ParseForm(); err != nil {
		data.Error = "Invalid form submission"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	input := store.CreateDatabaseConnectionInput{
		Name:         strings.TrimSpace(r.FormValue("name")),
		Host:         strings.TrimSpace(r.FormValue("host")),
		DatabaseName: strings.TrimSpace(r.FormValue("database_name")),
		Username:     strings.TrimSpace(r.FormValue("username")),
		DBType:       r.FormValue("db_type"),
		SSLMode:      r.FormValue("ssl_mode"),
	}
	data.FormConnection = input
	data.FormPassword = r.FormValue("password")

	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil || port < 1 || port > 65535 {
		data.Error = "Port must be a number between 1 and 65535"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	input.Port = port
	input.Password = []byte(data.FormPassword)
	data.FormConnection = input

	if input.Host == "" || input.DatabaseName == "" || input.Username == "" || len(input.Password) == 0 {
		data.Error = "Host, database name, username, and password are required"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	if err := store.TestDBConnection(r.Context(), input); err != nil {
		app.logger.Printf("Test target database connection failed: %v", err)
		data.Error = "Connection test failed, check the host, port, database name, username, password, and SSL mode"
		app.renderWithContext(w, r, "add-connection", data)
		return
	}

	data.Success = "Connection test succeeded"
	app.renderWithContext(w, r, "add-connection", data)
}

func (app *application) postgresTargetPoolConfig(connection store.DatabaseConnection, encryptedPassword []byte) (*pgxpool.Config, error) {
	if strings.HasPrefix(connection.Host, "/") {
		password, err := store.DecryptSecret(encryptedPassword, app.encryptionKeyOrEnv())
		if err != nil {
			return nil, err
		}
		dsn := fmt.Sprintf(
			"postgres://%s:%s@/%s?host=%s&port=%d&sslmode=%s",
			url.QueryEscape(connection.Username),
			url.QueryEscape(password),
			url.PathEscape(connection.DatabaseName),
			url.QueryEscape(connection.Host),
			connection.Port,
			url.QueryEscape(connection.SSLMode),
		)
		return pgxpool.ParseConfig(dsn)
	}
	return store.PostgresTargetConfig(connection, encryptedPassword, app.encryptionKeyOrEnv())
}

func (app *application) openTargetPostgresPool(ctx context.Context, id int64, source string) (*pgxpool.Pool, error) {
	db := app.stores.forType(source)
	conn, err := store.GetDatabaseConnection(ctx, db, id)
	if err != nil {
		return nil, err
	}
	encPass, err := store.GetEncryptedPassword(ctx, db, id)
	if err != nil {
		return nil, err
	}
	cfg, err := app.postgresTargetPoolConfig(conn, encPass)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}
