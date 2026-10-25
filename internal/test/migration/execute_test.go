package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

type executionTestResult struct {
	migration *Migration
	err       error
}

func TestExecute(t *testing.T) {
	pool := openTestDatabase(t)
	migrationStore := NewStore(pool)
	service := NewService(migrationStore, NewDatabaseAccess(pool))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Buat database target sementara agar SQL tes terpisah dari metadata aplikasi.
	name := fmt.Sprintf("execute_%d", time.Now().UnixNano())
	targetName := name + "_test"
	_, err := pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{targetName}.Sanitize())
	if err != nil {
		t.Fatal("gagal membuat database target tes; user TEST_DATABASE_URL perlu izin CREATEDB")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := pool.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{targetName}.Sanitize())
		if err != nil {
			t.Error("gagal menghapus database target tes")
		}
	})

	targetConfig := pool.Config().Copy()
	targetConfig.ConnConfig.Database = targetName
	target, err := pgxpool.NewWithConfig(ctx, targetConfig)
	if err != nil {
		t.Fatal("gagal menyiapkan koneksi pemeriksaan target")
	}
	t.Cleanup(target.Close)
	if err := target.Ping(ctx); err != nil {
		t.Fatal("database target tes tidak dapat dihubungi")
	}

	key := strings.Repeat("01", 32)
	config := target.Config().ConnConfig
	encrypted, err := store.EncryptSecret(config.Password, key)
	if err != nil {
		t.Fatal("gagal mengenkripsi password tes")
	}
	sslMode := "disable"
	if config.TLSConfig != nil {
		sslMode = "require"
	}
	var databaseID int
	var userIDs, groupIDs []int
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM migrations WHERE database_id = $1", databaseID); err != nil {
			t.Error("gagal menghapus riwayat tes")
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM database_connections WHERE id = $1", databaseID); err != nil {
			t.Error("gagal menghapus koneksi tes")
		}
		for _, id := range groupIDs {
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM user_groups WHERE id = $1", id); err != nil {
				t.Error("gagal menghapus group tes")
			}
		}
		for _, id := range userIDs {
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", id); err != nil {
				t.Error("gagal menghapus user tes")
			}
		}
	})

	err = pool.QueryRow(ctx, `INSERT INTO database_connections
		(name, host, port, database_name, username, password_ciphertext, ssl_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		name, config.Host, config.Port, targetName, config.User, encrypted, sslMode,
	).Scan(&databaseID)
	if err != nil {
		t.Fatal("gagal menyimpan koneksi target tes")
	}
	for _, role := range []string{"editor", "reader", "tanpa_akses"} {
		var id int
		err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash)
			VALUES ($1, 'hash-tes') RETURNING id`, name+role).Scan(&id)
		if err != nil {
			t.Fatal("gagal membuat user tes")
		}
		userIDs = append(userIDs, id)
	}
	editorID := userIDs[0]
	readerID := userIDs[1]
	noAccessID := userIDs[2]
	for i, level := range []string{"view_edit", "view_only"} {
		var groupID int
		err := pool.QueryRow(ctx, "INSERT INTO user_groups (name) VALUES ($1) RETURNING id", name+level).Scan(&groupID)
		if err != nil {
			t.Fatal("gagal membuat group tes")
		}
		groupIDs = append(groupIDs, groupID)
		_, err = pool.Exec(ctx, "INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)", groupID, userIDs[i])
		if err != nil {
			t.Fatal("gagal memasukkan user ke group tes")
		}
		_, err = pool.Exec(ctx, `INSERT INTO group_database_access
			(group_id, database_connection_id, access_level) VALUES ($1, $2, $3)`, groupID, databaseID, level)
		if err != nil {
			t.Fatal("gagal memberi akses group tes")
		}
	}

	newDraft := func(t *testing.T, title, sql string) *Migration {
		t.Helper()
		m, err := service.CreateDraft(ctx, editorID, databaseID, title, sql)
		if err != nil {
			t.Fatalf("gagal membuat draft tes: %v", err)
		}
		return m
	}

	t.Run("commit_dan_riwayat", func(t *testing.T) {
		table := name + "_commit"
		sql := "CREATE TABLE " + table + " (id INT); INSERT INTO " + table + " VALUES (1), (2);"
		// Pembuat dan pelaksana sengaja berbeda untuk memeriksa executed_by.
		draft, err := migrationStore.CreateDraft(ctx, databaseID, "Commit", sql, readerID)
		if err != nil {
			t.Fatal("gagal membuat draft")
		}
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err != nil {
			t.Fatalf("migration gagal: %v", err)
		}
		checkExecutionHistory(t, m, S_Executed, editorID)
		if *m.Version != 1 || m.SQL != sql || m.CreatedBy != readerID || m.ErrorMessage != nil {
			t.Fatal("riwayat berhasil tidak sesuai")
		}
		var count int
		if err := target.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 2 {
			t.Fatal("perubahan belum di-commit pada target")
		}
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("SQL pengguna tidak boleh membuat tabel pada database aplikasi")
		}
		saved, err := service.GetByID(ctx, readerID, draft.ID)
		if err != nil || saved.Status != S_Executed || saved.ExecutedBy == nil || *saved.ExecutedBy != editorID {
			t.Fatal("hasil eksekusi belum tercatat di riwayat")
		}
	})

	t.Run("rollback_seluruh_script", func(t *testing.T) {
		table := name + "_rollback"
		draft := newDraft(t, "Rollback", "CREATE TABLE "+table+" (id INT); INSERT INTO "+table+" VALUES (1); SELECT 1 / 0;")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err == nil {
			t.Fatal("SQL yang gagal harus menghasilkan error")
		}
		checkExecutionHistory(t, m, S_Failed, editorID)
		if m.ErrorMessage == nil || *m.ErrorMessage == "" {
			t.Fatal("penyebab kegagalan belum dicatat")
		}
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("tabel dari script gagal seharusnya ikut di-rollback")
		}

		next := newDraft(t, "Setelah gagal", "SELECT 1;")
		nextResult, err := service.Execute(ctx, editorID, next.ID, key)
		if err != nil {
			t.Fatalf("migration berikutnya gagal: %v", err)
		}
		if nextResult.Version == nil || *nextResult.Version != *m.Version+1 {
			t.Fatal("versi gagal tidak boleh digunakan ulang")
		}
		if _, err := service.Execute(ctx, editorID, draft.ID, key); !errors.Is(err, NotDraft) {
			t.Fatal("migration gagal tidak boleh dijalankan ulang")
		}
	})

	t.Run("commit_ditolak_database", func(t *testing.T) {
		table := name + "_constraint"
		draft := newDraft(t, "Constraint", "CREATE TABLE "+table+" (id INT UNIQUE DEFERRABLE INITIALLY DEFERRED); INSERT INTO "+table+" VALUES (1), (1);")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err == nil {
			t.Fatal("commit seharusnya ditolak karena data kembar")
		}
		checkExecutionHistory(t, m, S_Failed, editorID)
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("commit yang ditolak harus membatalkan perubahan")
		}
	})

	t.Run("reader_dan_user_tanpa_akses_ditolak", func(t *testing.T) {
		draft := newDraft(t, "Akses", "SELECT 1;")
		for _, userID := range []int{readerID, noAccessID, 0} {
			m, err := service.Execute(ctx, userID, draft.ID, key)
			if m != nil || !errors.Is(err, Forbidden) {
				t.Fatal("pengguna tanpa izin edit tidak boleh execute")
			}
		}
		saved, err := migrationStore.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Draft || saved.Version != nil {
			t.Fatal("permintaan yang ditolak tidak boleh mengubah draft")
		}
	})

	t.Run("kontrol_transaksi_ditolak", func(t *testing.T) {
		table := name + "_transaction"
		draft := newDraft(t, "Commit pengguna", "CREATE TABLE "+table+" (id INT); COMMIT;")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if m != nil || !errors.Is(err, InvalidInput) {
			t.Fatal("COMMIT dari pengguna harus ditolak")
		}
		saved, err := migrationStore.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Draft || saved.Version != nil {
			t.Fatal("SQL yang ditolak validator harus tetap menjadi draft")
		}
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("SQL yang ditolak tidak boleh dijalankan")
		}
	})

	t.Run("koneksi_gagal_dicatat", func(t *testing.T) {
		draft := newDraft(t, "Kunci salah", "SELECT 1;")
		m, err := service.Execute(ctx, editorID, draft.ID, strings.Repeat("02", 32))
		if err == nil {
			t.Fatal("kunci salah harus menghasilkan error")
		}
		checkExecutionHistory(t, m, S_Failed, editorID)
		if m.ErrorMessage == nil {
			t.Fatal("kegagalan koneksi harus dicatat")
		}
	})

	t.Run("draft_sama_tidak_dijalankan_dua_kali", func(t *testing.T) {
		table := name + "_once"
		draft := newDraft(t, "Sekali", "CREATE TABLE "+table+" (id INT); INSERT INTO "+table+" VALUES (1);")
		results := make(chan executionTestResult, 2)
		for i := 0; i < 2; i++ {
			go func() {
				m, err := service.Execute(ctx, editorID, draft.ID, key)
				results <- executionTestResult{m, err}
			}()
		}
		first := <-results
		second := <-results
		// Tepat satu permintaan berhasil, yang lain harus ditolak sebagai bukan draft.
		if first.err != nil {
			first, second = second, first
		}
		if first.err != nil || !errors.Is(second.err, NotDraft) {
			t.Fatalf("hasil eksekusi ganda tidak sesuai: %v, %v", first.err, second.err)
		}
		var count int
		if err := target.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatal("SQL harus dijalankan tepat satu kali")
		}
	})

	t.Run("versi_mengikuti_urutan_perubahan", func(t *testing.T) {
		table := name + "_order"
		if _, err := target.Exec(ctx, "CREATE TABLE "+table+" (position SERIAL, label TEXT)"); err != nil {
			t.Fatal("gagal membuat tabel urutan tes")
		}
		firstDraft := newDraft(t, "Pertama", "SELECT pg_sleep(0.1); INSERT INTO "+table+" (label) VALUES ('pertama');")
		secondDraft := newDraft(t, "Kedua", "INSERT INTO "+table+" (label) VALUES ('kedua');")
		results := make(chan executionTestResult, 2)
		for _, id := range []int{firstDraft.ID, secondDraft.ID} {
			go func(id int) {
				m, err := service.Execute(ctx, editorID, id, key)
				results <- executionTestResult{m, err}
			}(id)
		}
		first := <-results
		second := <-results
		if first.err != nil || second.err != nil {
			t.Fatalf("eksekusi bersamaan gagal: %v, %v", first.err, second.err)
		}
		if *first.migration.Version > *second.migration.Version {
			first, second = second, first
		}
		if *second.migration.Version != *first.migration.Version+1 {
			t.Fatal("versi harus berurutan tanpa duplikat")
		}
		var firstLabel string
		if err := target.QueryRow(ctx, "SELECT label FROM "+table+" ORDER BY position LIMIT 1").Scan(&firstLabel); err != nil {
			t.Fatal("gagal membaca urutan perubahan target")
		}
		if firstLabel != strings.ToLower(first.migration.Title) {
			t.Fatal("urutan perubahan target tidak sesuai nomor versi")
		}
	})

	t.Run("memakai_sql_terbaru_saat_draft_diklaim", func(t *testing.T) {
		draft := newDraft(t, "Sebelum edit", "COMMIT;")
		table := name + "_edited"
		newSQL := "CREATE TABLE " + table + " (id INT); INSERT INTO " + table + " VALUES (7);"

		// Tahan perubahan draft agar Execute harus menunggu UPDATE ini selesai.
		edit, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal("gagal memulai edit draft tes")
		}
		defer rollbackExecution(edit)
		_, err = edit.Exec(ctx, "UPDATE migrations SET title = 'Setelah edit', sql = $1 WHERE id = $2", newSQL, draft.ID)
		if err != nil {
			t.Fatal("gagal mengedit draft tes")
		}
		result := make(chan executionTestResult, 1)
		go func() {
			m, err := service.Execute(ctx, editorID, draft.ID, key)
			result <- executionTestResult{m, err}
		}()

		err = waitForBlockedQuery(ctx, pool, int(edit.Conn().PgConn().PID()))
		if err != nil {
			rollbackExecution(edit)
			<-result
			t.Fatalf("Execute tidak menunggu edit: %v", err)
		}
		if err := edit.Commit(ctx); err != nil {
			rollbackExecution(edit)
			<-result
			t.Fatal("gagal menyimpan edit draft tes")
		}
		finished := <-result
		if finished.err != nil {
			t.Fatalf("eksekusi SQL terbaru gagal: %v", finished.err)
		}
		if finished.migration.SQL != newSQL || finished.migration.Title != "Setelah edit" {
			t.Fatal("SQL yang dicatat tidak sesuai draft terbaru")
		}
		var value int
		if err := target.QueryRow(ctx, "SELECT id FROM "+table).Scan(&value); err != nil || value != 7 {
			t.Fatal("SQL terbaru belum dijalankan pada target")
		}
	})

	t.Run("pembatalan_tetap_mencatat_kegagalan", func(t *testing.T) {
		table := name + "_cancel"
		draft := newDraft(t, "Dibatalkan", "CREATE TABLE "+table+" (id INT); SELECT pg_sleep(10);")
		requestCtx, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan executionTestResult, 1)
		go func() {
			m, err := service.Execute(requestCtx, editorID, draft.ID, key)
			result <- executionTestResult{m, err}
		}()
		waitForRunningMigration(t, pool, draft.ID)
		stop()
		finished := <-result
		if !errors.Is(finished.err, context.Canceled) {
			t.Fatalf("mengharapkan pembatalan: %v", finished.err)
		}
		checkExecutionHistory(t, finished.migration, S_Failed, editorID)
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("perubahan dari request batal harus di-rollback")
		}
	})

	t.Run("running_tertinggal_menghentikan_eksekusi_baru", func(t *testing.T) {
		previous := newDraft(t, "Belum pasti", "SELECT 1;")
		if _, err := pool.Exec(ctx, "UPDATE migrations SET status = 'running' WHERE id = $1", previous.ID); err != nil {
			t.Fatal("gagal menyiapkan migration running")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM migrations WHERE id = $1", previous.ID); err != nil {
				t.Error("gagal menghapus migration running tes")
			}
		})
		draft := newDraft(t, "Menunggu pemeriksaan", "SELECT 2;")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if m != nil || !errors.Is(err, ExecutionPending) {
			t.Fatal("migration baru harus menunggu pemeriksaan hasil sebelumnya")
		}
		saved, err := migrationStore.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Draft || saved.Version != nil {
			t.Fatal("draft yang menunggu pemeriksaan tidak boleh dijalankan")
		}
	})

	t.Run("commit_berhasil_tetapi_pencatatan_gagal", func(t *testing.T) {
		table := name + "_unrecorded"
		draft := newDraft(t, "Riwayat gagal", "CREATE TABLE "+table+" (id INT); INSERT INTO "+table+" VALUES (1);")
		functionName := pgx.Identifier{name + "_history_error"}.Sanitize()
		triggerName := pgx.Identifier{name + "_history_trigger"}.Sanitize()
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "DROP TRIGGER IF EXISTS "+triggerName+" ON migrations"); err != nil {
				t.Error("gagal menghapus trigger tes")
			}
			if _, err := pool.Exec(cleanupCtx, "DROP FUNCTION IF EXISTS "+functionName+"()"); err != nil {
				t.Error("gagal menghapus fungsi tes")
			}
		})
		// Simulasikan kegagalan pencatatan hanya untuk migration milik tes ini.
		query := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
				IF NEW.id = %d AND NEW.status = 'executed' THEN
					RAISE EXCEPTION 'simulasi gagal menyimpan riwayat';
				END IF;
				RETURN NEW;
			END;
			$$;
			CREATE TRIGGER %s BEFORE UPDATE ON migrations
			FOR EACH ROW EXECUTE FUNCTION %s();`, functionName, draft.ID, triggerName, functionName)
		if _, err := pool.Exec(ctx, query); err != nil {
			t.Fatal("gagal menyiapkan simulasi kegagalan riwayat")
		}
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err == nil || m == nil || m.Status != S_Running {
			t.Fatal("hasil yang belum tercatat harus tetap ditandai running")
		}
		var count int
		if err := target.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatal("SQL target seharusnya sudah di-commit")
		}
		saved, err := migrationStore.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Running || saved.FinishTime != nil {
			t.Fatal("riwayat tidak boleh mengaku selesai saat pencatatan gagal")
		}
		next := newDraft(t, "Tunggu hasil sebelumnya", "SELECT 1;")
		if _, err := service.Execute(ctx, editorID, next.ID, key); !errors.Is(err, ExecutionPending) {
			t.Fatal("eksekusi berikutnya harus berhenti sampai riwayat diperiksa")
		}
	})
}

func checkExecutionHistory(t *testing.T, m *Migration, status string, userID int) {
	t.Helper()
	if m == nil {
		t.Fatal("hasil migration tidak tersedia")
	}
	if m.Status != status || m.Version == nil || *m.Version < 1 {
		t.Fatalf("status atau versi tidak sesuai: status %s", m.Status)
	}
	if m.ExecutedBy == nil || *m.ExecutedBy != userID {
		t.Fatal("pelaksana migration belum tercatat dengan benar")
	}
	if m.StartTime == nil || m.FinishTime == nil || m.UpdatedAt == nil {
		t.Fatal("waktu eksekusi belum lengkap")
	}
	if m.FinishTime.Before(*m.StartTime) {
		t.Fatal("waktu selesai lebih awal daripada waktu mulai")
	}
}

func waitForRunningMigration(t *testing.T, pool *pgxpool.Pool, id int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM migrations WHERE id = $1", id).Scan(&status); err != nil {
			t.Fatal("gagal memeriksa status migration tes")
		}
		if status == S_Running {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("migration tidak mencapai status running")
		case <-ticker.C:
		}
	}
}

// Tunggu sampai koneksi Execute tertahan oleh transaksi edit draft.
func waitForBlockedQuery(ctx context.Context, pool *pgxpool.Pool, blockingPID int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))
		)`, blockingPID).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
