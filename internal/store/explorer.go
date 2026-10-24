package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ExplorerData struct {
	Schemas        []string
	Tables         []string
	SelectedSchema string
	SelectedTable  string
	Columns        []string
	Rows           [][]string
	Page           int
	HasNextPage    bool
}

func GetEncryptedPassword(ctx context.Context, pool *DB, id int64) ([]byte, error) {
	var password []byte
	if err := pool.DB.QueryRowContext(ctx, pool.Rebind("SELECT password_ciphertext FROM database_connections WHERE id = $1"), id).Scan(&password); err != nil {
		return nil, fmt.Errorf("get connection password: %w", err)
	}
	return password, nil
}

func ListPostgresSchemas(ctx context.Context, target *pgxpool.Pool) ([]string, error) {
	rows, err := target.Query(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('information_schema', 'pg_catalog')
		  AND schema_name NOT LIKE 'pg_%'
		ORDER BY schema_name
	`)
	if err != nil {
		return nil, fmt.Errorf("List target schemas: %w", err)
	}

	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			return nil, fmt.Errorf("Scan target schema: %w", err)
		}
		schemas = append(schemas, schema)
	}
	return schemas, rows.Err()
}

func OpenMariaDBTarget(ctx context.Context, connection DatabaseConnection, encryptedPassword []byte, hexKey string) (*sql.DB, error) {
	password, err := DecryptSecret(encryptedPassword, hexKey)
	if err != nil {
		return nil, err
	}

	config := mysql.Config{
		User:                 connection.Username,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%d", connection.Host, connection.Port),
		DBName:               connection.DatabaseName,
		TLSConfig:            mariaDBTLSConfig(connection.SSLMode),
		AllowNativePasswords: true,
	}

	target, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("Create target MariaDB connection: %w", err)
	}

	if err := target.PingContext(ctx); err != nil {
		target.Close()
		return nil, fmt.Errorf("Ping target MariaDB: %w", err)
	}
	return target, nil
}

func ListMariaDBSchemas(ctx context.Context, target *sql.DB) ([]string, error) {
	rows, err := target.QueryContext(ctx, `
		SELECT SCHEMA_NAME
		FROM information_schema.SCHEMATA
		WHERE SCHEMA_NAME NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
		ORDER BY SCHEMA_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("List target MariaDB schemas: %w", err)
	}
	defer rows.Close()

	return scanSingleStringColumn(rows, "MariaDB schema")
}

func ListMariaDBTables(ctx context.Context, target *sql.DB, schema string) ([]string, error) {
	rows, err := target.QueryContext(ctx, `
		SELECT TABLE_NAME
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME
	`, schema)
	if err != nil {
		return nil, fmt.Errorf("List target MariaDB tables: %w", err)
	}
	defer rows.Close()

	return scanSingleStringColumn(rows, "MariaDB table")
}

func ReadMariaDBTable(ctx context.Context, target *sql.DB, schema, table string, page int) ([]string, [][]string, bool, error) {
	if page < 1 {
		page = 1
	}

	const pageSize = 50
	rows, err := target.QueryContext(ctx, fmt.Sprintf(
		"SELECT * FROM %s.%s LIMIT %d OFFSET %d",
		quoteMariaDBIdentifier(schema), quoteMariaDBIdentifier(table), pageSize+1, (page-1)*pageSize,
	))

	if err != nil {
		return nil, nil, false, fmt.Errorf("Read target MariaDB table: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, false, fmt.Errorf("Read target MariaDB columns: %w", err)
	}

	values := make([][]string, 0)
	for rows.Next() {
		raw := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}

		if err := rows.Scan(dest...); err != nil {
			return nil, nil, false, fmt.Errorf("Read target MariaDB row: %w", err)
		}

		if len(values) == pageSize {
			return columns, values, true, nil
		}

		row := make([]string, len(raw))
		for i, value := range raw {
			if value == nil {
				row[i] = "NULL"
			} else if bytes, ok := value.([]byte); ok {
				row[i] = string(bytes)
			} else {
				row[i] = fmt.Sprint(value)
			}
		}
		values = append(values, row)
	}
	return columns, values, false, rows.Err()
}

func scanSingleStringColumn(rows *sql.Rows, label string) ([]string, error) {
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("Scan %s: %w", label, err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func quoteMariaDBIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func mariaDBTLSConfig(sslMode string) string {
	if sslMode == "require" || sslMode == "verify-full" {
		return "true"
	}
	return ""
}

func ListPostgresTables(ctx context.Context, target *pgxpool.Pool, schema string) ([]string, error) {
	rows, err := target.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`, schema)
	if err != nil {
		return nil, fmt.Errorf("List target tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("Scan target table: %w", err)
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func ReadPostgresTable(ctx context.Context, target *pgxpool.Pool, schema, table string, page int) (columns []string, values [][]string, hasNext bool, err error) {
	if page < 1 {
		page = 1
	}

	const pageSize = 50
	rows, err := target.Query(ctx, fmt.Sprintf(
		"SELECT * FROM %s.%s LIMIT %d OFFSET %d",
		quoteIdentifier(schema), quoteIdentifier(table), pageSize+1, (page-1)*pageSize,
	))

	if err != nil {
		return nil, nil, false, fmt.Errorf("Read target table: %w", err)
	}
	defer rows.Close()

	fieldDescriptions := rows.FieldDescriptions()
	for _, field := range fieldDescriptions {
		columns = append(columns, string(field.Name))
	}

	for rows.Next() {
		raw, err := rows.Values()
		if err != nil {
			return nil, nil, false, fmt.Errorf("Read target row: %w", err)
		}

		if len(values) == pageSize {
			hasNext = true
			break
		}

		row := make([]string, len(raw))
		for i, value := range raw {
			if value == nil {
				row[i] = "NULL"
			} else {
				row[i] = fmt.Sprint(value)
			}
		}
		values = append(values, row)
	}
	return columns, values, hasNext, rows.Err()
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func ParsePageNumber(value string) int {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 {
		return 1
	}
	return page
}
