package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to load .env: %v", err)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set, create .env in the project root or set DATABASE_URL in the environment")
	}

	connectionCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(connectionCtx, databaseURL)
	if err != nil {
		log.Fatalf("Failed to configure PostgreSQL connection pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(connectionCtx); err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}

	if err := store.RunMigrations(connectionCtx, pool); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	fmt.Println("Database connection successful")
}