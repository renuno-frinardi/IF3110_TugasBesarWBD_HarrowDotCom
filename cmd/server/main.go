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
	Title       string
	Active      string
	Error       string
	Success     string
	Stats       dashboardStats
	Connections []store.DatabaseConnection
	Connection  store.DatabaseConnection
	Explorer    store.ExplorerData
}

type dashboardStats struct {
	Connections int
	Users       int
	Groups      int
	AuditLogs   int
}

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to load .env: %v", err)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed to configure PostgreSQL connection pool: %v", err)
	}

	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}

	if err := store.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS()))))
	mux.HandleFunc("/", dashboardHandler(pool))
	mux.HandleFunc("/connections", connectionsHandler(pool))
	mux.HandleFunc("/connections/view", connectionDetailHandler(pool))
	mux.HandleFunc("/connections/explore", exploreConnectionHandler(pool))
	mux.HandleFunc("/connections/delete", deleteConnectionHandler(pool))
	mux.HandleFunc("/connections/new", newConnectionHandler(pool))
	mux.HandleFunc("/connections/edit", editConnectionHandler(pool))
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

func dashboardHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		connections, err := store.ListDatabaseConnections(r.Context(), pool)
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		stats := dashboardStats{Connections: len(connections)}
		stats.Users, err = store.CountRecords(r.Context(), pool, "users")
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		stats.Groups, err = store.CountRecords(r.Context(), pool, "user_groups")
		if err != nil {
			render(w, "dashboard", pageData{Title: "Dashboard", Active: "dashboard", Error: err.Error()})
			return
		}

		stats.AuditLogs, err = store.CountRecords(r.Context(), pool, "audit_logs")
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

func connectionsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		connections, err := store.ListDatabaseConnections(r.Context(), pool)
		if err != nil {
			render(w, "connections", pageData{Title: "Connections", Active: "connections", Error: err.Error()})
			return
		}

		render(w, "connections", pageData{Title: "Connections", Active: "connections", Connections: connections})
	}
}

func connectionDetailHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		connection, err := store.GetDatabaseConnection(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "database connection not found", http.StatusNotFound)
			return
		}
		render(w, "connection-detail", pageData{Title: "Connection details", Active: "connections", Connection: connection})
	}
}

func exploreConnectionHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		connection, err := store.GetDatabaseConnection(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "database connection not found", http.StatusNotFound)
			return
		}

		if connection.DBType != "postgresql" {
			render(w, "connection-explorer", pageData{Title: "Database explorer", Active: "connections", Connection: connection, Error: "Database explorer currently supports PostgreSQL connections only"})
			return
		}

		encryptedPassword, err := store.GetEncryptedPassword(r.Context(), pool, id)
		if err != nil {
			renderExplorerError(w, connection, err)
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
				explorer.Columns, explorer.Rows, explorer.HasNextPage, err = store.ReadPostgresTable(r.Context(), target, explorer.SelectedSchema, explorer.SelectedTable, explorer.Page)
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

func renderExplorerError(w http.ResponseWriter, connection store.DatabaseConnection, err error) {
	log.Printf("Target database connection failed for connection %d: %v", connection.ID, err)
	render(w, "connection-explorer", pageData{
		Title: "Database explorer",
		Active: "connections",
		Connection: connection,
		Error: "Unable to connect to the target database, check the connection settings and try again",
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

func deleteConnectionHandler(pool *pgxpool.Pool) http.HandlerFunc {
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
		if err := store.DeleteDatabaseConnection(r.Context(), pool, id); err != nil {
			http.Error(w, "unable to delete database connection", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/connections", http.StatusSeeOther)
	}
}

func editConnectionHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid connection id", http.StatusBadRequest)
			return
		}

		connection, err := store.GetDatabaseConnection(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "database connection not found", http.StatusNotFound)
			return
		}

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
			Name: strings.TrimSpace(r.FormValue("name")),
			Host: strings.TrimSpace(r.FormValue("host")),
			Port: port,
			DatabaseName: strings.TrimSpace(r.FormValue("database_name")),
			Username: strings.TrimSpace(r.FormValue("username")),
			DBType: r.FormValue("db_type"),
			SSLMode: r.FormValue("ssl_mode"),
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

		if err := store.UpdateDatabaseConnection(r.Context(), pool, id, input); err != nil {
			data.Error = "Unable to update connection, check that the name is unique"
			render(w, "edit-connection", data)
			return
		}

		http.Redirect(w, r, "/connections/view?id="+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func newConnectionHandler(pool *pgxpool.Pool) http.HandlerFunc {
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

		port, err := strconv.Atoi(r.FormValue("port"))
		if err != nil {
			data.Error = "Port must be a number."
			render(w, "add-connection", data)
			return
		}

		input := store.CreateDatabaseConnectionInput{
			Name: r.FormValue("name"), Host: r.FormValue("host"), Port: port,
			DatabaseName: r.FormValue("database_name"), Username: r.FormValue("username"),
			DBType: r.FormValue("db_type"), SSLMode: r.FormValue("ssl_mode"),
		}

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

		if err := store.CreateDatabaseConnection(r.Context(), pool, input); err != nil {
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

	port, err := strconv.Atoi(r.FormValue("port"))
	if err != nil || port < 1 || port > 65535 {
		data.Error = "Port must be a number between 1 and 65535"
		render(w, "add-connection", data)
		return
	}

	input := store.CreateDatabaseConnectionInput{
		Name: strings.TrimSpace(r.FormValue("name")),
		Host: strings.TrimSpace(r.FormValue("host")),
		Port: port,
		DatabaseName: strings.TrimSpace(r.FormValue("database_name")),
		Username: strings.TrimSpace(r.FormValue("username")),
		Password: []byte(r.FormValue("password")),
		DBType: r.FormValue("db_type"),
		SSLMode: r.FormValue("ssl_mode"),
	}

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