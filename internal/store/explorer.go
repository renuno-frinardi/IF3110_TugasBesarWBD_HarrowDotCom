package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ExplorerData struct {
	Schemas        []string
	Tables         []string
	SelectedSchema string
	SelectedTable  string
	Columns        []string
	Rows           [][]string
	RowIDs         []string
	Constraints    []TableConstraint
	Indexes        []TableIndex
	Page           int
	HasNextPage    bool
}

type TableConstraint struct {
	Name       string
	Type       string
	Definition string
}

type TableIndex struct {
	Name       string
	Type       string
	Definition string
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
	dsn, err := MariaDBTargetDSN(connection, encryptedPassword, hexKey)
	if err != nil {
		return nil, err
	}
	target, err := sql.Open("mysql", dsn)
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

func ListMariaDBConstraints(ctx context.Context, target *sql.DB, schema, table string) ([]TableConstraint, error) {
	rows, err := target.QueryContext(ctx, `
		SELECT tc.CONSTRAINT_NAME, tc.CONSTRAINT_TYPE,
		       COALESCE(GROUP_CONCAT(kcu.COLUMN_NAME ORDER BY kcu.ORDINAL_POSITION SEPARATOR ', '), '')
		FROM information_schema.TABLE_CONSTRAINTS tc
		LEFT JOIN information_schema.KEY_COLUMN_USAGE kcu
		  ON kcu.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
		 AND kcu.TABLE_SCHEMA = tc.TABLE_SCHEMA
		 AND kcu.TABLE_NAME = tc.TABLE_NAME
		 AND kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
		WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ?
		GROUP BY tc.CONSTRAINT_NAME, tc.CONSTRAINT_TYPE
		ORDER BY tc.CONSTRAINT_NAME
	`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("List MariaDB constraints: %w", err)
	}
	defer rows.Close()

	constraints := make([]TableConstraint, 0)
	for rows.Next() {
		var constraint TableConstraint
		if err := rows.Scan(&constraint.Name, &constraint.Type, &constraint.Definition); err != nil {
			return nil, fmt.Errorf("Scan MariaDB constraint: %w", err)
		}
		constraints = append(constraints, constraint)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Read MariaDB constraints: %w", err)
	}
	return constraints, nil
}

func ListMariaDBIndexes(ctx context.Context, target *sql.DB, schema, table string) ([]TableIndex, error) {
	rows, err := target.QueryContext(ctx, `
		SELECT INDEX_NAME,
		       CASE WHEN INDEX_NAME = 'PRIMARY' THEN 'PRIMARY KEY'
		            WHEN NON_UNIQUE = 0 THEN 'UNIQUE'
		            ELSE 'INDEX' END,
		       GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR ', ')
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		GROUP BY INDEX_NAME, NON_UNIQUE
		ORDER BY INDEX_NAME
	`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("List MariaDB indexes: %w", err)
	}
	defer rows.Close()

	indexes := make([]TableIndex, 0)
	for rows.Next() {
		var index TableIndex
		if err := rows.Scan(&index.Name, &index.Type, &index.Definition); err != nil {
			return nil, fmt.Errorf("Scan MariaDB index: %w", err)
		}
		indexes = append(indexes, index)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Read MariaDB indexes: %w", err)
	}
	return indexes, nil
}

func ReadMariaDBTable(ctx context.Context, target *sql.DB, schema, table string, page int) ([]string, [][]string, bool, error) {
	if page < 1 {
		page = 1
	}

	keys, err := target.QueryContext(ctx, `SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND CONSTRAINT_NAME = 'PRIMARY'
		ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, nil, false, err
	}
	primaryKey, err := scanSingleStringColumn(keys, "primary key")
	keys.Close()
	if err != nil {
		return nil, nil, false, err
	}
	orderBy := ""
	for i := range primaryKey {
		primaryKey[i] = quoteMariaDBIdentifier(primaryKey[i])
	}
	if len(primaryKey) > 0 {
		orderBy = " ORDER BY " + strings.Join(primaryKey, ", ")
	}

	const pageSize = 50
	rows, err := target.QueryContext(ctx, fmt.Sprintf(
		"SELECT * FROM %s.%s%s LIMIT %d OFFSET %d",
		quoteMariaDBIdentifier(schema), quoteMariaDBIdentifier(table), orderBy, pageSize+1, (page-1)*pageSize,
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

func ListPostgresConstraints(ctx context.Context, target *pgxpool.Pool, schema, table string) ([]TableConstraint, error) {
	rows, err := target.Query(ctx, `
		SELECT con.conname,
		       CASE con.contype
		         WHEN 'p' THEN 'PRIMARY KEY'
		         WHEN 'f' THEN 'FOREIGN KEY'
		         WHEN 'u' THEN 'UNIQUE'
		         WHEN 'c' THEN 'CHECK'
		         WHEN 'x' THEN 'EXCLUSION'
		         WHEN 'n' THEN 'NOT NULL'
		         ELSE con.contype::text
		       END,
		       pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class rel ON rel.oid = con.conrelid
		JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
		WHERE nsp.nspname = $1 AND rel.relname = $2
		ORDER BY con.conname
	`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("List target constraints: %w", err)
	}
	defer rows.Close()

	constraints := make([]TableConstraint, 0)
	for rows.Next() {
		var constraint TableConstraint
		if err := rows.Scan(&constraint.Name, &constraint.Type, &constraint.Definition); err != nil {
			return nil, fmt.Errorf("Scan target constraint: %w", err)
		}
		constraints = append(constraints, constraint)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Read target constraints: %w", err)
	}
	return constraints, nil
}

func ListPostgresIndexes(ctx context.Context, target *pgxpool.Pool, schema, table string) ([]TableIndex, error) {
	rows, err := target.Query(ctx, `
		SELECT indexname, 'INDEX', indexdef
		FROM pg_indexes
		WHERE schemaname = $1 AND tablename = $2
		ORDER BY indexname
	`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("List target indexes: %w", err)
	}
	defer rows.Close()

	indexes := make([]TableIndex, 0)
	for rows.Next() {
		var index TableIndex
		if err := rows.Scan(&index.Name, &index.Type, &index.Definition); err != nil {
			return nil, fmt.Errorf("Scan target index: %w", err)
		}
		indexes = append(indexes, index)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Read target indexes: %w", err)
	}
	return indexes, nil
}

func formatPostgresValue(value any) string {
	if value == nil {
		return "NULL"
	}
	switch v := value.(type) {
	case time.Time:
		return v.Format("2006-01-02 15:04:05.999999Z07:00")
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
		return fmt.Sprintf("\\x%x", v)
	default:
		return fmt.Sprint(value)
	}
}

func ReadPostgresTable(ctx context.Context, target *pgxpool.Pool, schema, table string, page int) (columns []string, values [][]string, hasNext bool, err error) {
	columns, values, _, hasNext, err = ReadPostgresTableWithRowIDs(ctx, target, schema, table, page)
	return columns, values, hasNext, err
}

func ReadPostgresTableWithRowIDs(ctx context.Context, target *pgxpool.Pool, schema, table string, page int) (columns []string, values [][]string, rowIDs []string, hasNext bool, err error) {
	if page < 1 {
		page = 1
	}

	keys, err := target.Query(ctx, `SELECT a.attname
		FROM pg_index i
		JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum, position) ON true
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE n.nspname = $1 AND t.relname = $2 AND i.indisprimary AND k.position <= i.indnkeyatts
		ORDER BY k.position`, schema, table)
	if err != nil {
		return nil, nil, nil, false, err
	}
	var primaryKey []string
	for keys.Next() {
		var column string
		if err := keys.Scan(&column); err != nil {
			keys.Close()
			return nil, nil, nil, false, err
		}
		primaryKey = append(primaryKey, quoteIdentifier(column))
	}
	keys.Close()
	if err := keys.Err(); err != nil {
		return nil, nil, nil, false, err
	}
	orderBy := "ctid"
	if len(primaryKey) > 0 {
		orderBy = strings.Join(primaryKey, ", ")
	}

	const pageSize = 50
	rows, err := target.Query(ctx, fmt.Sprintf(
		"SELECT ctid::text, * FROM %s.%s ORDER BY %s LIMIT %d OFFSET %d",
		quoteIdentifier(schema), quoteIdentifier(table), orderBy, pageSize+1, (page-1)*pageSize,
	))

	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("Read target table: %w", err)
	}
	defer rows.Close()

	fieldDescriptions := rows.FieldDescriptions()
	for i, field := range fieldDescriptions {
		if i == 0 {
			continue // skip ctid::text column
		}
		columns = append(columns, string(field.Name))
	}

	for rows.Next() {
		raw, err := rows.Values()
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("Read target row: %w", err)
		}

		if len(values) == pageSize {
			hasNext = true
			break
		}

		if len(raw) > 0 {
			rowIDs = append(rowIDs, fmt.Sprint(raw[0]))
			raw = raw[1:]
		}

		row := make([]string, len(raw))
		for i, value := range raw {
			row[i] = formatPostgresValue(value)
		}
		values = append(values, row)
	}
	return columns, values, rowIDs, hasNext, rows.Err()
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

func postgresColumnUDTMap(ctx context.Context, target *pgxpool.Pool, schema, table string) (map[string]string, error) {
	rows, err := target.Query(ctx, `
		SELECT column_name, udt_name
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
	`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var col, udt string
		if err := rows.Scan(&col, &udt); err != nil {
			return nil, err
		}
		result[col] = udt
	}
	return result, rows.Err()
}

func isSafeUDTName(udt string) bool {
	if udt == "" {
		return false
	}
	for _, r := range udt {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func InsertPostgresRow(ctx context.Context, target *pgxpool.Pool, schema, table string, columns []string, inputValues map[string]string) error {
	udtMap, err := postgresColumnUDTMap(ctx, target, schema, table)
	if err != nil {
		return fmt.Errorf("read column types: %w", err)
	}

	var insertCols []string
	var placeholders []string
	var args []any
	argIdx := 1

	for _, col := range columns {
		rawVal, exists := inputValues[col]
		if !exists || strings.TrimSpace(rawVal) == "" {
			continue
		}
		insertCols = append(insertCols, quoteIdentifier(col))
		if strings.EqualFold(strings.TrimSpace(rawVal), "NULL") {
			placeholders = append(placeholders, "NULL")
			continue
		}
		udt := udtMap[col]
		if isSafeUDTName(udt) {
			placeholders = append(placeholders, fmt.Sprintf("$%d::text::%s", argIdx, quoteIdentifier(udt)))
		} else {
			placeholders = append(placeholders, fmt.Sprintf("$%d::text", argIdx))
		}
		args = append(args, rawVal)
		argIdx++
	}

	var query string
	if len(insertCols) == 0 {
		query = fmt.Sprintf("INSERT INTO %s.%s DEFAULT VALUES", quoteIdentifier(schema), quoteIdentifier(table))
	} else {
		query = fmt.Sprintf(
			"INSERT INTO %s.%s (%s) VALUES (%s)",
			quoteIdentifier(schema),
			quoteIdentifier(table),
			strings.Join(insertCols, ", "),
			strings.Join(placeholders, ", "),
		)
	}

	_, err = target.Exec(ctx, query, args...)
	return err
}

func UpdatePostgresRow(ctx context.Context, target *pgxpool.Pool, schema, table string, columns []string, rowCTID string, oldValues, newValues []string) error {
	if len(columns) == 0 || len(oldValues) != len(columns) || len(newValues) != len(columns) {
		return fmt.Errorf("column count mismatch")
	}

	udtMap, err := postgresColumnUDTMap(ctx, target, schema, table)
	if err != nil {
		return fmt.Errorf("read column types: %w", err)
	}

	var setClauses []string
	var args []any
	argIdx := 1

	for i, col := range columns {
		if oldValues[i] == newValues[i] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(newValues[i]), "NULL") {
			setClauses = append(setClauses, fmt.Sprintf("%s = NULL", quoteIdentifier(col)))
			continue
		}
		udt := udtMap[col]
		if isSafeUDTName(udt) {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d::text::%s", quoteIdentifier(col), argIdx, quoteIdentifier(udt)))
		} else {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d::text", quoteIdentifier(col), argIdx))
		}
		args = append(args, newValues[i])
		argIdx++
	}

	if len(setClauses) == 0 {
		return nil
	}

	var query string
	if strings.TrimSpace(rowCTID) != "" {
		args = append(args, strings.TrimSpace(rowCTID))
		query = fmt.Sprintf(
			"UPDATE %s.%s SET %s WHERE ctid = $%d::text::tid",
			quoteIdentifier(schema),
			quoteIdentifier(table),
			strings.Join(setClauses, ", "),
			argIdx,
		)
	} else {
		var whereClauses []string
		for i, col := range columns {
			if oldValues[i] == "NULL" {
				whereClauses = append(whereClauses, fmt.Sprintf("%s IS NULL", quoteIdentifier(col)))
			} else {
				whereClauses = append(whereClauses, fmt.Sprintf("%s::text = $%d::text", quoteIdentifier(col), argIdx))
				args = append(args, oldValues[i])
				argIdx++
			}
		}
		query = fmt.Sprintf(
			"UPDATE %s.%s SET %s WHERE ctid = (SELECT ctid FROM %s.%s WHERE %s LIMIT 1)",
			quoteIdentifier(schema),
			quoteIdentifier(table),
			strings.Join(setClauses, ", "),
			quoteIdentifier(schema),
			quoteIdentifier(table),
			strings.Join(whereClauses, " AND "),
		)
	}

	tag, err := target.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no matching row found to update")
	}
	return nil
}

func DeletePostgresRow(ctx context.Context, target *pgxpool.Pool, schema, table string, columns []string, rowCTID string, oldValues []string) error {
	if len(columns) == 0 || len(oldValues) != len(columns) {
		return fmt.Errorf("column count mismatch")
	}

	var query string
	var args []any
	if strings.TrimSpace(rowCTID) != "" {
		args = append(args, strings.TrimSpace(rowCTID))
		query = fmt.Sprintf(
			"DELETE FROM %s.%s WHERE ctid = $1::text::tid",
			quoteIdentifier(schema),
			quoteIdentifier(table),
		)
	} else {
		var whereClauses []string
		argIdx := 1
		for i, col := range columns {
			if oldValues[i] == "NULL" {
				whereClauses = append(whereClauses, fmt.Sprintf("%s IS NULL", quoteIdentifier(col)))
			} else {
				whereClauses = append(whereClauses, fmt.Sprintf("%s::text = $%d::text", quoteIdentifier(col), argIdx))
				args = append(args, oldValues[i])
				argIdx++
			}
		}
		query = fmt.Sprintf(
			"DELETE FROM %s.%s WHERE ctid = (SELECT ctid FROM %s.%s WHERE %s LIMIT 1)",
			quoteIdentifier(schema),
			quoteIdentifier(table),
			quoteIdentifier(schema),
			quoteIdentifier(table),
			strings.Join(whereClauses, " AND "),
		)
	}

	tag, err := target.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("no matching row found to delete")
	}
	return nil
}

func InsertMariaDBRow(ctx context.Context, target *sql.DB, schema, table string, columns []string, inputValues map[string]string) error {
	var insertCols []string
	var placeholders []string
	var args []any

	for _, col := range columns {
		rawVal, exists := inputValues[col]
		if !exists || strings.TrimSpace(rawVal) == "" {
			continue
		}
		insertCols = append(insertCols, quoteMariaDBIdentifier(col))
		if strings.EqualFold(strings.TrimSpace(rawVal), "NULL") {
			placeholders = append(placeholders, "NULL")
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, rawVal)
	}

	var query string
	if len(insertCols) == 0 {
		query = fmt.Sprintf("INSERT INTO %s.%s () VALUES ()", quoteMariaDBIdentifier(schema), quoteMariaDBIdentifier(table))
	} else {
		query = fmt.Sprintf(
			"INSERT INTO %s.%s (%s) VALUES (%s)",
			quoteMariaDBIdentifier(schema),
			quoteMariaDBIdentifier(table),
			strings.Join(insertCols, ", "),
			strings.Join(placeholders, ", "),
		)
	}

	_, err := target.ExecContext(ctx, query, args...)
	return err
}

func UpdateMariaDBRow(ctx context.Context, target *sql.DB, schema, table string, columns []string, oldValues, newValues []string) error {
	if len(columns) == 0 || len(oldValues) != len(columns) || len(newValues) != len(columns) {
		return fmt.Errorf("column count mismatch")
	}

	var setClauses []string
	var args []any

	for i, col := range columns {
		if oldValues[i] == newValues[i] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(newValues[i]), "NULL") {
			setClauses = append(setClauses, fmt.Sprintf("%s = NULL", quoteMariaDBIdentifier(col)))
			continue
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", quoteMariaDBIdentifier(col)))
		args = append(args, newValues[i])
	}

	if len(setClauses) == 0 {
		return nil
	}

	var whereClauses []string
	for i, col := range columns {
		if oldValues[i] == "NULL" {
			whereClauses = append(whereClauses, fmt.Sprintf("%s IS NULL", quoteMariaDBIdentifier(col)))
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("CAST(%s AS CHAR) = ?", quoteMariaDBIdentifier(col)))
			args = append(args, oldValues[i])
		}
	}

	query := fmt.Sprintf(
		"UPDATE %s.%s SET %s WHERE %s LIMIT 1",
		quoteMariaDBIdentifier(schema),
		quoteMariaDBIdentifier(table),
		strings.Join(setClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)

	_, err := target.ExecContext(ctx, query, args...)
	return err
}

func DeleteMariaDBRow(ctx context.Context, target *sql.DB, schema, table string, columns []string, oldValues []string) error {
	if len(columns) == 0 || len(oldValues) != len(columns) {
		return fmt.Errorf("column count mismatch")
	}

	var whereClauses []string
	var args []any

	for i, col := range columns {
		if oldValues[i] == "NULL" {
			whereClauses = append(whereClauses, fmt.Sprintf("%s IS NULL", quoteMariaDBIdentifier(col)))
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("CAST(%s AS CHAR) = ?", quoteMariaDBIdentifier(col)))
			args = append(args, oldValues[i])
		}
	}

	query := fmt.Sprintf(
		"DELETE FROM %s.%s WHERE %s LIMIT 1",
		quoteMariaDBIdentifier(schema),
		quoteMariaDBIdentifier(table),
		strings.Join(whereClauses, " AND "),
	)

	_, err := target.ExecContext(ctx, query, args...)
	return err
}
