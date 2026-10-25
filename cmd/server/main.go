package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/web"
)

type pageData struct {
	Title                  string
	Active                 string
	Error                  string
	Success                string
	Stats                  dashboardStats
	Connections            []store.DatabaseConnection
	PostgreSQLConnections  []store.DatabaseConnection
	MariaDBConnections     []store.DatabaseConnection
	Connection             store.DatabaseConnection
	FormConnection         store.CreateDatabaseConnectionInput
	FormPassword           string
	Explorer               store.ExplorerData
	ConnectionSearch       string
	ConnectionTypeFilter   string
	ConnectionStatusFilter string
}

type dashboardStats struct {
	Connections int
	Users       int
	Groups      int
	AuditLogs   int
}

type appStores struct {
	postgres *store.DB
	mariaDB  *store.DB
}

func (s *appStores) forType(dbType string) *store.DB {
	if dbType == "mariadb" || dbType == "mysql" {
		return s.mariaDB
	}
	return s.postgres
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	postgresURL := envOrDefault("POSTGRES_DATABASE_URL", store.PostgresAppURL(
		envOrDefault("POSTGRES_HOST", "localhost"), envIntOrDefault("POSTGRES_PORT", 5433),
		envOrDefault("POSTGRES_DB", "harrow_db"), envOrDefault("POSTGRES_USER", "harrow"),
		os.Getenv("POSTGRES_PASSWORD"),
	))

	postgresDB, err := store.OpenDB(ctx, "postgresql", postgresURL)
	if err != nil {
		log.Fatalf("Failed to configure PostgreSQL database: %v", err)
	}

	mariaURL := envOrDefault("MARIADB_DATABASE_URL", store.MariaDBAppURL(
		envOrDefault("MARIADB_HOST", "localhost"), envIntOrDefault("MARIADB_PORT", 3307),
		envOrDefault("MARIADB_DATABASE", "harrow_db"), envOrDefault("MARIADB_USER", "harrow"),
		os.Getenv("MARIADB_PASSWORD"),
	))

	mariaDB, err := store.OpenDB(ctx, "mariadb", mariaURL)
	if err != nil {
		postgresDB.Close()
		log.Fatalf("Failed to configure MariaDB database: %v", err)
	}
	defer postgresDB.Close()
	defer mariaDB.Close()

	if err := store.RunMigrations(ctx, postgresDB); err != nil {
		log.Fatalf("Failed to run PostgreSQL migrations: %v", err)
	}

	if err := store.RunMigrations(ctx, mariaDB); err != nil {
		log.Fatalf("Failed to run MariaDB migrations: %v", err)
	}

	stores := &appStores{postgres: postgresDB, mariaDB: mariaDB}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS()))))
	mux.HandleFunc("/", dashboardHandler(stores))
	mux.HandleFunc("/connections", connectionsHandler(stores))
	mux.HandleFunc("/connections/view", connectionDetailHandler(stores))
	mux.HandleFunc("/connections/explore", exploreConnectionHandler(stores))
	mux.HandleFunc("/connections/delete", deleteConnectionHandler(stores))
	mux.HandleFunc("/connections/new", newConnectionHandler(stores))
	mux.HandleFunc("/connections/edit", editConnectionHandler(stores))
	mux.HandleFunc("/connections/test", testConnectionHandler)
	mux.HandleFunc("/users", placeholderHandler("Users", "users"))
	mux.HandleFunc("/groups", placeholderHandler("Groups", "groups"))
	mux.HandleFunc("/access", placeholderHandler("Access management", "placeholder"))
	mux.HandleFunc("/audit-logs", placeholderHandler("Audit logs", "placeholder"))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Harrow is running at http://localhost:%s", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func dashboardHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		postgresConnections, err := store.ListDatabaseConnections(r.Context(), stores.postgres)
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		mariaConnections, err := store.ListDatabaseConnections(r.Context(), stores.mariaDB)
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		for i := range postgresConnections {
			postgresConnections[i].Source = "postgresql"
		}

		for i := range mariaConnections {
			mariaConnections[i].Source = "mariadb"
		}

		postgresConnections = filterConnectionsByType(postgresConnections, "postgresql")
		mariaConnections = filterConnectionsByType(mariaConnections, "mariadb")
		connections := append(postgresConnections, mariaConnections...)

		stats := dashboardStats{Connections: len(connections)}
		stats.Users, err = store.CountRecords(r.Context(), stores.postgres, "users")
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		stats.Groups, err = store.CountRecords(r.Context(), stores.postgres, "user_groups")
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		stats.AuditLogs, err = store.CountRecords(r.Context(), stores.postgres, "audit_logs")
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		if len(connections) > 4 {
			connections = connections[:4]
		}

		render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Stats: stats, Connections: connections})
	}
}

func connectionsHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		postgresqlConnections, err := store.ListDatabaseConnections(r.Context(), stores.postgres)
		if err != nil {
			render(w, "connections", pageData{Title: "Connections", Active: "connections", Error: err.Error()})
			return
		}

		mariaDBConnections, err := store.ListDatabaseConnections(r.Context(), stores.mariaDB)
		if err != nil {
			render(w, "connections", pageData{Title: "Connections", Active: "connections", Error: err.Error()})
			return
		}

		for i := range postgresqlConnections {
			postgresqlConnections[i].Source = "postgresql"
		}

		for i := range mariaDBConnections {
			mariaDBConnections[i].Source = "mariadb"
		}

		postgresqlConnections = filterConnectionsByType(postgresqlConnections, "postgresql")
		mariaDBConnections = filterConnectionsByType(mariaDBConnections, "mariadb")

		search := strings.TrimSpace(r.URL.Query().Get("search"))
		typeFilter := r.URL.Query().Get("type")
		statusFilter := r.URL.Query().Get("status")
		postgresqlConnections = filterConnections(postgresqlConnections, search, typeFilter, statusFilter)
		mariaDBConnections = filterConnections(mariaDBConnections, search, typeFilter, statusFilter)

		render(w, "connections", pageData{
			Title:                  "Connections",
			Active:                 "connections",
			PostgreSQLConnections:  postgresqlConnections,
			MariaDBConnections:     mariaDBConnections,
			ConnectionSearch:       search,
			ConnectionTypeFilter:   typeFilter,
			ConnectionStatusFilter: statusFilter,
		})
	}
}

func filterConnectionsByType(connections []store.DatabaseConnection, dbType string) []store.DatabaseConnection {
	filtered := make([]store.DatabaseConnection, 0, len(connections))
	for _, connection := range connections {
		if connection.DBType == dbType {
			filtered = append(filtered, connection)
		}
	}
	return filtered
}

func filterConnections(connections []store.DatabaseConnection, search, typeFilter, statusFilter string) []store.DatabaseConnection {
	search = strings.ToLower(strings.TrimSpace(search))
	filtered := make([]store.DatabaseConnection, 0, len(connections))

	for _, connection := range connections {
		if typeFilter != "" && connection.DBType != typeFilter {
			continue
		}
		if statusFilter == "active" && !connection.IsActive {
			continue
		}
		if statusFilter == "inactive" && connection.IsActive {
			continue
		}
		if search != "" {
			matches := strings.Contains(strings.ToLower(connection.Name), search) ||
				strings.Contains(strings.ToLower(connection.Host), search) ||
				strings.Contains(strings.ToLower(connection.DatabaseName), search) ||
				strings.Contains(strings.ToLower(connection.Username), search)
			if !matches {
				continue
			}
		}
		filtered = append(filtered, connection)
	}

	return filtered
}

func connectionDetailHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		source := r.URL.Query().Get("source")
		db := stores.forType(source)
		connection, err := store.GetDatabaseConnection(r.Context(), db, id)
		if err != nil {
			http.Error(w, "database connection not found", http.StatusNotFound)
			return
		}
		connection.Source = source
		render(w, "connection-detail", pageData{Title: "Connection details", Active: "connections", Connection: connection})
	}
}

func exploreConnectionHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		source := r.URL.Query().Get("source")
		db := stores.forType(source)
		connection, err := store.GetDatabaseConnection(r.Context(), db, id)
		if err != nil {
			http.Error(w, "database connection not found", http.StatusNotFound)
			return
		}
		connection.Source = source

		encryptedPassword, err := store.GetEncryptedPassword(r.Context(), db, id)
		if err != nil {
			renderExplorerError(w, connection, err)
			return
		}

		if connection.DBType == "mariadb" {
			exploreMariaDBConnection(w, r, connection, encryptedPassword)
			return
		}

		config, err := store.PostgresTargetConfig(connection, encryptedPassword, os.Getenv("ENCRYPTION_KEY"))
		if err != nil {
			renderExplorerError(w, connection, err)
			return
		}

		target, err := pgxpool.NewWithConfig(r.Context(), config)
		if err != nil {
			renderExplorerError(w, connection, err)
			return
		}

		defer target.Close()
		if err := target.Ping(r.Context()); err != nil {
			renderExplorerError(w, connection, err)
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
				explorer.Constraints, err = store.ListPostgresConstraints(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable)
				if err == nil {
					explorer.Indexes, err = store.ListPostgresIndexes(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable)
				}

				if err == nil {
					explorer.Columns, explorer.Rows, explorer.HasNextPage, err = store.ReadPostgresTable(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable, explorer.Page)
				}
			}
		}

		if err != nil {
			log.Printf("Read target database metadata failed for connection %d: %v", connection.ID, err)
			explorerError := pageData{Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer, Error: "Unable to read the target database, please check the connection settings and permissions"}
			render(w, "connection-explorer", explorerError)
			return
		}
		render(w, "connection-explorer", pageData{Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer})
	}

}

func exploreMariaDBConnection(w http.ResponseWriter, r *http.Request, connection store.DatabaseConnection, encryptedPassword []byte) {
	target, err := store.OpenMariaDBTarget(r.Context(), connection, encryptedPassword, os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		renderExplorerError(w, connection, err)
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
			explorer.Constraints, err = store.ListMariaDBConstraints(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable)
			if err == nil {
				explorer.Indexes, err = store.ListMariaDBIndexes(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable)
			}

			if err == nil {
				explorer.Columns, explorer.Rows, explorer.HasNextPage, err = store.ReadMariaDBTable(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable, explorer.Page)
			}
		}
	}

	if err != nil {
		log.Printf("Read MariaDB target metadata failed for connection %d: %v", connection.ID, err)
		render(w, "connection-explorer", pageData{
			Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer,
			Error: "Unable to read the target database, please check the connection settings and permissions",
		})
		return
	}

	render(w, "connection-explorer", pageData{Title: "Database explorer", Active: "connections", Connection: connection, Explorer: explorer})
}

func renderExplorerError(w http.ResponseWriter, connection store.DatabaseConnection, err error) {
	log.Printf("Target database connection failed for connection %d: %v", connection.ID, err)
	render(w, "connection-explorer", pageData{
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

func deleteConnectionHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}
		db := stores.forType(r.FormValue("source"))
		if err := store.DeleteDatabaseConnection(r.Context(), db, id); err != nil {
			http.Error(w, "unable to delete database connection", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/connections", http.StatusSeeOther)
	}
}

func editConnectionHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		source := r.URL.Query().Get("source")
		db := stores.forType(source)
		connection, err := store.GetDatabaseConnection(r.Context(), db, id)
		if err != nil {
			http.Error(w, "database connection not found", http.StatusNotFound)
			return
		}
		connection.Source = source

		data := pageData{Title: "Edit connection", Active: "connections", Connection: connection}
		if r.Method == http.MethodGet {
			render(w, "edit-connection", data)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			data.Error = "Invalid form submission."
			render(w, "edit-connection", data)
			return
		}

		port, err := strconv.Atoi(r.FormValue("port"))
		if err != nil {
			data.Error = "Port must be a number."
			render(w, "edit-connection", data)
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
			render(w, "edit-connection", data)
			return
		}

		if password := r.FormValue("password"); password != "" {
			input.Password, err = store.EncryptSecret(password, os.Getenv("ENCRYPTION_KEY"))
			if err != nil {
				data.Error = "Connection encryption is not configured correctly"
				render(w, "edit-connection", data)
				return
			}
		}

		if err := store.UpdateDatabaseConnection(r.Context(), db, id, input); err != nil {
			data.Error = "Unable to update connection, check that the name is unique"
			render(w, "edit-connection", data)
			return
		}

		http.Redirect(w, r, "/connections/view?id="+strconv.FormatInt(id, 10)+"&source="+input.DBType, http.StatusSeeOther)
	}
}

func newConnectionHandler(stores *appStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := pageData{Title: "Add connection", Active: "connections"}
		if r.Method == http.MethodGet {
			render(w, "add-connection", data)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			data.Error = "Invalid form submission."
			render(w, "add-connection", data)
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
			render(w, "add-connection", data)
			return
		}

		input.Port = port
		data.FormConnection = input

		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" {
			data.Error = "Connection name and host are required"
			render(w, "add-connection", data)
			return
		}

		input.Password, err = store.EncryptSecret(r.FormValue("password"), os.Getenv("ENCRYPTION_KEY"))
		if err != nil {
			data.Error = "Connection encryption is not configured correctly"
			render(w, "add-connection", data)
			return
		}

		if err := store.CreateDatabaseConnection(r.Context(), stores.forType(input.DBType), input); err != nil {
			data.Error = "Unable to save connection, check that the name is unique"
			render(w, "add-connection", data)
			return
		}

		http.Redirect(w, r, "/connections", http.StatusSeeOther)
	}
}

func testConnectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data := pageData{Title: "Add connection", Active: "connections"}
	if err := r.ParseForm(); err != nil {
		data.Error = "Invalid form submission"
		render(w, "add-connection", data)
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
		render(w, "add-connection", data)
		return
	}

	input.Port = port
	input.Password = []byte(data.FormPassword)
	data.FormConnection = input

	if input.Host == "" || input.DatabaseName == "" || input.Username == "" || len(input.Password) == 0 {
		data.Error = "Host, database name, username, and password are required"
		render(w, "add-connection", data)
		return
	}

	if err := store.TestDBConnection(r.Context(), input); err != nil {
		log.Printf("Test target database connection failed: %v", err)
		data.Error = "Connection test failed, check the host, port, database name, username, password, and SSL mode"
		render(w, "add-connection", data)
		return
	}

	data.Success = "Connection test succeeded"
	render(w, "add-connection", data)
}

func placeholderHandler(title, active string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, "placeholder", pageData{Title: title, Active: active})
	}
}

func render(w http.ResponseWriter, page string, data pageData) {
	tmpl, err := web.ParsePage(page)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
		return
	}

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("Render %s: %v", page, err)
	}
}
