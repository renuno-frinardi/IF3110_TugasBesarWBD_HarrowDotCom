package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DatabaseConnection struct {
	ID           int64
	Name         string
	Host         string
	Port         int
	DatabaseName string
	Username     string
	DBType       string
	SSLMode      string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type CreateDatabaseConnectionInput struct {
	Name         string
	Host         string
	Port         int
	DatabaseName string
	Username     string
	Password     []byte
	DBType       string
	SSLMode      string
}

func EncryptSecret(secret string, hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("Encryption key must be 32 bytes encoded as hexadecimal")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("Create encryption cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("Create encryption mode: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("Generate encryption nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, []byte(secret), nil), nil
}

func DecryptSecret(encrypted []byte, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return "", fmt.Errorf("kunci harus berisi 64 karakter dari 0-9, a-f, atau A-F")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("gagal menyiapkan dekripsi password: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gagal menyiapkan dekripsi password: %w", err)
	}

	if len(encrypted) < gcm.NonceSize()+gcm.Overhead() {
		return "", fmt.Errorf("data password terenkripsi terlalu pendek")
	}

	nonce := encrypted[:gcm.NonceSize()]
	plaintext, err := gcm.Open(nil, nonce, encrypted[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("gagal mendekripsi password: %w", err)
	}

	return string(plaintext), nil
}

func ListDatabaseConnections(ctx context.Context, pool *pgxpool.Pool) ([]DatabaseConnection, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, host, port, database_name, username, db_type, ssl_mode, is_active, created_at, updated_at
		FROM database_connections
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("List database connections: %w", err)
	}
	defer rows.Close()

	connections := make([]DatabaseConnection, 0)
	for rows.Next() {
		var connection DatabaseConnection
		if err := rows.Scan(
			&connection.ID,
			&connection.Name,
			&connection.Host,
			&connection.Port,
			&connection.DatabaseName,
			&connection.Username,
			&connection.DBType,
			&connection.SSLMode,
			&connection.IsActive,
			&connection.CreatedAt,
			&connection.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("Scan database connection: %w", err)
		}
		connections = append(connections, connection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate database connections: %w", err)
	}
	return connections, nil
}

func CountRecords(ctx context.Context, pool *pgxpool.Pool, table string) (int, error) {
	var count int
	switch table {
	case "users", "user_groups", "database_connections", "audit_logs":
	default:
		return 0, fmt.Errorf("Unsupported count table %q", table)
	}

	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return 0, fmt.Errorf("Count %s: %w", table, err)
	}

	return count, nil
}

func CreateDatabaseConnection(ctx context.Context, pool *pgxpool.Pool, input CreateDatabaseConnectionInput) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext, db_type, ssl_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, input.Name, input.Host, input.Port, input.DatabaseName, input.Username,
		input.Password, input.DBType, input.SSLMode)
	
	if err != nil {
		return fmt.Errorf("Create database connection: %w", err)
	}
	
	return nil
}