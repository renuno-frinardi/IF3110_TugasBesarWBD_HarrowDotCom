package explain

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func ExplainMariaDB(ctx context.Context, target *sql.DB, sqlText string, analyze bool) (*Result, error) {
	statements, err := store.MariaDBStatements(sqlText)
	if err != nil {
		return nil, err
	}
	if len(statements) != 1 {
		return nil, ErrMultipleStatements
	}
	statement := statements[0]
	switch strings.ToUpper(strings.Fields(statement)[0]) {
	case "SELECT", "UPDATE", "DELETE":
	case "INSERT", "CREATE", "ALTER", "DROP", "TRUNCATE":
		message := "EXPLAIN MariaDB mendukung SELECT, UPDATE, dan DELETE."
		return &Result{SQL: sqlText, Analyze: analyze, NotApplicable: true, Message: message, PlanLines: []string{message}}, nil
	default:
		return nil, errors.New("EXPLAIN MariaDB mendukung SELECT, UPDATE, dan DELETE")
	}
	if target == nil {
		return nil, errors.New("koneksi MariaDB belum siap")
	}
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	prefix := "EXPLAIN "
	if analyze {
		prefix = "ANALYZE "
	}
	rows, err := tx.QueryContext(ctx, prefix+statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var lines []string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		var parts []string
		for i, value := range values {
			text := value.String
			if !value.Valid {
				text = "NULL"
			}
			parts = append(parts, columns[i]+"="+text)
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := tx.Rollback(); err != nil {
		return nil, err
	}
	return &Result{SQL: sqlText, Analyze: analyze, Message: strings.Join(lines, "\n"), PlanLines: lines}, nil
}
