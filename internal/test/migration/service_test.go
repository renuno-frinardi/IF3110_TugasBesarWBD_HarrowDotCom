package migration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMigrationService(t *testing.T) {
	pool := openTestDatabase(t)
	store := NewStore(pool)
	access := NewDatabaseAccess(pool)
	service := NewService(store, access)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("service_test_%d", time.Now().UnixNano())

	var databaseID int
	var userIDs []int
	var groupIDs []int

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)

		defer cleanupCancel()

		for _, query := range []string{"DELETE FROM migrations WHERE database_id = $1",
			"DELETE FROM database_connections WHERE id = $1"} {
			if _, err := pool.Exec(cleanupCtx, query, databaseID); err != nil {
				t.Errorf("gagal membersihkan database test: %v", err)
			}
		}

		for _, id := range groupIDs {
			_, err := pool.Exec(cleanupCtx, "DELETE FROM user_groups WHERE id = $1", id)

			if err != nil {
				t.Errorf("gagal menghapus group test: %v", err)
			}
		}

		for _, id := range userIDs {
			_, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", id)

			if err != nil {
				t.Errorf("gagal menghapus user test: %v", err)
			}
		}
	})

	err := pool.QueryRow(ctx, ` INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext)
								VALUES ($1, 'localhost', 5432, 'test_target', 'test_user', $2) RETURNING id`, name, []byte("dummy")).Scan(&databaseID)

	if err != nil {
		t.Fatalf("gagal membuat koneksi test: %v", err)
	}

	for _, access := range []string{"editor", "reader", "no_access"} {
		var userID int
		err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`, name+"_"+access, "test-only-hash").Scan(&userID)
		if err != nil {
			t.Fatalf("gagal membuat user test: %v", err)
		}

		userIDs = append(userIDs, userID)
	}

	// group untuk editor dan pembaca.
	for i, level := range []string{"view_edit", "view_only"} {
		var groupID int
		err := pool.QueryRow(ctx, `INSERT INTO user_groups (name) VALUES ($1) RETURNING id`, name+"_"+level).Scan(&groupID)
		if err != nil {
			t.Fatalf("gagal membuat group test: %v", err)
		}

		groupIDs = append(groupIDs, groupID)

		_, err = pool.Exec(ctx, `INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)`, groupID, userIDs[i])

		if err != nil {
			t.Fatalf("gagal memasukkan user ke group: %v", err)
		}

		_, err = pool.Exec(ctx, `INSERT INTO group_database_access (group_id, database_connection_id, access_level) VALUES ($1, $2, $3)`, groupID, databaseID, level)

		if err != nil {
			t.Fatalf("gagal memberikan akses group: %v", err)
		}
	}

	editorID := userIDs[0]
	readerID := userIDs[1]
	noAccessID := userIDs[2]

	// Editor harus dapat membuat draft melalui Service.
	draft, err := service.CreateDraft(ctx, editorID, databaseID, "Draft awal", "SELECT 1;")

	if err != nil {
		t.Fatalf("editor gagal membuat draft: %v", err)
	}

	if draft == nil {
		t.Fatal("draft editor tidak tersedia")
	}

	if draft.CreatedBy != editorID {
		t.Error("pembuat draft tidak sesuai dengan user yang meminta")
	}

	t.Run("editor_bisa_mengubah_draft", func(t *testing.T) {
		m, err := service.Update(ctx, editorID, draft.ID, "Draft diperbarui", "SELECT 2;")

		if err != nil {
			t.Fatalf("editor gagal mengubah draft: %v", err)
		}
		if m == nil {
			t.Fatal("hasil update nil")
		}

		if m.Title != "Draft diperbarui" || m.SQL != "SELECT 2;" {
			t.Error("perubahan draft tidak sesuai")
		}
	})

	t.Run("editor_dan_reader_can_read", func(t *testing.T) {
		for _, userID := range []int{editorID, readerID} {
			m, err := service.GetByID(ctx, userID, draft.ID)
			if err != nil {
				t.Fatalf("user %d gagal membaca detail: %v", userID, err)
			}
			if m == nil || m.ID != draft.ID {
				t.Fatal("detail migration tidak sesuai")
			}

			list, err := service.GetByDatabase(ctx, userID, databaseID)
			if err != nil {
				t.Fatalf("user %d gagal membaca daftar: %v", userID, err)
			}
			if len(list) != 1 {
				t.Fatalf("mengharapkan 1 migration, mendapat %d", len(list))
			}
			if list[0].ID != draft.ID {
				t.Error("daftar berisi migration yang tidak sesuai")
			}
		}
	})

	t.Run("reader_dan_user_tanpa_akses_cant_write", func(t *testing.T) {
		for _, userID := range []int{readerID, noAccessID} {
			m, err := service.CreateDraft(ctx, userID, databaseID, "Tidak diizinkan", "SELECT 3;")

			if !errors.Is(err, Forbidden) || m != nil {
				t.Fatalf("pembuatan draft user %d seharusnya ditolak: %v", userID, err)
			}

			m, err = service.Update(ctx, userID, draft.ID, "Tidak diizinkan", "SELECT 3;")

			if !errors.Is(err, Forbidden) || m != nil {
				t.Fatalf("update user %d seharusnya ditolak: %v", userID, err)
			}
		}
	})

	t.Run("user_tanpa_akses_cant_read", func(t *testing.T) {
		m, err := service.GetByID(ctx, noAccessID, draft.ID)
		if !errors.Is(err, Forbidden) || m != nil {
			t.Fatalf("pembacaan detail seharusnya ditolak: %v", err)
		}

		list, err := service.GetByDatabase(ctx, noAccessID, databaseID)
		if !errors.Is(err, Forbidden) || list != nil {
			t.Fatalf("pembacaan daftar seharusnya ditolak: %v", err)
		}
	})

	t.Run("judul_dan_sql_kosong_ditolak", func(t *testing.T) {
		inputs := []struct {
			title string
			sql   string
		}{{"", "SELECT 1;"}, {"Judul", ""}, {"   ", "SELECT 1;"}, {"Judul", "   "}}

		for _, input := range inputs {
			m, err := service.CreateDraft(
				ctx, editorID, databaseID, input.title, input.sql,
			)
			if !errors.Is(err, InvalidInput) || m != nil {
				t.Fatalf("pembuatan draft kosong seharusnya ditolak: %v", err)
			}

			m, err = service.Update(ctx, editorID, draft.ID, input.title, input.sql)
			if !errors.Is(err, InvalidInput) || m != nil {
				t.Fatalf("update dengan input kosong seharusnya ditolak: %v", err)
			}
		}
	})

	list, err := store.GetByDatabase(ctx, databaseID)
	if err != nil {
		t.Fatalf("gagal memeriksa hasil akhir: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("seharusnya hanya ada 1 migration, mendapat %d", len(list))
	}

	if list[0].Title != "Draft diperbarui" ||
		list[0].SQL != "SELECT 2;" ||
		list[0].CreatedBy != editorID {
		t.Error("data berubah setelah permintaan ditolak")
	}
}
