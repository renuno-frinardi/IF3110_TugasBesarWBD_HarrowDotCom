package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func main() {
	log.SetOutput(auth.NewRedactingWriter(os.Stderr))

	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to load .env: %v", err)
	}

	var (
		connName   string
		targetHost string
		targetPort int
		sslMode    string
		verifyConn bool
	)
	flag.StringVar(&connName, "name", "Commerce LoadTest DB", "Display name in HarrowDotCom connections")
	flag.StringVar(&targetHost, "target-host", envOrDefault("COMMERCE_DB_HOST", "localhost"), "Target host saved in database_connections")
	flag.IntVar(&targetPort, "target-port", envIntOrDefault("COMMERCE_DB_PORT", 5434), "Target port saved in database_connections")
	flag.StringVar(&sslMode, "ssl-mode", "disable", "PostgreSQL SSL mode")
	flag.BoolVar(&verifyConn, "verify", true, "Verify connectivity to the target database before registering")
	flag.Parse()

	encKey := os.Getenv("ENCRYPTION_KEY")
	if encKey == "" {
		log.Fatal("ENCRYPTION_KEY must be set in .env to encrypt the target database password")
	}

	dbName := envOrDefault("COMMERCE_DB_NAME", "harrow_commerce")
	dbUser := envOrDefault("COMMERCE_DB_USER", "commerce_admin")
	dbPass := envOrDefault("COMMERCE_DB_PASSWORD", "commerce_secret_123")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if verifyConn {
		testInput := store.CreateDatabaseConnectionInput{
			Name:         connName,
			Host:         targetHost,
			Port:         targetPort,
			DatabaseName: dbName,
			Username:     dbUser,
			Password:     []byte(dbPass),
			DBType:       "postgresql",
			SSLMode:      sslMode,
		}
		if err := store.TestDBConnection(ctx, testInput); err != nil {
			log.Fatalf("target database connectivity check (%s:%d/%s) failed: %v", targetHost, targetPort, dbName, err)
		}
	}

	ciphertext, err := store.EncryptSecret(dbPass, encKey)
	if err != nil {
		log.Fatalf("failed to encrypt target database password: %v", err)
	}

	postgresURL := store.PostgresAppURL(
		envOrDefault("POSTGRES_HOST", "localhost"),
		envIntOrDefault("POSTGRES_PORT", 5433),
		envOrDefault("POSTGRES_DB", "harrow_db"),
		envOrDefault("POSTGRES_USER", "harrow"),
		os.Getenv("POSTGRES_PASSWORD"),
	)

	pgDB, err := store.OpenDB(ctx, "postgresql", postgresURL)
	if err != nil {
		log.Fatalf("failed to connect to platform PostgreSQL: %v", err)
	}
	defer pgDB.Close()

	if err := store.RunMigrations(ctx, pgDB); err != nil {
		log.Fatalf("failed to run platform migrations: %v", err)
	}

	var connID int64
	err = pgDB.DB.QueryRowContext(ctx, `
		INSERT INTO database_connections (
			name, host, port, database_name, username, password_ciphertext, db_type, ssl_mode, is_active
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'postgresql', $7, TRUE)
		ON CONFLICT (name) DO UPDATE SET
			host = EXCLUDED.host,
			port = EXCLUDED.port,
			database_name = EXCLUDED.database_name,
			username = EXCLUDED.username,
			password_ciphertext = EXCLUDED.password_ciphertext,
			db_type = EXCLUDED.db_type,
			ssl_mode = EXCLUDED.ssl_mode,
			is_active = TRUE,
			updated_at = NOW()
		RETURNING id
	`, connName, targetHost, targetPort, dbName, dbUser, ciphertext, sslMode).Scan(&connID)
	if err != nil {
		log.Fatalf("failed to upsert target database connection: %v", err)
	}

	res, err := pgDB.DB.ExecContext(ctx, `
		INSERT INTO group_database_access (group_id, database_connection_id, access_level)
		SELECT id, $1, 'view_edit' FROM user_groups
		ON CONFLICT (group_id, database_connection_id) DO UPDATE SET
			access_level = 'view_edit',
			updated_at = NOW()
	`, connID)
	if err != nil {
		log.Fatalf("failed to grant group access to target database connection: %v", err)
	}
	grantedGroups, _ := res.RowsAffected()

	fmt.Fprintf(
		os.Stdout,
		"Registered target connection %q (id=%d, target=%s:%d/%s, groups_granted=%d)\n",
		connName, connID, targetHost, targetPort, dbName, grantedGroups,
	)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
