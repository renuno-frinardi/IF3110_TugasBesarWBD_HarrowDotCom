package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/async"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/telemetry"
)

func main() {
	_ = godotenv.Load()

	logger := telemetry.NewJSONLogger(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	postgresURL := store.PostgresAppURL(
		envOrDefault("POSTGRES_HOST", "localhost"),
		envIntOrDefault("POSTGRES_PORT", 5433),
		envOrDefault("POSTGRES_DB", "harrow_db"),
		envOrDefault("POSTGRES_USER", "harrow"),
		os.Getenv("POSTGRES_PASSWORD"),
	)

	initCtx, cancelInit := context.WithTimeout(rootCtx, 30*time.Second)
	postgresDB, err := store.OpenDB(initCtx, "postgresql", postgresURL)
	if err != nil {
		cancelInit()
		logger.Error("worker.db.open_failed", "error", err, "status", "fatal")
		os.Exit(1)
	}
	if err := store.RunMigrations(initCtx, postgresDB); err != nil {
		_ = postgresDB.Close()
		cancelInit()
		logger.Error("worker.db.migrate_failed", "error", err, "status", "fatal")
		os.Exit(1)
	}
	_ = postgresDB.Close()

	pgPool, err := pgxpool.New(initCtx, postgresURL)
	cancelInit()
	if err != nil {
		logger.Error("worker.pool.open_failed", "error", err, "status", "fatal")
		os.Exit(1)
	}
	defer pgPool.Close()

	maxRetries := envIntOrDefault("WORKER_MAX_RETRIES", 3)
	retryBaseMs := envIntOrDefault("WORKER_RETRY_BASE_MS", 500)
	leaseTTL := time.Duration(envIntOrDefault("WORKER_LEASE_TTL_SECONDS", 15)) * time.Second
	pollInterval := time.Duration(envIntOrDefault("WORKER_POLL_INTERVAL_MS", 500)) * time.Millisecond
	concurrency := envIntOrDefault("WORKER_CONCURRENCY", 4)

	queue := async.NewQueue(pgPool, logger, maxRetries)
	locks := async.NewLockManager(pgPool, logger, leaseTTL)
	migStore := migration.NewStore(pgPool)
	migService := migration.NewServiceWithPublisher(migStore, migration.NewDatabaseAccess(pgPool), queue)
	migService.SetLogger(logger)

	worker := async.NewWorker(pgPool, queue, locks, migStore, migService, logger, async.WorkerConfig{
		WorkerID:      os.Getenv("WORKER_ID"),
		Concurrency:   concurrency,
		PollInterval:  pollInterval,
		LeaseTTL:      leaseTTL,
		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),
		RetryPolicy: async.RetryPolicy{
			MaxAttempts: maxRetries,
			BaseDelay:   time.Duration(retryBaseMs) * time.Millisecond,
			MaxDelay:    10 * time.Second,
		},
	})

	if err := worker.Run(rootCtx); err != nil {
		logger.Error("worker.exited_with_error", "error", err, "status", "error")
		os.Exit(1)
	}
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
