package explain

// Import pkg
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pganalyze/pg_query_go/v6"
)

const NotApplicableMessage = "Not applicable"

var ErrEmptyQuery = errors.New("SQL query cannot be empty")
var ErrMultipleStatements = errors.New("EXPLAIN only supports one SQL statement")

// Menyimpan hasil statement SQL
type Result struct {
	SQL           string
	Analyze       bool
	NotApplicable bool
	Message       string
	PlanLines     []string
}

// Mengembalikan apakah statement SQL adalah pure DDL
func IsPureDDL(sqlText string) bool {
	kinds, ok := statementKinds(sqlText)
	if !ok || len(kinds) != 1 {
		return false
	}
	switch kinds[0] {
	case "CreateTableAsStmt", "SelectStmt", "InsertStmt", "UpdateStmt", "DeleteStmt", "MergeStmt", "CallStmt", "PrepareStmt", "ExecuteStmt":
		return false
	default:
		return true
	}
}

func statementKinds(sqlText string) ([]string, bool) {
	parsed, err := pg_query.ParseToJSON(sqlText)
	if err != nil {
		return nil, false
	}
	var result struct {
		Statements []struct {
			Statement map[string]json.RawMessage `json:"stmt"`
		} `json:"stmts"`
	}
	if err := json.Unmarshal([]byte(parsed), &result); err != nil {
		return nil, false
	}
	kinds := make([]string, 0, len(result.Statements))
	for _, statement := range result.Statements {
		if len(statement.Statement) != 1 {
			return nil, false
		}
		for kind := range statement.Statement {
			kinds = append(kinds, kind)
		}
	}
	return kinds, true
}

// Menjalankan EXPLAIN atau EXPLAIN ANALYZE terhadap suatu database postgres
func ExplainPostgres(ctx context.Context, target *pgxpool.Pool, sqlText string, analyze bool) (*Result, error) {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return nil, ErrEmptyQuery
	}

	kinds, ok := statementKinds(trimmed)
	if !ok {
		return nil, fmt.Errorf("Parse SQL for explain: invalid SQL")
	}
	if len(kinds) != 1 {
		return nil, ErrMultipleStatements
	}

	if IsPureDDL(trimmed) {
		return &Result{
			SQL:           trimmed,
			Analyze:       analyze,
			NotApplicable: true,
			Message:       NotApplicableMessage,
			PlanLines:     []string{NotApplicableMessage},
		}, nil
	}

	if target == nil {
		return nil, errors.New("Target database connection is nil")
	}

	tx, err := target.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("Begin explain transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	prefix := "EXPLAIN (COSTS TRUE, FORMAT TEXT) "
	if analyze {
		prefix = "EXPLAIN (ANALYZE TRUE, COSTS TRUE, TIMING TRUE, FORMAT TEXT) "
	}

	rows, err := tx.Query(ctx, prefix+trimmed)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("execute explain: %w", err)
	}

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return nil, fmt.Errorf("scan explain line: %w", err)
		}
		lines = append(lines, line)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("Iterate explain rows: %w", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		return nil, fmt.Errorf("Rollback explain transaction: %w", err)
	}

	return &Result{
		SQL:           trimmed,
		Analyze:       analyze,
		NotApplicable: false,
		Message:       strings.Join(lines, "\n"),
		PlanLines:     lines,
	}, nil
}
