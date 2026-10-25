package migration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func (s *Service) MakeConnection(ctx context.Context, userID, databaseID int, hexKey string) (*pgx.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := s.checkAccess(ctx, userID, databaseID, true); err != nil {
		return nil, err
	}
	if s.store == nil || s.store.pool == nil {
		return nil, errors.New("The application database connection is not ready")
	}

	var connection store.DatabaseConnection
	var encrypted []byte

	query := `SELECT host, port, database_name, username, password_ciphertext, db_type, ssl_mode
		FROM database_connections
		WHERE id = $1 AND is_active`
	err := s.store.pool.QueryRow(ctx, query, databaseID).Scan(&connection.Host, &connection.Port, &connection.DatabaseName,
		&connection.Username, &encrypted, &connection.DBType, &connection.SSLMode)
	if err != nil {
		return nil, fmt.Errorf("Failed to load the target database configuration: %w", err)
	}

	if connection.DBType != "postgresql" {
		return nil, fmt.Errorf("%w: migration only supports PostgreSQL", InvalidInput)
	}

	password, err := store.DecryptSecret(encrypted, hexKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to decrypt the target database password: %w", err)
	}

	config, err := targetConfig(connection, password)
	if err != nil {
		return nil, err
	}

	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Failed to open the target database connection: %w", ctx.Err())
		}
		return nil, errors.New("Failed to connect to the target database, check the address, username, and password")
	}
	return conn, nil
}

func targetConfig(connection store.DatabaseConnection, password string) (*pgx.ConnConfig, error) {
	params := url.Values{}
	params.Set("host", connection.Host)
	params.Set("port", strconv.Itoa(connection.Port))
	params.Set("dbname", connection.DatabaseName)
	params.Set("user", connection.Username)
	params.Set("sslmode", connection.SSLMode)
	params.Set("connect_timeout", "10")

	// Parser koneksi PostgreSQL baca space nya %20.
	query := strings.ReplaceAll(params.Encode(), "+", "%20")
	config, err := pgx.ParseConfig("postgresql://?" + query)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid target database connection settings", InvalidInput)
	}

	config.Password = password
	return config, nil
}

func TargetConfig(connection store.DatabaseConnection, password string) (*pgx.ConnConfig, error) {
	return targetConfig(connection, password)
}
