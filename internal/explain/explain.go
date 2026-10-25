package explain

// Import pkg
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const NotApplicableMessage = "tidak berlaku"

var ErrEmptyQuery = errors.New("SQL query cannot be empty")

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
	cleaned := stripSQLComments(sqlText)
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimSuffix(cleaned, ";")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return false
	}

	upper := strings.ToUpper(cleaned)
	tokens := strings.Fields(upper)
	if len(tokens) == 0 {
		return false
	}

	switch tokens[0] {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "VALUES", "WITH", "TABLE":
		return false
	case "CREATE":
		if strings.Contains(upper, " AS SELECT ") || strings.Contains(upper, " AS (SELECT ") {
			return false
		}
		return true
	case "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE", "COMMENT", "RENAME", "SET", "VACUUM", "ANALYZE":
		return true
	default:
		return true
	}
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
		return nil, errors.New("target database connection is nil")
	}

	tx, err := target.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin explain transaction: %w", err)
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
		return nil, fmt.Errorf("iterate explain rows: %w", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		return nil, fmt.Errorf("rollback explain transaction: %w", err)
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
