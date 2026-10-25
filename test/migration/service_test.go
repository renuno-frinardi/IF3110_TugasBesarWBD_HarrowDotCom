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
				t.Errorf("failed to clean up the test database: %v", err)
			}
		}

		for _, id := range groupIDs {
			_, err := pool.Exec(cleanupCtx, "DELETE FROM user_groups WHERE id = $1", id)

			if err != nil {
				t.Errorf("failed to delete the test group: %v", err)
			}
		}

		for _, id := range userIDs {
			_, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", id)

			if err != nil {
				t.Errorf("failed to delete the test user: %v", err)
			}
		}
	})

	err := pool.QueryRow(ctx, ` INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext)
								VALUES ($1, 'localhost', 5432, 'test_target', 'test_user', $2) RETURNING id`, name, []byte("dummy")).Scan(&databaseID)

	if err != nil {
		t.Fatalf("failed to create the test connection: %v", err)
	}

	for _, access := range []string{"editor", "reader", "no_access"} {
		var userID int
		err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`, name+"_"+access, "test-only-hash").Scan(&userID)
		if err != nil {
			t.Fatalf("failed to create the test user: %v", err)
		}

		userIDs = append(userIDs, userID)
	}

	// group untuk editor dan pembaca.
	for i, level := range []string{"view_edit", "view_only"} {
		var groupID int
		err := pool.QueryRow(ctx, `INSERT INTO user_groups (name) VALUES ($1) RETURNING id`, name+"_"+level).Scan(&groupID)
		if err != nil {
			t.Fatalf("failed to create the test group: %v", err)
		}

		groupIDs = append(groupIDs, groupID)

		_, err = pool.Exec(ctx, `INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)`, groupID, userIDs[i])

		if err != nil {
			t.Fatalf("failed to add the user to the group: %v", err)
		}

		_, err = pool.Exec(ctx, `INSERT INTO group_database_access (group_id, database_connection_id, access_level) VALUES ($1, $2, $3)`, groupID, databaseID, level)

		if err != nil {
			t.Fatalf("failed to grant group access: %v", err)
		}
	}

	editorID := userIDs[0]
	readerID := userIDs[1]
	noAccessID := userIDs[2]

	// Editor harus dapat membuat draft melalui Service.
	draft, err := service.CreateDraft(ctx, editorID, databaseID, "Draft awal", "SELECT 1;")

	if err != nil {
		t.Fatalf("editor failed to create a draft: %v", err)
	}

	if draft == nil {
		t.Fatal("the editor draft is unavailable")
	}

	if draft.CreatedBy != editorID {
		t.Error("the draft creator does not match the requesting user")
	}

	t.Run("editor_bisa_mengubah_draft", func(t *testing.T) {
		m, err := service.Update(ctx, editorID, draft.ID, "Draft diperbarui", "SELECT 2;")

		if err != nil {
			t.Fatalf("editor failed to update the draft: %v", err)
		}
		if m == nil {
			t.Fatal("hasil update nil")
		}

		if m.Title != "Draft diperbarui" || m.SQL != "SELECT 2;" {
			t.Error("draft changes are incorrect")
		}
	})

	t.Run("editor_dan_reader_can_read", func(t *testing.T) {
		for _, userID := range []int{editorID, readerID} {
			m, err := service.GetByID(ctx, userID, draft.ID)
			if err != nil {
				t.Fatalf("user %d failed to read the details: %v", userID, err)
			}
			if m == nil || m.ID != draft.ID {
				t.Fatal("migration details are incorrect")
			}

			list, err := service.GetByDatabase(ctx, userID, databaseID)
			if err != nil {
				t.Fatalf("user %d failed to read the list: %v", userID, err)
			}
			if len(list) != 1 {
				t.Fatalf("expected 1 migration, got %d", len(list))
			}
			if list[0].ID != draft.ID {
				t.Error("the list contains an incorrect migration")
			}
		}
	})

	t.Run("reader_dan_user_tanpa_akses_cant_write", func(t *testing.T) {
		for _, userID := range []int{readerID, noAccessID} {
			m, err := service.CreateDraft(ctx, userID, databaseID, "Tidak diizinkan", "SELECT 3;")

			if !errors.Is(err, Forbidden) || m != nil {
				t.Fatalf("draft creation by user %d should be rejected: %v", userID, err)
			}

			m, err = service.Update(ctx, userID, draft.ID, "Tidak diizinkan", "SELECT 3;")

			if !errors.Is(err, Forbidden) || m != nil {
				t.Fatalf("update by user %d should be rejected: %v", userID, err)
			}
		}
	})

	t.Run("user_tanpa_akses_cant_read", func(t *testing.T) {
		m, err := service.GetByID(ctx, noAccessID, draft.ID)
		if !errors.Is(err, Forbidden) || m != nil {
			t.Fatalf("reading the details should be rejected: %v", err)
		}

		list, err := service.GetByDatabase(ctx, noAccessID, databaseID)
		if !errors.Is(err, Forbidden) || list != nil {
			t.Fatalf("reading the list should be rejected: %v", err)
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
				t.Fatalf("empty draft creation should be rejected: %v", err)
			}

			m, err = service.Update(ctx, editorID, draft.ID, input.title, input.sql)
			if !errors.Is(err, InvalidInput) || m != nil {
				t.Fatalf("an update with empty input should be rejected: %v", err)
			}
		}
	})

	list, err := store.GetByDatabase(ctx, databaseID)
	if err != nil {
		t.Fatalf("failed to verify the final result: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("there should be only 1 migration, got %d", len(list))
	}

	if list[0].Title != "Draft diperbarui" ||
		list[0].SQL != "SELECT 2;" ||
		list[0].CreatedBy != editorID {
		t.Error("data changed after the request was rejected")
	}
}
