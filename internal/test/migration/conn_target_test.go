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
		t.Fatal("gagal membuat konfigurasi target")
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
		t.Fatal("SSL harus aktif untuk verify-full")
	}
	if config.TLSConfig.InsecureSkipVerify {
		t.Error("sertifikat server harus diperiksa")
	}
	if len(config.Fallbacks) != 0 {
		t.Error("verify-full tidak boleh mencoba pengaturan koneksi lain")
	}
	if strings.Contains(config.ConnString(), password) {
		t.Fatal("alamat koneksi tidak boleh berisi password")
	}

	connection.SSLMode = "disable"
	config, err = targetConfig(connection, password)
	if err != nil {
		t.Fatal("gagal membuat konfigurasi tanpa SSL")
	}
	if config.TLSConfig != nil {
		t.Fatal("SSL seharusnya dimatikan")
	}
	connection.SSLMode = "invalid"
	if _, err := targetConfig(connection, password); !errors.Is(err, InvalidInput) {
		t.Fatal("pengaturan SSL yang salah harus ditolak")
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
		t.Fatal("gagal mengenkripsi password tes")
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
			t.Error("gagal menghapus data koneksi tes")
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM user_groups WHERE id = $1", groupID); err != nil {
			t.Error("gagal menghapus group tes")
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Error("gagal menghapus user tes")
		}
	})
	err = pool.QueryRow(ctx, `INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext, ssl_mode) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, name, config.Host,
		config.Port, config.Database, config.User, encrypted, sslMode).Scan(&databaseID)

	if err != nil {
		t.Fatal("gagal menyiapkan database target untuk tes")
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash)
		VALUES ($1, 'test-only-hash') RETURNING id`, name).Scan(&userID); err != nil {
		t.Fatal("gagal membuat user test")
	}
	if err := pool.QueryRow(ctx, "INSERT INTO user_groups (name) VALUES ($1) RETURNING id", name).Scan(&groupID); err != nil {
		t.Fatal("gagal membuat group test")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO user_group_members (group_id, user_id) VALUES ($1, $2)", groupID, userID); err != nil {
		t.Fatal("gagal menghubungkan user dan group test")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_database_access
		(group_id, database_connection_id, access_level) VALUES ($1, $2, 'view_edit')`, groupID, databaseID); err != nil {
		t.Fatal("gagal memberi akses editor")
	}

	t.Run("editor_membuka_koneksi_terpisah", func(t *testing.T) {
		metadata, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal("gagal mengambil koneksi database aplikasi")
		}
		defer metadata.Release()
		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if err != nil {
			t.Fatalf("gagal terhubung ke database target: %v", err)
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := conn.Close(cleanupCtx); err != nil {
				t.Error("gagal menutup koneksi target")
			}
		})
		var database string
		if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || database != config.Database {
			t.Fatal("koneksi tidak menuju database test yang dipilih")
		}
		if conn.PgConn().PID() == metadata.Conn().PgConn().PID() {
			t.Fatal("koneksi target harus terpisah dari koneksi database aplikasi")
		}
	})

	t.Run("reader_ditolak", func(t *testing.T) {
		_, err := pool.Exec(ctx, "UPDATE group_database_access SET access_level = 'view_only' WHERE database_connection_id = $1", databaseID)
		if err != nil {
			t.Fatal("gagal mengubah akses menjadi reader")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, err := pool.Exec(cleanupCtx, "UPDATE group_database_access SET access_level = 'view_edit' WHERE database_connection_id = $1", databaseID)
			if err != nil {
				t.Error("gagal mengembalikan akses editor")
			}
		})

		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("reader tidak boleh membuka koneksi untuk migration")
		}
		if !errors.Is(err, Forbidden) {
			t.Fatalf("akses reader seharusnya ditolak: %v", err)
		}
	})

	t.Run("koneksi_nonaktif", func(t *testing.T) {
		_, err := pool.Exec(ctx, "UPDATE database_connections SET is_active = false WHERE id = $1", databaseID)
		if err != nil {
			t.Fatal("gagal menonaktifkan koneksi tes")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, err := pool.Exec(cleanupCtx, "UPDATE database_connections SET is_active = true WHERE id = $1", databaseID)
			if err != nil {
				t.Error("gagal mengaktifkan kembali koneksi tes")
			}
		})

		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("koneksi nonaktif tidak boleh dibuka")
		}
		if !errors.Is(err, Forbidden) {
			t.Fatalf("akses ke koneksi nonaktif seharusnya ditolak: %v", err)
		}
	})

	t.Run("dbms_tidak_didukung", func(t *testing.T) {
		_, err := pool.Exec(ctx, "UPDATE database_connections SET db_type = 'mysql' WHERE id = $1", databaseID)
		if err != nil {
			t.Fatal("gagal mengubah jenis database tes")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, err := pool.Exec(cleanupCtx, "UPDATE database_connections SET db_type = 'postgresql' WHERE id = $1", databaseID)
			if err != nil {
				t.Error("gagal mengembalikan jenis database ke PostgreSQL")
			}
		})

		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("migration hanya boleh membuka koneksi PostgreSQL")
		}
		if !errors.Is(err, InvalidInput) {
			t.Fatalf("jenis database yang belum didukung seharusnya ditolak: %v", err)
		}
	})

	t.Run("key_salah", func(t *testing.T) {
		conn, err := service.MakeConnection(ctx, userID, databaseID, strings.Repeat("02", 32))
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("koneksi harus ditolak jika kunci salah")
		}
		if err == nil {
			t.Fatal("kunci yang salah harus ditolak")
		}
	})
	t.Run("tanpa_akses", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DELETE FROM user_group_members WHERE user_id = $1 AND group_id = $2", userID, groupID); err != nil {
			t.Fatal("gagal mencabut keanggotaan group test")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "INSERT INTO user_group_members (user_id, group_id) VALUES ($1, $2)", userID, groupID); err != nil {
				t.Error("gagal mengembalikan keanggotaan group test")
			}
		})
		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil || !errors.Is(err, Forbidden) {
			t.Fatal("user tanpa akses harus ditolak")
		}
	})
	t.Run("target_tidak_tersedia", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "UPDATE database_connections SET database_name = $1 WHERE id = $2", name+"_missing", databaseID); err != nil {
			t.Fatal("gagal menyiapkan tes database yang tidak ada")
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := pool.Exec(cleanupCtx, "UPDATE database_connections SET database_name = $1 WHERE id = $2", config.Database, databaseID); err != nil {
				t.Error("gagal mengembalikan nama database target")
			}
		})
		conn, err := service.MakeConnection(ctx, userID, databaseID, key)
		if conn != nil {
			_ = conn.Close(context.Background())
			t.Fatal("koneksi harus gagal jika database tidak ada")
		}
		if err == nil {
			t.Fatal("database yang tidak ada harus menghasilkan error")
		}
		if strings.Contains(err.Error(), key) || (config.Password != "" && strings.Contains(err.Error(), config.Password)) {
			t.Fatal("pesan error tidak boleh berisi password atau kunci")
		}
	})
	t.Run("context_dibatalkan", func(t *testing.T) {
		canceledCtx, stop := context.WithCancel(ctx)
		stop()
		conn, err := service.MakeConnection(canceledCtx, userID, databaseID, key)
		if conn != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("proses seharusnya dibatalkan: %v", err)
		}
	})
}
