package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDBConnection(ctx context.Context, input CreateDatabaseConnectionInput) error {
	if input.DBType == "postgresql" {
		return testPostgreSQLConnection(ctx, input)
	}

	if input.DBType == "mariadb" {
		return testMariaDBConnection(ctx, input)
	}

	return fmt.Errorf("unsupported database type %q", input.DBType)
}

func testPostgreSQLConnection(ctx context.Context, input CreateDatabaseConnectionInput) error {
	databaseURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(input.Username),
		url.QueryEscape(string(input.Password)),
		input.Host,
		input.Port,
		url.PathEscape(input.DatabaseName),
		url.QueryEscape(input.SSLMode),
	)

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("Create PostgreSQL configuration: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("Create PostgreSQL connection: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("PostgreSQL connection failed: %w", err)
	}
	return nil
}

func testMariaDBConnection(ctx context.Context, input CreateDatabaseConnectionInput) error {
	tlsConfig := ""
	if input.SSLMode == "require" || input.SSLMode == "verify-full" {
		tlsConfig = "true"
	}

	config := mysql.Config{
		User: input.Username,
		Passwd: string(input.Password),
		Net: "tcp",
		Addr: fmt.Sprintf("%s:%d", input.Host, input.Port),
		DBName: input.DatabaseName,
		TLSConfig: tlsConfig,
		AllowNativePasswords: true,
	}

	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return fmt.Errorf("Create %s connection: %w", input.DBType, err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("%s connection failed: %w", input.DBType, err)
	}
	return nil
}