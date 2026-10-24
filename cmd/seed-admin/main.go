package main

// Import pkg
import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

// Fungsi utama untuk melakukan seeding ke dalam database
func main() {
	log.SetOutput(auth.NewRedactingWriter(os.Stderr))

	var username string
	flag.StringVar(&username, "username", "", "Platform Admin username (defaults to SEED_ADMIN_USERNAME or 'admin')")
	flag.Parse()

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to load .env: %v", err)
	}

	if username == "" {
		username = os.Getenv("SEED_ADMIN_USERNAME")
	}
	if username == "" {
		username = "admin"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	postgresURL := envOrDefault("POSTGRES_DATABASE_URL", store.PostgresAppURL(
		envOrDefault("POSTGRES_HOST", "localhost"),
		envIntOrDefault("POSTGRES_PORT", 5433),
		envOrDefault("POSTGRES_DB", "harrow_db"),
		envOrDefault("POSTGRES_USER", "harrow"),
		os.Getenv("POSTGRES_PASSWORD"),
	))

	pgDB, err := store.OpenDB(ctx, "postgresql", postgresURL)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer pgDB.Close()

	if err := store.RunMigrations(ctx, pgDB); err != nil {
		log.Fatalf("Failed to run PostgreSQL migrations: %v", err)
	}

	if err := runSeedAdmin(ctx, pgDB.DB, username, os.Stdin, os.Stdout); err != nil {
		log.Fatalf("seed-admin failed: %v", err)
	}
}

// Menjalankan seeder runtuk admin
func runSeedAdmin(ctx context.Context, db *sql.DB, username string, stdin io.Reader, stdout io.Writer) error {
	password, err := auth.ReadSeedPassword(stdin)
	if err != nil {
		return err
	}

	sessMgr := auth.NewSessionManager(auth.NewPostgresSessionStore(db), auth.DefaultSessionConfig())
	authSvc, err := auth.NewService(db, sessMgr)
	if err != nil {
		return err
	}

	user, err := authSvc.SeedAdmin(ctx, username, password)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "Seeded Platform Admin user %q (id=%d)\n", user.Username, user.ID)
	return nil
}

// Membaca string dari env jika ada dan fallback jiak tdk
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Membaca integer dari env jika ada dan fallback jiak tdk
func envIntOrDefault(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
