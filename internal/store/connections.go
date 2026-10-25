package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DatabaseConnection struct {
	ID           int64
	Source       string
	CanManage    bool
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

func DecryptSecret(ciphertext []byte, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return "", fmt.Errorf("Encryption key must be 32 bytes encoded as hexadecimal")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("Create decryption cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("Create decryption mode: %w", err)
	}

	if len(ciphertext) < gcm.NonceSize() {
		return "", fmt.Errorf("Encrypted secret is invalid")
	}

	secret, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("Decrypt secret: %w", err)
	}
	return string(secret), nil
}

func PostgresTargetConfig(connection DatabaseConnection, encryptedPassword []byte, hexKey string) (*pgxpool.Config, error) {
	password, err := DecryptSecret(encryptedPassword, hexKey)
	if err != nil {
		return nil, err
	}

	databaseURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(connection.Username),
		url.QueryEscape(password),
		connection.Host,
		connection.Port,
		url.PathEscape(connection.DatabaseName),
		url.QueryEscape(connection.SSLMode),
	)

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("Create target PostgreSQL configuration: %w", err)
	}
	return config, nil
}

func MariaDBTargetDSN(connection DatabaseConnection, encryptedPassword []byte, hexKey string) (string, error) {
	password, err := DecryptSecret(encryptedPassword, hexKey)
	if err != nil {
		return "", err
	}
	config := mysql.NewConfig()
	config.User = connection.Username
	config.Passwd = password
	config.Net = "tcp"
	config.Addr = net.JoinHostPort(connection.Host, strconv.Itoa(connection.Port))
	config.DBName = connection.DatabaseName
	config.TLSConfig = mariaDBTLSConfig(connection.SSLMode)
	config.ParseTime = true
	config.Timeout = 10 * time.Second
	return config.FormatDSN(), nil
}

func ListDatabaseConnections(ctx context.Context, pool *DB) ([]DatabaseConnection, error) {
	rows, err := pool.DB.QueryContext(ctx, pool.Rebind(`
		SELECT id, name, host, port, database_name, username, db_type, ssl_mode, is_active, created_at, updated_at
		FROM database_connections
		ORDER BY updated_at DESC, id DESC
	`))
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

func CountRecords(ctx context.Context, pool *DB, table string) (int, error) {
	var count int
	switch table {
	case "users", "user_groups", "database_connections", "audit_logs":
	default:
		return 0, fmt.Errorf("Unsupported count table %q", table)
	}

	if err := pool.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return 0, fmt.Errorf("Count %s: %w", table, err)
	}

	return count, nil
}

func CreateDatabaseConnection(ctx context.Context, pool *DB, input CreateDatabaseConnectionInput) error {
	_, err := pool.DB.ExecContext(ctx, pool.Rebind(`
		INSERT INTO database_connections (name, host, port, database_name, username, password_ciphertext, db_type, ssl_mode)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`), input.Name, input.Host, input.Port, input.DatabaseName, input.Username,
		input.Password, input.DBType, input.SSLMode)

	if err != nil {
		return fmt.Errorf("Create database connection: %w", err)
	}

	return nil
}

func GetDatabaseConnection(ctx context.Context, pool *DB, id int64) (DatabaseConnection, error) {
	var connection DatabaseConnection
	err := pool.DB.QueryRowContext(ctx, pool.Rebind(`
		SELECT id, name, host, port, database_name, username, db_type, ssl_mode,
		       is_active, created_at, updated_at
		FROM database_connections
		WHERE id = $1
	`), id).Scan(
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
	)
	if err != nil {
		return DatabaseConnection{}, fmt.Errorf("Get database connection: %w", err)
	}
	return connection, nil
}

func DeleteDatabaseConnection(ctx context.Context, pool *DB, id int64) error {
	result, err := pool.DB.ExecContext(ctx, pool.Rebind("DELETE FROM database_connections WHERE id = $1"), id)
	if err != nil {
		return fmt.Errorf("Delete database connection: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Check deleted database connection: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("Database connection %d not found", id)
	}
	return nil
}

func SetDatabaseConnectionActive(ctx context.Context, pool *DB, id int64, isActive bool) error {
	result, err := pool.DB.ExecContext(ctx, pool.Rebind(`
		UPDATE database_connections
		SET is_active = $1
		WHERE id = $2
	`), isActive, id)
	if err != nil {
		return fmt.Errorf("Set database connection status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Check updated database connection status: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("Database connection %d not found", id)
	}
	return nil
}

func UpdateDatabaseConnection(ctx context.Context, pool *DB, id int64, input CreateDatabaseConnectionInput) error {
	result, err := pool.DB.ExecContext(ctx, pool.Rebind(`
		UPDATE database_connections
		SET name = $1, host = $2, port = $3, database_name = $4, username = $5,
		    password_ciphertext = COALESCE($6, password_ciphertext),
		    db_type = $7, ssl_mode = $8
		WHERE id = $9
	`), input.Name, input.Host, input.Port, input.DatabaseName, input.Username,
		input.Password, input.DBType, input.SSLMode, id)
	if err != nil {
		return fmt.Errorf("Update database connection: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Check updated database connection: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("Database connection %d not found", id)
	}
	return nil
}
