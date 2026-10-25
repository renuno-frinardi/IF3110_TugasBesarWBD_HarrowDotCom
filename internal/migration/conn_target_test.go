package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func TestTargetConfig(t *testing.T) {
	connection := store.DatabaseConnection{
		Host:         "localhost",
		Port:         5432,
		DatabaseName: "target /?&=",
		Username:     "user @&=",
		SSLMode:      "verify-full",
	}
	password := "PasswordTes123!"
	config, err := targetConfig(connection, password)
	if err != nil {
		t.Fatal("failed to create the target configuration")
	}
	if config.Host != connection.Host {
		t.Error("host berubah saat membuat konfigurasi")
	}
	if int(config.Port) != connection.Port {
		t.Error("port berubah saat membuat konfigurasi")
	}
	if config.Database != connection.DatabaseName {
		t.Error("nama database berubah saat membuat konfigurasi")
	}
	if config.User != connection.Username {
		t.Error("username berubah saat membuat konfigurasi")
	}
	if config.Password != password {
		t.Error("password berubah saat membuat konfigurasi")
	}

	if config.TLSConfig == nil {
		t.Fatal("SSL must be enabled for verify-full")
	}
	if config.TLSConfig.InsecureSkipVerify {
		t.Error("the server certificate must be verified")
	}
	if len(config.Fallbacks) != 0 {
		t.Error("verify-full must not try other connection settings")
	}
	if strings.Contains(config.ConnString(), password) {
		t.Fatal("the connection string must not contain the password")
	}

	connection.SSLMode = "disable"
	config, err = targetConfig(connection, password)
	if err != nil {
		t.Fatal("failed to create the configuration without SSL")
	}
	if config.TLSConfig != nil {
		t.Fatal("SSL should be disabled")
	}
	connection.SSLMode = "invalid"
	if _, err := targetConfig(connection, password); !errors.Is(err, InvalidInput) {
		t.Fatal("invalid SSL settings must be rejected")
	}
}

func TestMakeConnection(t *testing.T) {
	pool := openTestDatabase(t)
	service := NewService(NewStore(pool), NewDatabaseAccess(pool))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := pool.Config().ConnConfig
	key := strings.Repeat("01", 32)
	encrypted, err := store.EncryptSecret(config.Password, key)
	if err != nil {
		t.Fatal("failed to encrypt the test password")
	}
	sslMode := "disable"
	if config.TLSConfig != nil {
		sslMode = "require"
	}
	name := fmt.Sprintf("target_test_%d", time.Now().UnixNano())
	var databaseID, userID, groupID int
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM database_connections WHERE id = $1", databaseID); err != nil {
			t.Error("failed to delete the test connection data")
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM user_groups WHERE id = $1", groupID); err != nil {
			t.Error("failed to delete the test group")
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Error("failed to delete the test user")
		}
	})
	err = pool.QueryRow(ctx, `INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext, ssl_mode) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, name, config.Host,
		config.Port, config.Database, config.User, encrypted, sslMode).Scan(&databaseID)

	if err != nil {
		t.Fatal("failed to prepare the test target database")
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash)
		VALUES ($1, 'test-only-hash') RETURNING id`, name).Scan(&userID); err != nil {
		t.Fatal("failed to create the test user")
	}
	if err := pool.QueryRow(ctx, "INSERT INTO user_groups (name) VALUES ($1) RETURNING id", name).Scan(&groupID); err != nil {
		t.Fatal("failed to create the test group")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)", groupID, userID); err != nil {
		t.Fatal("failed to link the test user and group")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_database_access
		(group_id, database_connection_id, access_level) VALUES ($1, $2, 'view_edit')`, groupID, databaseID); err != nil {
		t.Fatal("failed to grant editor access")
	}

	t.Run("editor_membuka_koneksi_terpisah", func(t *testing.T) {
		metadata, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal("failed to acquire the application database connection")
		}
		defer metadata.Release()
		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if err != nil {
			t.Fatalf("failed to connect to the target database: %v", err)
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := conn.Close(cleanupCtx); err != nil {
				t.Error("failed to close the target connection")
			}
		})
		var database string
		if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || database != config.Database {
			t.Fatal("the connection does not target the selected test database")
		}
		if conn.PgConn().PID() == metadata.Conn().PgConn().PID() {
			t.Fatal("the target connection must be separate from the application database connection")
		}
	})

	t.Run("reader_ditolak", func(t *testing.T) {
		_, err := pool.Exec(ctx, "UPDATE group_database_access SET access_level = 'view_only' WHERE database_connection_id = $1", databaseID)
		if err != nil {
			t.Fatal("failed to change the access level to reader")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, err := pool.Exec(cleanupCtx, "UPDATE group_database_access SET access_level = 'view_edit' WHERE database_connection_id = $1", databaseID)
			if err != nil {
				t.Error("failed to restore editor access")
			}
		})

		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("reader must not open a connection for migration")
		}
		if !errors.Is(err, Forbidden) {
			t.Fatalf("reader access should be rejected: %v", err)
		}
	})

	t.Run("koneksi_nonaktif", func(t *testing.T) {
		_, err := pool.Exec(ctx, "UPDATE database_connections SET is_active = false WHERE id = $1", databaseID)
		if err != nil {
			t.Fatal("failed to deactivate the test connection")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, err := pool.Exec(cleanupCtx, "UPDATE database_connections SET is_active = true WHERE id = $1", databaseID)
			if err != nil {
				t.Error("failed to reactivate the test connection")
			}
		})

		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("an inactive connection must not be opened")
		}
		if !errors.Is(err, Forbidden) {
			t.Fatalf("access to an inactive connection should be rejected: %v", err)
		}
	})

	t.Run("dbms_tidak_didukung", func(t *testing.T) {
		_, err := pool.Exec(ctx, "UPDATE database_connections SET db_type = 'mysql' WHERE id = $1", databaseID)
		if err != nil {
			t.Fatal("failed to change the test database type")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, err := pool.Exec(cleanupCtx, "UPDATE database_connections SET db_type = 'postgresql' WHERE id = $1", databaseID)
			if err != nil {
				t.Error("failed to restore the database type to PostgreSQL")
			}
		})

		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("migration may only open PostgreSQL connections")
		}
		if !errors.Is(err, InvalidInput) {
			t.Fatalf("an unsupported database type should be rejected: %v", err)
		}
	})

	t.Run("key_salah", func(t *testing.T) {
		conn, err := service.MakeConnection(ctx, userID, databaseID, strings.Repeat("02", 32))
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("the connection must be rejected when the key is invalid")
		}
		if err == nil {
			t.Fatal("the invalid key must be rejected")
		}
	})
	t.Run("tanpa_akses", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DELETE FROM user_group_members WHERE user_id = $1 AND group_id = $2", userID, groupID); err != nil {
			t.Fatal("failed to remove the test group membership")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "INSERT INTO user_group_members (user_id, group_id) VALUES ($1, $2)", userID, groupID); err != nil {
				t.Error("failed to restore test group membership")
			}
		})
		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil || !errors.Is(err, Forbidden) {
			t.Fatal("a user without access must be rejected")
		}
	})
	t.Run("target_tidak_tersedia", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "UPDATE database_connections SET database_name = $1 WHERE id = $2", name+"_missing", databaseID); err != nil {
			t.Fatal("failed to prepare the nonexistent database test")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "UPDATE database_connections SET database_name = $1 WHERE id = $2", config.Database, databaseID); err != nil {
				t.Error("failed to restore the target database name")
			}
		})
		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("the connection must fail when the database does not exist")
		}
		if err == nil {
			t.Fatal("a nonexistent database must produce an error")
		}
		if strings.Contains(err.Error(), key) || (config.Password != "" && strings.Contains(err.Error(), config.Password)) {
			t.Fatal("the error message must not contain the password or key")
		}
	})
	t.Run("context_dibatalkan", func(t *testing.T) {
		canceledCtx, stop := context.WithCancel(ctx)
		stop()
		conn, err := service.MakeConnection(canceledCtx, userID, databaseID, key)
		if conn != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("the process should be canceled: %v", err)
		}
	})
}
