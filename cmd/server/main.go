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
	mux.HandleFunc("/connections/new", newConnectionHandler(pool))
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

func newConnectionHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data := pageData{Title: "Add connection", Active: "connections"}
		if r.Method == http.MethodGet {
			render(w, "connection_new", data)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			data.Error = "Invalid form submission."
			render(w, "connection_new", data)
			return
		}

		port, err := strconv.Atoi(r.FormValue("port"))
		if err != nil {
			data.Error = "Port must be a number."
			render(w, "connection_new", data)
			return
		}

		input := store.CreateDatabaseConnectionInput{
			Name: r.FormValue("name"), Host: r.FormValue("host"), Port: port,
			DatabaseName: r.FormValue("database_name"), Username: r.FormValue("username"),
			DBType: r.FormValue("db_type"), SSLMode: r.FormValue("ssl_mode"),
		}

		if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Host) == "" {
			data.Error = "Connection name and host are required"
			render(w, "connection_new", data)
			return
		}

		input.Password, err = store.EncryptSecret(r.FormValue("password"), os.Getenv("ENCRYPTION_KEY"))
		if err != nil {
			data.Error = "Connection encryption is not configured correctly"
			render(w, "connection_new", data)
			return
		}

		if err := store.CreateDatabaseConnection(r.Context(), pool, input); err != nil {
			data.Error = "Unable to save connection, check that the name is unique"
			render(w, "connection_new", data)
			return
		}

		http.Redirect(w, r, "/connections", http.StatusSeeOther)
	}
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