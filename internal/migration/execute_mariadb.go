package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// menjalankan migration SQL pada db MariaDB
func (s *Service) ExecuteMariaDB(ctx context.Context, userID, migrationID int, target *sql.DB) (*Migration, error) {
	if target == nil {
		return nil, errors.New("koneksi database MariaDB belum siap")
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
		return nil, errors.New("Gagal membuka koneksi untuk mencatat migration")
	}
	defer closeExecutionConnection(metadata)

	if _, err := metadata.Exec(ctx, "SELECT pg_advisory_lock($1, $2)", executionLock, m.DatabaseID); err != nil {
		return nil, fmt.Errorf("Gagal menunggu giliran migration: %w", err)
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
		return fmt.Errorf("Gagal menyiapkan riwayat migration: %w", err)
	}
	defer rollbackExecution(tx)

	var hasRunning bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM migrations WHERE database_id = $1 AND status = 'running')`, m.DatabaseID).Scan(&hasRunning); err != nil {
		return fmt.Errorf("Gagal memeriksa migration yang berjalan: %w", err)
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
		return fmt.Errorf("Gagal memulai migration: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("Gagal menyimpan awal migration: %w", err)
	}
	return nil
}

func runMariaDBMigrationSQL(ctx context.Context, target *sql.DB, sqlText string) (string, error) {
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return S_Failed, fmt.Errorf("Gagal memulai transaksi target MariaDB: %w", err)
	}

	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		_ = tx.Rollback()
		return S_Failed, fmt.Errorf("Gagal menjalankan SQL MariaDB (rollback best effort): %w", err)
	}

	if err := ctx.Err(); err != nil {
		_ = tx.Rollback()
		return S_Failed, fmt.Errorf("Eksekusi migration dibatalkan: %w", err)
	}
	
	if err := tx.Commit(); err != nil {
		return S_Running, fmt.Errorf("%w: konfirmasi commit MariaDB tidak diterima; jangan jalankan SQL ulang", ExecutionPending)
	}
	return S_Executed, nil
}
