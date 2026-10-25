package explain

// Import pkg
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// Menghilangkan komen SQL
func stripSQLComments(sqlText string) string {
	lines := strings.Split(sqlText, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		kept = append(kept, line)
	}
	res := strings.Join(kept, "\n")
	for {
		start := strings.Index(res, "/*")
		if start == -1 {
			break
		}
		end := strings.Index(res[start+2:], "*/")
		if end == -1 {
			res = res[:start]
			break
		}
		res = res[:start] + " " + res[start+2+end+2:]
	}
	return res
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

// Melakukan eksekusi transaksional dari suatu statement SQL
func ExecuteMigrationTransactional(ctx context.Context, platformDB *sql.DB, target *pgxpool.Pool, migrationID int, actorUserID int64) error {
	var dbID int
	var sqlText, status string
	err := platformDB.QueryRowContext(ctx, `
		SELECT database_id, sql, status FROM migrations WHERE id = $1
	`, migrationID).Scan(&dbID, &sqlText, &status)
	if err != nil {
		return fmt.Errorf("lookup migration %d: %w", migrationID, err)
	}
	if status != "draft" && status != "failed" {
		return fmt.Errorf("migration %d cannot be executed from status %q", migrationID, status)
	}

	startTime := time.Now().UTC()
	_, _ = platformDB.ExecContext(ctx, `
		UPDATE migrations
		SET status = 'running', executed_by = $1, start_time = $2, updated_at = NOW(), error_message = NULL
		WHERE id = $3
	`, actorUserID, startTime, migrationID)

	tx, err := target.Begin(ctx)
	if err != nil {
		finishTime := time.Now().UTC()
		errMsg := err.Error()
		_, _ = platformDB.ExecContext(ctx, `
			UPDATE migrations
			SET status = 'failed', finish_time = $1, updated_at = NOW(), error_message = $2
			WHERE id = $3
		`, finishTime, errMsg, migrationID)
		return fmt.Errorf("begin target transaction: %w", err)
	}

	if _, execErr := tx.Exec(ctx, sqlText); execErr != nil {
		_ = tx.Rollback(ctx)
		finishTime := time.Now().UTC()
		errMsg := execErr.Error()
		_, _ = platformDB.ExecContext(ctx, `
			UPDATE migrations
			SET status = 'failed', finish_time = $1, updated_at = NOW(), error_message = $2
			WHERE id = $3
		`, finishTime, errMsg, migrationID)
		return fmt.Errorf("migration execution failed (rolled back): %w", execErr)
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		_ = tx.Rollback(ctx)
		finishTime := time.Now().UTC()
		errMsg := commitErr.Error()
		_, _ = platformDB.ExecContext(ctx, `
			UPDATE migrations
			SET status = 'failed', finish_time = $1, updated_at = NOW(), error_message = $2
			WHERE id = $3
		`, commitTimeOrNow(finishTime), errMsg, migrationID)
		return fmt.Errorf("commit target transaction: %w", commitErr)
	}

	finishTime := time.Now().UTC()
	_, err = platformDB.ExecContext(ctx, `
		UPDATE migrations
		SET status = 'executed',
		    version = COALESCE((SELECT MAX(m2.version) FROM migrations m2 WHERE m2.database_id = $1), 0) + 1,
		    executed_by = $2,
		    finish_time = $3,
		    updated_at = NOW(),
		    error_message = NULL
		WHERE id = $4
	`, dbID, actorUserID, finishTime, migrationID)
	return err
}

// Mengecek dan mengembalikan waktu commit
func commitTimeOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}
