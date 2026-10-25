package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type DB struct {
	DB     *sql.DB
	Driver string
}

func OpenDB(ctx context.Context, driver, databaseURL string) (*DB, error) {
	var sqlDriver string
	switch driver {
	case "postgresql", "postgres":
		sqlDriver = "pgx"
	case "mariadb":
		sqlDriver = "mysql"
	default:
		return nil, fmt.Errorf("unsupported internal database driver %q", driver)
	}

	db, err := sql.Open(sqlDriver, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open internal database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping internal database: %w", err)
	}

	return &DB{DB: db, Driver: driver}, nil
}

func (db *DB) Close() error {
	return db.DB.Close()
}

func (db *DB) Rebind(query string) string {
	if db.Driver != "mariadb" {
		return query
	}

	var builder strings.Builder
	index := 0
	for _, character := range query {
		if character == '$' {
			continue
		}
		if character >= '1' && character <= '9' && index >= 0 {
			builder.WriteByte('?')
			index++
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func MariaDBAppURL(host string, port int, database, user, password string) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?multiStatements=true&parseTime=true", url.QueryEscape(user), url.QueryEscape(password), host, port, database)
}

func PostgresAppURL(host string, port int, database, user, password string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		url.QueryEscape(user),
		url.QueryEscape(password),
		host,
		port,
		url.PathEscape(database),
	)
}