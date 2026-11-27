package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestJSONLoggerRedactionAndContextEnrichment(t *testing.T) {
	var logBuf bytes.Buffer

	logger := NewJSONLogger(&logBuf, slog.LevelDebug)

	ctx := context.Background()
	ctx = WithCorrelationID(ctx, "corr-abc-123")
	ctx = WithActorID(ctx, 42)
	ctx = WithDatabaseID(ctx, 7)

	secretSession := strings.Repeat("ab", 32)
	logger.InfoContext(ctx, "worker.job.started",
		slog.String("status", "running"),
		slog.String("job_id", "0123456789abcdef0123456789abcdef"),
		slog.String("password", "super-secret-db-pass"),
		slog.String("session_id", secretSession),
		slog.String("dsn_info", "postgresql://harrow:mySecretPass123@localhost:5433/harrow_db"),
		slog.String("note", "cookie harrow_session=tok12345 and token=xyz987"),
	)

	rawLog := logBuf.String()
	for _, leaked := range []string{"super-secret-db-pass", "mySecretPass123", secretSession, "tok12345", "xyz987"} {
		if strings.Contains(rawLog, leaked) {
			t.Fatalf("sensitive value %q leaked in log output: %s", leaked, rawLog)
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawLog)), &parsed); err != nil {
		t.Fatalf("expected valid JSON log line, got %v: %s", err, rawLog)
	}

	for _, key := range []string{"time", "level", "event", "actor", "database", "status", "correlation_id", "job_id"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing required JSON log attribute %q in %v", key, parsed)
		}
	}

	if parsed["event"] != "worker.job.started" {
		t.Errorf("expected event worker.job.started, got %v", parsed["event"])
	}
	if parsed["actor"] != "42" || parsed["database"] != "7" || parsed["correlation_id"] != "corr-abc-123" {
		t.Errorf("unexpected context attributes in %v", parsed)
	}
	if parsed["job_id"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("job_id should not be redacted, got %v", parsed["job_id"])
	}
}

func TestSeparateSQLLoggingAndSQLState(t *testing.T) {
	var buf bytes.Buffer
	logger := NewJSONLogger(&buf, slog.LevelInfo)

	ctx := WithCorrelationID(context.Background(), "corr-sql-1")
	pgErr := &pgconn.PgError{Code: "42P01", Message: "relation \"missing_tbl\" does not exist"}

	entry := SQLLogEntry{
		Actor:        "9",
		Database:     "3",
		MigrationID:  11,
		JobID:        "job-123",
		ExecutionID:  55,
		Attempt:      2,
		BatchNo:      1,
		Query:        "UPDATE users SET password='inlineSecretValue' WHERE id > $1",
		Params:       []any{100},
		Duration:     15 * time.Millisecond,
		RowsAffected: 0,
		Err:          errors.New("exec failed"),
	}
	entry.Err = pgErr

	LogSQLStart(ctx, logger, SQLLogEntry{
		Actor: entry.Actor, Database: entry.Database, MigrationID: entry.MigrationID,
		JobID: entry.JobID, ExecutionID: entry.ExecutionID, Attempt: entry.Attempt, BatchNo: entry.BatchNo,
		Query: entry.Query, Params: entry.Params,
	})
	LogSQLResult(ctx, logger, entry)
	LogTxOutcome(ctx, logger, false, entry)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 separate log lines (sql.start, sql.result, tx.rollback), got %d:\n%s", len(lines), buf.String())
	}
	if strings.Contains(buf.String(), "inlineSecretValue") {
		t.Fatalf("inline SQL password should be redacted:\n%s", buf.String())
	}

	events := make([]string, 0, 3)
	for _, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		ev, _ := obj["event"].(string)
		events = append(events, ev)
		if ev == "sql.result" && obj["sqlstate"] != "42P01" {
			t.Errorf("expected sqlstate 42P01 in sql.result, got %v", obj["sqlstate"])
		}
	}
	if events[0] != "sql.start" || events[1] != "sql.result" || events[2] != "tx.rollback" {
		t.Fatalf("unexpected SQL log sequence: %v", events)
	}
}
