package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var NotDraft = errors.New("hanya draft yang boleh dijalankan")
var ExecutionPending = errors.New("hasil migration sebelumnya perlu diperiksa")

// Nomor pembeda untuk lock fitur migration pada database aplikasi.
const executionLock = 311016

// Execute menjalankan SQL yang tersimpan dalam draft.
// userID berasal dari session, sedangkan hexKey berasal dari konfigurasi server.
func (s *Service) Execute(ctx context.Context, userID, migrationID int, hexKey string) (*Migration, error) {
	if s.store == nil || s.store.pool == nil {
		return nil, errors.New("koneksi database aplikasi belum siap")
	}

	// 1. Periksa migration dan izin pengguna.
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

	// 2. Pegang lock sampai hasil selesai dicatat. Koneksi ini khusus metadata,
	// ditutup setelah selesai agar tidak mengembalikan lock ke pool aplikasi.
	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	metadata, err := pgx.ConnectConfig(connectCtx, s.store.pool.Config().ConnConfig)
	cancelConnect()
	if err != nil {
		return nil, errors.New("gagal membuka koneksi untuk mencatat migration")
	}
	defer closeExecutionConnection(metadata)

	_, err = metadata.Exec(ctx, "SELECT pg_advisory_lock($1, $2)", executionLock, m.DatabaseID)
	if err != nil {
		return nil, fmt.Errorf("gagal menunggu giliran migration: %w", err)
	}

	// Izin mungkin berubah selama menunggu giliran.
	if err := s.checkAccess(ctx, userID, m.DatabaseID, true); err != nil {
		return nil, err
	}

	// 3. Ubah draft menjadi running, ambil versi dan SQL terbaru secara bersamaan.
	if err := claimMigration(ctx, metadata, m, userID); err != nil {
		return nil, err
	}

	// 4. SQL pengguna hanya dijalankan pada koneksi target.
	target, err := s.MakeConnection(ctx, userID, m.DatabaseID, hexKey)
	if err != nil {
		return saveExecution(metadata, m, S_Failed, err)
	}
	defer closeExecutionConnection(target)

	status, executionErr := runMigrationSQL(ctx, target, m.SQL)

	// 5. Catat hasil meskipun request pengguna sudah dibatalkan.
	return saveExecution(metadata, m, status, executionErr)
}

// Dipanggil setelah lock per database didapatkan.
func claimMigration(ctx context.Context, metadata *pgx.Conn, m *Migration, userID int) error {
	tx, err := metadata.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gagal menyiapkan riwayat migration: %w", err)
	}
	defer rollbackExecution(tx)

	// Running yang tertinggal bisa berarti proses mati atau hasil commit belum jelas.
	var hasRunning bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM migrations WHERE database_id = $1 AND status = 'running'
	)`, m.DatabaseID).Scan(&hasRunning)
	if err != nil {
		return fmt.Errorf("gagal memeriksa migration yang berjalan: %w", err)
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
		return fmt.Errorf("gagal memulai migration: %w", err)
	}

	// Validasi SQL yang benar-benar diambil saat draft dikunci.
	// Jika ditolak, rollback metadata menjaga status tetap draft tanpa nomor versi.
	if err := validateSQL(m.SQL); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("gagal menyimpan awal migration; periksa riwayat sebelum mencoba lagi: %w", err)
	}
	return nil
}

func runMigrationSQL(ctx context.Context, target *pgx.Conn, sql string) (string, error) {
	tx, err := target.Begin(ctx)
	if err != nil {
		return S_Failed, fmt.Errorf("gagal memulai transaksi target: %w", err)
	}
	defer rollbackExecution(tx)

	// pgx dapat menjalankan seluruh script tanpa memecahnya berdasarkan titik koma.
	if _, err := tx.Exec(ctx, sql); err != nil {
		return S_Failed, fmt.Errorf("gagal menjalankan SQL: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return S_Failed, fmt.Errorf("eksekusi migration dibatalkan: %w", err)
	}

	err = tx.Commit(ctx)
	if err == nil {
		return S_Executed, nil
	}

	// PostgreSQL bisa menolak commit, misalnya karena constraint yang diperiksa
	// di akhir transaksi. Status I berarti server sudah kembali di luar transaksi.
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return S_Failed, fmt.Errorf("transaksi dibatalkan oleh database: %w", err)
	}
	var databaseErr *pgconn.PgError
	if errors.As(err, &databaseErr) && databaseErr.Severity == "ERROR" {
		if !target.IsClosed() && target.PgConn().TxStatus() == 'I' {
			return S_Failed, fmt.Errorf("commit ditolak oleh database: %w", err)
		}
	}

	// Koneksi putus saat commit tidak membuktikan bahwa SQL gagal.
	return S_Running, fmt.Errorf("%w: konfirmasi commit tidak diterima; jangan jalankan SQL ulang", ExecutionPending)
}

func saveExecution(metadata *pgx.Conn, m *Migration, status string, executionErr error) (*Migration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var message *string
	if executionErr != nil {
		text := executionErr.Error()
		message = &text
	}

	// Salin hasil hanya setelah penyimpanan berhasil.
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
			return m, fmt.Errorf("SQL sudah di-commit, tetapi riwayat belum tersimpan; jangan jalankan ulang: %w", err)
		}
		return m, fmt.Errorf("hasil migration belum tercatat; periksa riwayat sebelum melanjutkan: %w", err)
	}
	return &updated, executionErr
}

func rollbackExecution(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Aman dipanggil setelah commit; jika rollback gagal, pgx menutup koneksinya.
	_ = tx.Rollback(ctx)
}

func closeExecutionConnection(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}
