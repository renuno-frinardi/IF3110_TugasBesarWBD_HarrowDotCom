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
)

//go:embed migrations/*.up.sql
var migrationsFS embed.FS

//go:embed migrations-mariadb/*.up.sql
var mariadbMigrationsFS embed.FS

// RunMigrations applies all migration that has not been recorded yet
func RunMigrations(ctx context.Context, pool *DB) error {
	if pool == nil {
		return fmt.Errorf("Migration pool is nil")
	}

	migrationTableType := "TIMESTAMP WITH TIME ZONE"
	if pool.Driver == "mariadb" {
		migrationTableType = "TIMESTAMP"
	}

	if _, err := pool.DB.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			applied_at %s NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`, migrationTableType)); err != nil {
		return fmt.Errorf("Create schema_migrations table: %w", err)
	}

	filesystem := migrationsFS
	pattern := "migrations/*.up.sql"
	if pool.Driver == "mariadb" {
		filesystem = mariadbMigrationsFS
		pattern = "migrations-mariadb/*.up.sql"
	}

	entries, err := fs.Glob(filesystem, pattern)
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

		sqlBytes, err := filesystem.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("Read migration %s: %w", filename, err)
		}

		tx, err := pool.DB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("Begin migration %s: %w", filename, err)
		}

		var applied bool
		checkQuery := pool.Rebind("SELECT COUNT(*) > 0 FROM schema_migrations WHERE version = $1")
		if err := tx.QueryRowContext(ctx, checkQuery, version).Scan(&applied); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("Check migration %s: %w", filename, err)
		}

		if applied {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("Commit skipped migration %s: %w", filename, err)
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("Execute migration %s: %w", filename, err)
		}

		if _, err := tx.ExecContext(ctx,
			pool.Rebind("INSERT INTO schema_migrations (version) VALUES ($1)"),
			version,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("Record migration %s: %w", filename, err)
		}

		if err := tx.Commit(); err != nil {
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