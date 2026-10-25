package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

// menjalankan migration SQL pada db MariaDB
func (s *Service) ExecuteMariaDB(ctx context.Context, userID, migrationID int, target *sql.DB) (*Migration, error) {
	if target == nil {
		return nil, errors.New("The MariaDB connection is not ready")
	}

	m, err := s.store.GetByID(ctx, migrationID)
	if err != nil {
		return nil, err
	}

	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}

	if m.Status != S_Draft {
		return nil, NotDraft
	}

	metadata, err := pgx.ConnectConfig(ctx, s.store.pool.Config().ConnConfig)
	if err != nil {
		return nil, errors.New("Failed to open a connection to record the migration")
	}
	defer closeExecutionConnection(metadata)

	if _, err := metadata.Exec(ctx, "SELECT pg_advisory_lock($1, $2)", executionLock, m.DatabaseID); err != nil {
		return nil, fmt.Errorf("Failed to wait for the migration turn: %w", err)
	}

	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}

	if err := claimMigrationWithoutPostgresValidation(ctx, metadata, m, userID); err != nil {
		return nil, err
	}

	status, executionErr := runMariaDBMigrationSQL(ctx, target, m.SQL)
	return saveExecution(metadata, m, status, executionErr)
}

func claimMigrationWithoutPostgresValidation(ctx context.Context, metadata *pgx.Conn, m *Migration, userID int) error {
	tx, err := metadata.Begin(ctx)
	if err != nil {
		return fmt.Errorf("Failed to prepare the migration history: %w", err)
	}
	defer rollbackExecution(tx)

	var hasRunning bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM migrations WHERE database_id = $1 AND status = 'running')`, m.DatabaseID).Scan(&hasRunning); err != nil {
		return fmt.Errorf("Failed to check running migrations: %w", err)
	}

	if hasRunning {
		return ExecutionPending
	}

	err = tx.QueryRow(ctx, `UPDATE migrations
		SET status = 'running', version = (SELECT COALESCE(MAX(version), 0) + 1 FROM migrations WHERE database_id = $2),
		executed_by = $3, start_time = clock_timestamp(), updated_at = clock_timestamp(),
		finish_time = NULL, error_message = NULL
		WHERE id = $1 AND database_id = $2 AND status = 'draft'
		RETURNING id, database_id, version, title, sql, status, created_by, executed_by,
		created_at, updated_at, start_time, finish_time, error_message`,
		m.ID, m.DatabaseID, userID).Scan(
		&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQL, &m.Status,
		&m.CreatedBy, &m.ExecutedBy, &m.CreatedAt, &m.UpdatedAt,
		&m.StartTime, &m.FinishTime, &m.ErrorMessage)

	if errors.Is(err, pgx.ErrNoRows) {
		return NotDraft
	}

	if err != nil {
		return fmt.Errorf("Failed to start the migration: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("Failed to record the migration start: %w", err)
	}
	return nil
}

func runMariaDBMigrationSQL(ctx context.Context, target *sql.DB, sqlText string) (string, error) {
	statements, err := store.MariaDBStatements(sqlText)
	if err != nil {
		return S_Failed, err
	}

	if err := store.ValidateMariaDBStatements(statements); err != nil {
		return S_Failed, err
	}
	if err := requireTransactionalTables(ctx, target, statements); err != nil {
		return S_Failed, err
	}
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return S_Failed, fmt.Errorf("Failed to start the MariaDB target transaction: %w", err)
	}

	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return S_Running, fmt.Errorf("%w: MariaDB rollback could not be confirmed", ExecutionPending)
			}
			return S_Failed, fmt.Errorf("MariaDB SQL failed, changes were rolled back: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		_ = tx.Rollback()
		return S_Failed, fmt.Errorf("Migration execution was canceled: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return S_Running, fmt.Errorf("%w: MariaDB commit confirmation was not received, do not run the SQL again", ExecutionPending)
	}
	return S_Executed, nil
}

var mariaDBTableRef = regexp.MustCompile("(?i)\\b(?:INTO|UPDATE|FROM|JOIN)\\s+(?:`?([A-Za-z0-9_$]+)`?\\.)?`?([A-Za-z0-9_$]+)`?")

// Rollback hanya efektif pada engine transaksional; tabel MyISAM/Aria dkk tidak ikut dibatalkan.
func requireTransactionalTables(ctx context.Context, target *sql.DB, statements []string) error {
	checked := map[string]bool{}
	for _, statement := range statements {
		if strings.EqualFold(strings.Fields(statement)[0], "SELECT") {
			continue
		}
		for _, ref := range mariaDBTableRef.FindAllStringSubmatch(statement, -1) {
			key := ref[1] + "." + ref[2]
			if checked[key] {
				continue
			}
			checked[key] = true

			var engine sql.NullString
			err := target.QueryRowContext(ctx, `SELECT ENGINE FROM information_schema.TABLES
				WHERE TABLE_SCHEMA = COALESCE(NULLIF(?, ''), DATABASE()) AND TABLE_NAME = ?`, ref[1], ref[2]).Scan(&engine)
			if errors.Is(err, sql.ErrNoRows) {
				continue // tabel tidak ada: biarkan MariaDB melaporkan errornya
			}
			if err != nil {
				return fmt.Errorf("Failed to check the storage engine of table %s: %w", ref[2], err)
			}
			if engine.Valid && !strings.EqualFold(engine.String, "InnoDB") {
				return fmt.Errorf("%w: table %s uses the %s engine, which does not support rollback; only InnoDB tables are allowed", InvalidInput, ref[2], engine.String)
			}
		}
	}
	return nil
}

func RunMariaDBMigrationSQL(ctx context.Context, target *sql.DB, sqlText string) (string, error) {
	return runMariaDBMigrationSQL(ctx, target, sqlText)
}
