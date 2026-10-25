package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var NotDraft = errors.New("Only draft migrations can be executed")
var ExecutionPending = errors.New("The previous migration result needs to be checked")

const executionLock = 123456

func (s *Service) Execute(ctx context.Context, userID, migrationID int, hexKey string) (*Migration, error) {
	if s.store == nil || s.store.pool == nil {
		return nil, errors.New("The application database connection is not ready")
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

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	metadata, err := pgx.ConnectConfig(connectCtx, s.store.pool.Config().ConnConfig)
	cancelConnect()
	if err != nil {
		return nil, errors.New("Failed to open a connection to record the migration")
	}
	defer closeExecutionConnection(metadata)

	_, err = metadata.Exec(ctx, "SELECT pg_advisory_lock($1, $2)", executionLock, m.DatabaseID)
	if err != nil {
		return nil, fmt.Errorf("Failed to wait for the migration turn: %w", err)
	}

	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}

	if err := claimMigration(ctx, metadata, m, userID); err != nil {
		return nil, err
	}

	target, err := s.MakeConnection(ctx, userID, m.DatabaseID, hexKey)
	if err != nil {
		return saveExecution(metadata, m, S_Failed, err)
	}
	defer closeExecutionConnection(target)

	status, executionErr := runMigrationSQL(ctx, target, m.SQL)

	return saveExecution(metadata, m, status, executionErr)
}

func claimMigration(ctx context.Context, metadata *pgx.Conn, m *Migration, userID int) error {
	tx, err := metadata.Begin(ctx)
	if err != nil {
		return fmt.Errorf("Failed to prepare the migration history: %w", err)
	}
	defer rollbackExecution(tx)

	var hasRunning bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM migrations WHERE database_id = $1 AND status = 'running'
	)`, m.DatabaseID).Scan(&hasRunning)
	if err != nil {
		return fmt.Errorf("Failed to check running migrations: %w", err)
	}
	if hasRunning {
		return ExecutionPending
	}

	query := `UPDATE migrations
		SET status = 'running',
			version = (SELECT COALESCE(MAX(version), 0) + 1 FROM migrations WHERE database_id = $2),
			executed_by = $3, start_time = clock_timestamp(), updated_at = clock_timestamp(),
			finish_time = NULL, error_message = NULL
		WHERE id = $1 AND database_id = $2 AND status = 'draft'
		RETURNING id, database_id, version, title, sql, status, created_by, executed_by,
			created_at, updated_at, start_time, finish_time, error_message`
	err = tx.QueryRow(ctx, query, m.ID, m.DatabaseID, userID).Scan(
		&m.ID, &m.DatabaseID, &m.Version, &m.Title, &m.SQL, &m.Status,
		&m.CreatedBy, &m.ExecutedBy, &m.CreatedAt, &m.UpdatedAt,
		&m.StartTime, &m.FinishTime, &m.ErrorMessage,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotDraft
	}
	if err != nil {
		return fmt.Errorf("Failed to start the migration: %w", err)
	}

	if err := validateSQL(m.SQL); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("Failed to record the migration start; check the history before trying again: %w", err)
	}
	return nil
}

func runMigrationSQL(ctx context.Context, target *pgx.Conn, sql string) (string, error) {
	tx, err := target.Begin(ctx)
	if err != nil {
		return S_Failed, fmt.Errorf("Failed to start the target transaction: %w", err)
	}
	defer rollbackExecution(tx)

	if _, err := tx.Exec(ctx, sql); err != nil {
		return S_Failed, fmt.Errorf("Failed to execute SQL: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return S_Failed, fmt.Errorf("Migration execution was canceled: %w", err)
	}

	err = tx.Commit(ctx)
	if err == nil {
		return S_Executed, nil
	}

	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return S_Failed, fmt.Errorf("The transaction was rolled back by the database: %w", err)
	}
	var databaseErr *pgconn.PgError
	if errors.As(err, &databaseErr) && databaseErr.Severity == "ERROR" {
		if !target.IsClosed() && target.PgConn().TxStatus() == 'I' {
			return S_Failed, fmt.Errorf("The commit was rejected by the database: %w", err)
		}
	}

	return S_Running, fmt.Errorf("%w: commit confirmation was not received; do not run the SQL again", ExecutionPending)
}

func saveExecution(metadata *pgx.Conn, m *Migration, status string, executionErr error) (*Migration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var message *string
	if executionErr != nil {
		text := executionErr.Error()
		message = &text
	}

	updated := *m
	query := `UPDATE migrations
		SET status = $2, error_message = $3, updated_at = clock_timestamp(),
			finish_time = CASE WHEN $2 = 'running' THEN NULL ELSE clock_timestamp() END
		WHERE id = $1 AND status = 'running' AND version = $4
		RETURNING status, updated_at, finish_time, error_message`
	err := metadata.QueryRow(ctx, query, m.ID, status, message, m.Version).Scan(
		&updated.Status, &updated.UpdatedAt, &updated.FinishTime, &updated.ErrorMessage,
	)
	if err != nil {
		if status == S_Executed {
			return m, fmt.Errorf("SQL was committed, but the history was not saved; do not run it again: %w", err)
		}
		return m, fmt.Errorf("The migration result was not recorded; check the history before continuing: %w", err)
	}
	return &updated, executionErr
}

func rollbackExecution(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func closeExecutionConnection(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}
