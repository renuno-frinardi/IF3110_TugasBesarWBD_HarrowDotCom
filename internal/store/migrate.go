package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.up.sql
var migrationsFS embed.FS

// RunMigrations applies all migration that has not been recorded yet
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("Migration pool is nil")
	}

	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("Create schema_migrations table: %w", err)
	}

	entries, err := fs.Glob(migrationsFS, "migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("List migrations: %w", err)
	}

	if len(entries) == 0 {
		return fmt.Errorf("No migration files found")
	}

	sort.Strings(entries)
	for _, filename := range entries {
		version, err := migrationVersion(filename)
		if err != nil {
			return err
		}

		sqlBytes, err := migrationsFS.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("Read migration %s: %w", filename, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("Begin migration %s: %w", filename, err)
		}

		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(817311, $1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("Lock migration %s: %w", filename, err)
		}

		var applied bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)",
			version,
		).Scan(&applied); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("Check migration %s: %w", filename, err)
		}
		if applied {
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("Commit skipped migration %s: %w", filename, err)
			}
			continue
		}

		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("Execute migration %s: %w", filename, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations (version) VALUES ($1)",
			version,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("Record migration %s: %w", filename, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("Commit migration %s: %w", filename, err)
		}
	}

	return nil
}

func migrationVersion(filename string) (int64, error) {
	base := path.Base(filename)
	separator := strings.IndexByte(base, '_')
	if separator <= 0 {
		return 0, fmt.Errorf("Invalid migration filename %q: expected XXXXXX_name.up.sql", filename)
	}

	version, err := strconv.ParseInt(base[:separator], 10, 64)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("Invalid migration version in %q", filename)
	}
	return version, nil
}