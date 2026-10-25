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
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/admin"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/explain"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
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
	pgPool   *pgxpool.Pool
}

func (s *appStores) forType(dbType string) *store.DB {
	if (dbType == "mariadb" || dbType == "mysql") && s.mariaDB != nil {
		return s.mariaDB
	}
	return s.postgres
}

type application struct {
	stores        *appStores
	authSvc       *auth.Service
	sessions      *auth.SessionManager
	authorizer    authz.Authorizer
	dbAuthorizer  *authz.DBAuthorizer
	adminStore    *admin.Store
	migStore      *migration.Store
	encryptionKey string
	logger        *log.Logger
}

func main() {
	redactingOut := auth.NewRedactingWriter(os.Stderr)
	log.SetOutput(redactingOut)
	logger := log.New(redactingOut, "", log.LstdFlags)

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
	defer postgresDB.Close()

	if err := store.RunMigrations(ctx, postgresDB); err != nil {
		log.Fatalf("Failed to run PostgreSQL migrations: %v", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "seed-admin" {
		username := os.Getenv("SEED_ADMIN_USERNAME")
		if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
			username = strings.TrimSpace(os.Args[2])
		}
		if username == "" {
			username = "admin"
		}
		password, err := auth.ReadSeedPassword(os.Stdin)
		if err != nil {
			log.Fatalf("seed-admin failed to read password: %v", err)
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
			log.Fatalf("seed-admin init failed: %v", err)
		}
		u, err := authSvc.SeedAdmin(ctx, username, password)
		if err != nil {
			log.Fatalf("seed-admin failed: %v", err)
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

	pgPool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		log.Fatalf("Failed to open PostgreSQL pool: %v", err)
	}
	defer pgPool.Close()

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
		mariaDB = mdb
		defer mariaDB.Close()
	} else {
		logger.Printf("MariaDB internal store unavailable, continuing with PostgreSQL platform store: %v", err)
	}

	stores := &appStores{postgres: postgresDB, mariaDB: mariaDB, pgPool: pgPool}
	sessMgr := auth.NewSessionManager(auth.NewPostgresSessionStore(postgresDB.DB), auth.DefaultSessionConfig())
	authSvc, err := auth.NewService(postgresDB.DB, sessMgr)
	if err != nil {
		log.Fatalf("Failed to initialize auth service: %v", err)
	}
	dbAuthz := authz.NewDBAuthorizer(postgresDB.DB)
	adminStore := admin.NewStore(postgresDB.DB, authSvc, sessMgr)
	migStore := migration.NewStore(pgPool)

	app := &application{
		stores:        stores,
		authSvc:       authSvc,
		sessions:      sessMgr,
		authorizer:    dbAuthz,
		dbAuthorizer:  dbAuthz,
		adminStore:    adminStore,
		migStore:      migStore,
		encryptionKey: os.Getenv("ENCRYPTION_KEY"),
		logger:        logger,
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger.Printf("Harrow is running at http://localhost:%s", port)

	if err := http.ListenAndServe(":"+port, app.routes()); err != nil {
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
