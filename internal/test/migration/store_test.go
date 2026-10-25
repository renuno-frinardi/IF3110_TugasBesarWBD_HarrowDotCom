package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// go test ./internal/migration -run '^TestCreateDraft$' -v -count=1
// test 1 : nyimpen draft dengan data yang sesuai
// test 2 : menolak penyimpanan untuk ID database yang ga terdaftar
// test 3 : nanganin context yang sudah dibatalkan.
// test 4 : mengambil daftar migration dari db
// test 5 : update bukan draft

func TestCreateDraft(t *testing.T) {
	pool := openTestDatabase(t)
	store := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("store_test_%d", time.Now().UnixNano())
	var databaseID int
	err := pool.QueryRow(ctx, `INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext) VALUES ($1, 'localhost', 5432, 'test_target', 'test_user', $2) RETURNING id`, name, []byte("dummy")).Scan(&databaseID)
	if err != nil {
		t.Fatalf("gagal membuat data koneksi test: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, query := range []string{
			"DELETE FROM migrations WHERE database_id = $1",
			"DELETE FROM database_connections WHERE id = $1",
		} {
			if _, err := pool.Exec(cleanupCtx, query, databaseID); err != nil {
				t.Errorf("gagal membersihkan data test: %v", err)
			}
		}
		if _, err := pool.Exec(cleanupCtx, "DROP TABLE IF EXISTS "+name); err != nil {
			t.Errorf("gagal membersihkan tabel percobaan: %v", err)
		}
	})

	t.Run("draft_berhasil_disimpan", func(t *testing.T) {
		title := "test's draft " + name
		sqlText := "CREATE TABLE " + name + " (id INT);"
		m, err := store.CreateDraft(ctx, databaseID, title, sqlText, 7)
		if err != nil {
			t.Fatalf("gagal membuat draft: %v", err)
		}

		if m == nil {
			t.Fatal("draft yang dikembalikan nil")
		}

		if m.ID <= 0 {
			t.Error("ID draft belum terisi")
		}

		if m.CreatedAt.IsZero() {
			t.Error("waktu pembuatan belum terisi")
		}

		if m.DatabaseID != databaseID || m.Title != title ||
			m.SQL != sqlText || m.Status != S_Draft || m.CreatedBy != 7 {
			t.Error("data draft tidak sesuai dengan input")
		}

		// mastiin data kesimpen

		saved, err := store.GetByID(ctx, m.ID)
		if err != nil {
			t.Fatalf("gagal mengambil draft: %v", err)
		}

		if saved == nil {
			t.Fatal("draft yang sudah disimpan tidak ditemukan")
		}

		if saved.ID != m.ID || saved.DatabaseID != databaseID ||
			saved.Title != title || saved.SQL != sqlText ||
			saved.Status != S_Draft || saved.CreatedBy != 7 {
			t.Error("data draft yang dibaca tidak sesuai")
		}

		if !saved.CreatedAt.Equal(m.CreatedAt) {
			t.Error("waktu pembuatan draft tidak sesuai")
		}

		if saved.Version != nil || saved.ExecutedBy != nil ||
			saved.StartTime != nil || saved.FinishTime != nil ||
			saved.ErrorMessage != nil {
			t.Error("draft belum seharusnya memiliki informasi eksekusi")
		}

		var tableExists bool
		err = pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&tableExists)
		if err != nil {
			t.Fatalf("gagal periksa tabel: %v", err)
		}
		if tableExists {
			t.Error("SQL draft sudah dijalankan; seharusnya hanya disimpan sebagai teks")
		}
	})

	t.Run("migration_tidak_ditemukan", func(t *testing.T) {
		m, err := store.GetByID(ctx, -999)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("seharusnya data gagal ditemukan")
		}
		if m != nil {
			t.Error("migration harus nil jika ID tidak ditemukan")
		}
	})

	t.Run("database_tidak_terdaftar", func(t *testing.T) {
		m, err := store.CreateDraft(ctx, -1, "Database tidak ada", "SELECT 1;", 7)

		if err == nil {
			t.Fatal("seharusnya gagal karena database tidak terdaftar")
		}

		if m != nil {
			t.Error("migration harus nil ketika penyimpanan gagal")
		}
	})

	t.Run("context_dibatalkan", func(t *testing.T) {
		canceledCtx, stop := context.WithCancel(ctx)
		stop()
		m, err := store.CreateDraft(canceledCtx, databaseID, "Canceled draft", "SELECT 1;", 7)
		if !errors.Is(err, context.Canceled) || m != nil {
			t.Fatalf("mengharapkan migration nil dan context.Canceled, mendapat: %v", err)
		}
	})

	t.Run("daftar_migration_per_database", func(t *testing.T) {
		// buat koneksi kedua buat cek migration.
		var otherDatabaseID int
		err := pool.QueryRow(ctx, `
			INSERT INTO database_connections (
				name, host, port, database_name, username, password_ciphertext
			)
			VALUES ($1, 'localhost', 5432, 'other_target', 'test_user', $2)
			RETURNING id
		`, name+"_other", []byte("dummy")).Scan(&otherDatabaseID)

		if err != nil {
			t.Fatalf("gagal membuat koneksi kedua: %v", err)
		}

		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(
				context.Background(), 10*time.Second,
			)
			defer cleanupCancel()

			for _, query := range []string{
				"DELETE FROM migrations WHERE database_id = $1",
				"DELETE FROM database_connections WHERE id = $1",
			} {
				if _, err := pool.Exec(cleanupCtx, query, otherDatabaseID); err != nil {
					t.Errorf("gagal membersihkan data test: %v", err)
				}
			}
		})

		// Database kedua belum memiliki migration.
		emptyList, err := store.GetByDatabase(ctx, otherDatabaseID)
		if err != nil {
			t.Fatalf("gagal mengambil daftar kosong: %v", err)
		}
		if len(emptyList) != 0 {
			t.Fatal("database kedua seharusnya belum memiliki migration")
		}

		// nyimpen draft di db 2.
		otherDraft, err := store.CreateDraft(
			ctx, otherDatabaseID, "Draft kedua", "SELECT 2;", 7,
		)
		if err != nil {
			t.Fatalf("gagal membuat draft kedua: %v", err)
		}
		if otherDraft == nil {
			t.Fatal("draft kedua tidak tersedia")
		}

		list, err := store.GetByDatabase(ctx, databaseID)
		if err != nil {
			t.Fatalf("gagal mengambil daftar migration: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("mengharapkan 1 migration, mendapat %d", len(list))
		}

		if list[0].DatabaseID != databaseID ||
			list[0].Title != "test's draft "+name ||
			list[0].Status != S_Draft {
			t.Error("data migration database pertama tidak sesuai")
		}

		otherList, err := store.GetByDatabase(ctx, otherDatabaseID)
		if err != nil {
			t.Fatalf("gagal mengambil daftar database kedua: %v", err)
		}
		if len(otherList) != 1 {
			t.Fatalf("mengharapkan 1 migration, mendapat %d", len(otherList))
		}

		if otherList[0].ID != otherDraft.ID ||
			otherList[0].DatabaseID != otherDatabaseID {
			t.Error("data migration database kedua tidak sesuai")
		}
	})

	t.Run("mengubah_draft", func(t *testing.T) {
		list, err := store.GetByDatabase(ctx, databaseID)
		if err != nil {
			t.Fatalf("gagal mengambil draft: %v", err)
		}
		if len(list) != 1 {
			t.Fatal("seharusnya tersedia satu draft untuk diubah")
		}

		draft := list[0]

		updated, err := store.Update(
			ctx, draft.ID, "Judul baru", "SELECT 2;",
		)
		if err != nil {
			t.Fatalf("gagal mengubah draft: %v", err)
		}
		if updated == nil {
			t.Fatal("hasil perubahan draft nil")
		}

		// Mastiin perubahan berhasil
		saved, err := store.GetByID(ctx, draft.ID)
		if err != nil {
			t.Fatalf("gagal membaca draft: %v", err)
		}
		if saved == nil {
			t.Fatal("draft tidak ditemukan")
		}

		if saved.Title != "Judul baru" || saved.SQL != "SELECT 2;" {
			t.Error("judul atau SQL belum berubah")
		}

		if saved.UpdatedAt == nil || saved.UpdatedAt.IsZero() {
			t.Error("waktu perubahan belum terisi")
		}

		if saved.Status != S_Draft ||
			saved.CreatedBy != draft.CreatedBy ||
			!saved.CreatedAt.Equal(draft.CreatedAt) {
			t.Error("status atau informasi pembuatan ikut berubah")
		}
	})

	t.Run("menolak_update_bukan_draft", func(t *testing.T) {
		list, err := store.GetByDatabase(ctx, databaseID)
		if err != nil {
			t.Fatalf("gagal mengambil draft: %v", err)
		}
		if len(list) != 1 {
			t.Fatal("seharusnya tersedia satu draft untuk diubah")
		}
		draft := list[0]

		for _, status := range []string{S_Running, S_Executed, S_Failed} {
			_, err := pool.Exec(ctx, "UPDATE migrations SET status = $1 WHERE id = $2", status, draft.ID)

			if err != nil {
				t.Fatalf("gagal mengubah status draft menjadi %s: %v", status, err)
			}

			m, err := store.Update(ctx, draft.ID, "Tidak boleh berubah", "SELECT 3;")

			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("seharusnya update ditolak: %v", err)
			}
			if m != nil {
				t.Fatal("migration harus nil ketika update ditolak")
			}

			// Mastiin data ga berubah
			saved, err := store.GetByID(ctx, draft.ID)
			if err != nil {
				t.Fatalf("gagal membaca draft: %v", err)
			}
			if saved == nil {
				t.Fatal("draft tidak ditemukan")
			}

			if saved.Title != draft.Title || saved.SQL != draft.SQL || saved.Status != status {
				t.Error("judul atau SQL ikut berubah padahal update ditolak")
			}
		}
	})
	t.Run("update_id_tidak_ditemukan", func(t *testing.T) {
		m, err := store.Update(ctx, -999, "Tidak ditemukan", "SELECT 1;")

		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("seharusnya ID tidak ditemukan, mendapat: %v", err)
		}

		if m != nil {
			t.Error("hasil update harus nil jika ID tidak ditemukan")
		}
	})

	var count int
	err = pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM migrations WHERE database_id = $1", databaseID,
	).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("seharusnya hanya satu draft tersimpan; mendapat %d, error: %v", count, err)
	}
}

func openTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if err := godotenv.Load("../../../.env"); err != nil {
		t.Fatal("gagal membaca file .env")
	}

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL belum diatur")
	}

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("konfigurasi database tidak valid")
	}

	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("nama database untuk testing harus berakhiran _test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("gagal membuat koneksi database")
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatal("database tidak dapat dihubungi")
	}

	return pool
}
