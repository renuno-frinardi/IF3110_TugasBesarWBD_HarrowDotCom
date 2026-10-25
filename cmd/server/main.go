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
	ErrorMessage           string
	Success                string
	CurrentUser            *auth.User
	IsAdmin                bool
	CSRFToken              string
	Stats                  dashboardStats
	Connections            []store.DatabaseConnection
	EditableConnections    []store.DatabaseConnection
	PostgreSQLConnections  []store.DatabaseConnection
	MariaDBConnections     []store.DatabaseConnection
	PostgreSQLTable        connectionTableData
	MariaDBTable           connectionTableData
	Connection             store.DatabaseConnection
	FormConnection         store.CreateDatabaseConnectionInput
	FormPassword           string
	Explorer               store.ExplorerData
	ConnectionSearch       string
	ConnectionTypeFilter   string
	ConnectionStatusFilter string
	Users                  []admin.UserRecord
	Groups                 []admin.GroupRecord
	Grants                 []admin.DatabaseGrantRecord
	AuditLogs              []admin.AuditLogRecord
	Migrations             []migration.Migration
	CanEditMigration       bool
	DraftTitle             string
	DraftSQL               string
	ExplainResult          *explain.Result
}

type dashboardStats struct {
	Connections int
	Users       int
	Groups      int
	AuditLogs   int
}

type connectionTableData struct {
	Connections []store.DatabaseConnection
	CSRFToken   string
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
	migService    *migration.Service
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
		sessMgr := auth.NewSessionManager(auth.NewPostgresSessionStore(postgresDB.DB), auth.DefaultSessionConfig())
		authSvc, err := auth.NewService(postgresDB.DB, sessMgr)
		if err != nil {
			log.Fatalf("seed-admin init failed: %v", err)
		}
		u, err := authSvc.SeedAdmin(ctx, username, password)
		if err != nil {
			log.Fatalf("seed-admin failed: %v", err)
		}
		fmt.Fprintf(os.Stdout, "Seeded Platform Admin %q (id=%d)\n", u.Username, u.ID)
		return
	}

	pgPool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		log.Fatalf("Failed to open PostgreSQL pool: %v", err)
	}
	defer pgPool.Close()

	var mariaDB *store.DB
	mariaURL := envOrDefault("MARIADB_DATABASE_URL", store.MariaDBAppURL(
		envOrDefault("MARIADB_HOST", "localhost"), envIntOrDefault("MARIADB_PORT", 3307),
		envOrDefault("MARIADB_DATABASE", "harrow_db"), envOrDefault("MARIADB_USER", "harrow"),
		os.Getenv("MARIADB_PASSWORD"),
	))
	if mdb, err := store.OpenDB(ctx, "mariadb", mariaURL); err == nil {
		if migErr := store.RunMigrations(ctx, mdb); migErr != nil {
			mdb.Close()
			log.Fatalf("Failed to run MariaDB migrations: %v", migErr)
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
		migService:    migration.NewService(migStore, migration.NewDatabaseAccess(pgPool)),
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
