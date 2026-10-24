package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

func GetEncryptedPassword(ctx context.Context, pool *pgxpool.Pool, id int64) ([]byte, error) {
	var password []byte
	if err := pool.QueryRow(ctx, "SELECT password_ciphertext FROM database_connections WHERE id = $1", id).Scan(&password); err != nil {
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