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
	service := NewService(NewStore(pool), NewDatabaseAccess(pool))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Buat database target sementara agar SQL tes terpisah dari metadata aplikasi.
	name := fmt.Sprintf("execute_%d", time.Now().UnixNano())
	targetName := name + "_test"
	_, err := pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{targetName}.Sanitize())
	if err != nil {
		t.Fatal("failed to create the test target database; the TEST_DATABASE_URL user requires CREATEDB privilege")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := pool.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{targetName}.Sanitize())
		if err != nil {
			t.Error("failed to delete the test target database")
		}
	})

	targetConfig := pool.Config().Copy()
	targetConfig.ConnConfig.Database = targetName
	target, err := pgxpool.NewWithConfig(ctx, targetConfig)
	if err != nil {
		t.Fatal("failed to prepare the target inspection connection")
	}
	t.Cleanup(target.Close)
	if err := target.Ping(ctx); err != nil {
		t.Fatal("the test target database is unreachable")
	}

	key := strings.Repeat("01", 32)
	config := target.Config().ConnConfig
	encrypted, err := store.EncryptSecret(config.Password, key)
	if err != nil {
		t.Fatal("failed to encrypt the test password")
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
			t.Error("failed to delete the test history")
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM database_connections WHERE id = $1", databaseID); err != nil {
			t.Error("failed to delete the test connection")
		}
		for _, id := range groupIDs {
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM user_groups WHERE id = $1", id); err != nil {
				t.Error("failed to delete the test group")
			}
		}
		for _, id := range userIDs {
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", id); err != nil {
				t.Error("failed to delete the test user")
			}
		}
	})

	err = pool.QueryRow(ctx, `INSERT INTO database_connections
		(name, host, port, database_name, username, password_ciphertext, ssl_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		name, config.Host, config.Port, targetName, config.User, encrypted, sslMode,
	).Scan(&databaseID)
	if err != nil {
		t.Fatal("failed to save the test target connection")
	}
	for _, role := range []string{"editor", "reader", "tanpa_akses"} {
		var id int
		err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash)
			VALUES ($1, 'hash-tes') RETURNING id`, name+role).Scan(&id)
		if err != nil {
			t.Fatal("failed to create the test user")
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
			t.Fatal("failed to create the test group")
		}
		groupIDs = append(groupIDs, groupID)
		_, err = pool.Exec(ctx, "INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)", groupID, userIDs[i])
		if err != nil {
			t.Fatal("failed to add the user to the test group")
		}
		_, err = pool.Exec(ctx, `INSERT INTO group_database_access
			(group_id, database_connection_id, access_level) VALUES ($1, $2, $3)`, groupID, databaseID, level)
		if err != nil {
			t.Fatal("failed to grant access to the test group")
		}
	}

	newDraft := func(t *testing.T, title, sql string) *Migration {
		t.Helper()
		m, err := service.CreateDraft(ctx, editorID, databaseID, title, sql)
		if err != nil {
			t.Fatalf("failed to create the test draft: %v", err)
		}
		return m
	}

	t.Run("commit_dan_riwayat", func(t *testing.T) {
		table := name + "_commit"
		sql := "CREATE TABLE " + table + " (id INT); INSERT INTO " + table + " VALUES (1), (2);"
		// Pembuat dan pelaksana sengaja berbeda untuk memeriksa executed_by.
		draft, err := service.store.CreateDraft(ctx, databaseID, "Commit", sql, readerID)
		if err != nil {
			t.Fatal("failed to create the draft")
		}
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err != nil {
			t.Fatalf("migration failed: %v", err)
		}
		checkExecutionHistory(t, m, S_Executed, editorID)
		if *m.Version != 1 || m.SQL != sql || m.CreatedBy != readerID || m.ErrorMessage != nil {
			t.Fatal("successful history is incorrect")
		}
		var count int
		if err := target.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 2 {
			t.Fatal("the changes were not committed to the target")
		}
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("user SQL must not create tables in the application database")
		}
		saved, err := service.GetByID(ctx, readerID, draft.ID)
		if err != nil || saved.Status != S_Executed || saved.ExecutedBy == nil || *saved.ExecutedBy != editorID {
			t.Fatal("the execution result was not recorded in the history")
		}
	})

	t.Run("rollback_seluruh_script", func(t *testing.T) {
		table := name + "_rollback"
		draft := newDraft(t, "Rollback", "CREATE TABLE "+table+" (id INT); INSERT INTO "+table+" VALUES (1); SELECT 1 / 0;")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err == nil {
			t.Fatal("failed SQL must return an error")
		}
		checkExecutionHistory(t, m, S_Failed, editorID)
		if m.ErrorMessage == nil || *m.ErrorMessage == "" {
			t.Fatal("the failure reason was not recorded")
		}
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("tables from a failed script should also be rolled back")
		}

		next := newDraft(t, "Setelah gagal", "SELECT 1;")
		nextResult, err := service.Execute(ctx, editorID, next.ID, key)
		if err != nil {
			t.Fatalf("the next migration failed: %v", err)
		}
		if nextResult.Version == nil || *nextResult.Version != *m.Version+1 {
			t.Fatal("a failed version must not be reused")
		}
		if _, err := service.Execute(ctx, editorID, draft.ID, key); !errors.Is(err, NotDraft) {
			t.Fatal("a failed migration must not be executed again")
		}
	})

	t.Run("commit_ditolak_database", func(t *testing.T) {
		table := name + "_constraint"
		draft := newDraft(t, "Constraint", "CREATE TABLE "+table+" (id INT UNIQUE DEFERRABLE INITIALLY DEFERRED); INSERT INTO "+table+" VALUES (1), (1);")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err == nil {
			t.Fatal("the commit should be rejected because of duplicate data")
		}
		checkExecutionHistory(t, m, S_Failed, editorID)
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("a rejected commit must roll back the changes")
		}
	})

	t.Run("reader_dan_user_tanpa_akses_ditolak", func(t *testing.T) {
		draft := newDraft(t, "Akses", "SELECT 1;")
		for _, userID := range []int{readerID, noAccessID, 0} {
			m, err := service.Execute(ctx, userID, draft.ID, key)
			if m != nil || !errors.Is(err, Forbidden) {
				t.Fatal("a user without edit permission must not execute")
			}
		}
		saved, err := service.store.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Draft || saved.Version != nil {
			t.Fatal("a rejected request must not change the draft")
		}
	})

	t.Run("kontrol_transaksi_ditolak", func(t *testing.T) {
		table := name + "_transaction"
		draft := newDraft(t, "Commit pengguna", "CREATE TABLE "+table+" (id INT); COMMIT;")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if m != nil || !errors.Is(err, InvalidInput) {
			t.Fatal("user-supplied COMMIT must be rejected")
		}
		saved, err := service.store.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Draft || saved.Version != nil {
			t.Fatal("SQL rejected by the validator must remain a draft")
		}
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || exists {
			t.Fatal("rejected SQL must not be executed")
		}
	})

	t.Run("koneksi_gagal_dicatat", func(t *testing.T) {
		draft := newDraft(t, "Kunci salah", "SELECT 1;")
		m, err := service.Execute(ctx, editorID, draft.ID, strings.Repeat("02", 32))
		if err == nil {
			t.Fatal("an invalid key must produce an error")
		}
		checkExecutionHistory(t, m, S_Failed, editorID)
		if m.ErrorMessage == nil {
			t.Fatal("connection failure must be recorded")
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
			t.Fatalf("duplicate execution results are incorrect: %v, %v", first.err, second.err)
		}
		var count int
		if err := target.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatal("SQL must be executed exactly once")
		}
	})

	t.Run("versi_mengikuti_urutan_perubahan", func(t *testing.T) {
		table := name + "_order"
		if _, err := target.Exec(ctx, "CREATE TABLE "+table+" (position SERIAL, label TEXT)"); err != nil {
			t.Fatal("failed to create the test ordering table")
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
			t.Fatalf("concurrent execution failed: %v, %v", first.err, second.err)
		}
		if *first.migration.Version > *second.migration.Version {
			first, second = second, first
		}
		if *second.migration.Version != *first.migration.Version+1 {
			t.Fatal("versions must be sequential without duplicates")
		}
		var firstLabel string
		if err := target.QueryRow(ctx, "SELECT label FROM "+table+" ORDER BY position LIMIT 1").Scan(&firstLabel); err != nil {
			t.Fatal("failed to read the target change order")
		}
		if firstLabel != strings.ToLower(first.migration.Title) {
			t.Fatal("the target change order does not match the version numbers")
		}
	})

	t.Run("memakai_sql_terbaru_saat_draft_diklaim", func(t *testing.T) {
		draft := newDraft(t, "Sebelum edit", "COMMIT;")
		table := name + "_edited"
		newSQL := "CREATE TABLE " + table + " (id INT); INSERT INTO " + table + " VALUES (7);"

		// Tahan perubahan draft agar Execute harus menunggu UPDATE ini selesai.
		edit, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal("failed to start editing the test draft")
		}
		defer rollbackExecution(edit)
		_, err = edit.Exec(ctx, "UPDATE migrations SET title = 'Setelah edit', sql = $1 WHERE id = $2", newSQL, draft.ID)
		if err != nil {
			t.Fatal("failed to edit the test draft")
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
			t.Fatalf("Execute did not wait for the update: %v", err)
		}
		if err := edit.Commit(ctx); err != nil {
			rollbackExecution(edit)
			<-result
			t.Fatal("failed to save the test draft update")
		}
		finished := <-result
		if finished.err != nil {
			t.Fatalf("execution of the latest SQL failed: %v", finished.err)
		}
		if finished.migration.SQL != newSQL || finished.migration.Title != "Setelah edit" {
			t.Fatal("the recorded SQL does not match the latest draft")
		}
		var value int
		if err := target.QueryRow(ctx, "SELECT id FROM "+table).Scan(&value); err != nil || value != 7 {
			t.Fatal("the latest SQL was not executed on the target")
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
			t.Fatal("changes from a canceled request must be rolled back")
		}
	})

	t.Run("running_tertinggal_menghentikan_eksekusi_baru", func(t *testing.T) {
		previous := newDraft(t, "Belum pasti", "SELECT 1;")
		if _, err := pool.Exec(ctx, "UPDATE migrations SET status = 'running' WHERE id = $1", previous.ID); err != nil {
			t.Fatal("failed to prepare the running migration")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM migrations WHERE id = $1", previous.ID); err != nil {
				t.Error("failed to delete the test running migration")
			}
		})
		draft := newDraft(t, "Menunggu pemeriksaan", "SELECT 2;")
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if m != nil || !errors.Is(err, ExecutionPending) {
			t.Fatal("a new migration must wait for the previous result to be checked")
		}
		saved, err := service.store.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Draft || saved.Version != nil {
			t.Fatal("a draft awaiting review must not be executed")
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
				t.Error("failed to delete the test trigger")
			}
			if _, err := pool.Exec(cleanupCtx, "DROP FUNCTION IF EXISTS "+functionName+"()"); err != nil {
				t.Error("failed to delete the test function")
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
			t.Fatal("failed to prepare the history failure simulation")
		}
		m, err := service.Execute(ctx, editorID, draft.ID, key)
		if err == nil || m == nil || m.Status != S_Running {
			t.Fatal("an unrecorded result must remain marked as running")
		}
		var count int
		if err := target.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatal("target SQL should already be committed")
		}
		saved, err := service.store.GetByID(ctx, draft.ID)
		if err != nil || saved.Status != S_Running || saved.FinishTime != nil {
			t.Fatal("the history must not report completion when recording fails")
		}
		next := newDraft(t, "Tunggu hasil sebelumnya", "SELECT 1;")
		if _, err := service.Execute(ctx, editorID, next.ID, key); !errors.Is(err, ExecutionPending) {
			t.Fatal("the next execution must stop until the history is checked")
		}
	})
}

func checkExecutionHistory(t *testing.T, m *Migration, status string, userID int) {
	t.Helper()
	if m == nil {
		t.Fatal("migration result is unavailable")
	}
	if m.Status != status || m.Version == nil || *m.Version < 1 {
		t.Fatalf("status or version is incorrect: status %s", m.Status)
	}
	if m.ExecutedBy == nil || *m.ExecutedBy != userID {
		t.Fatal("the migration executor was not recorded correctly")
	}
	if m.StartTime == nil || m.FinishTime == nil || m.UpdatedAt == nil {
		t.Fatal("execution timestamps are incomplete")
	}
	if m.FinishTime.Before(*m.StartTime) {
		t.Fatal("the finish time is earlier than the start time")
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
			t.Fatal("failed to check the test migration status")
		}
		if status == S_Running {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("migration did not reach the running status")
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
