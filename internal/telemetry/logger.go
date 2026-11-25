package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
)

func NewJSONLogger(w io.Writer, level slog.Level) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.MessageKey {
				a.Key = "event"
				a.Value = slog.StringValue(RedactString(a.Value.String()))
				return a
			}
			return sanitizeAttr(a)
		},
	}
	base := slog.NewJSONHandler(w, opts)
	return slog.New(&contextHandler{inner: base})
}

type contextHandler struct {
	inner slog.Handler
	attrs []slog.Attr
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	present := make(map[string]bool, r.NumAttrs()+len(h.attrs))
	for _, a := range h.attrs {
		present[a.Key] = true
	}

	cleaned := slog.NewRecord(r.Time, r.Level, RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		present[a.Key] = true
		cleaned.AddAttrs(sanitizeAttr(a))
		return true
	})

	if ctx != nil {
		if !present["correlation_id"] {
			if corrID := CorrelationIDFromContext(ctx); corrID != "" {
				cleaned.AddAttrs(slog.String("correlation_id", corrID))
			}
		}
		if !present["actor"] {
			if actor := ActorFromContext(ctx); actor != "" {
				cleaned.AddAttrs(slog.String("actor", actor))
			}
		}
		if !present["database"] {
			if db := DatabaseFromContext(ctx); db != "" {
				cleaned.AddAttrs(slog.String("database", db))
			}
		}
		sc := trace.SpanContextFromContext(ctx)
		if sc.IsValid() {
			if !present["trace_id"] {
				cleaned.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
			}
			if !present["span_id"] {
				cleaned.AddAttrs(slog.String("span_id", sc.SpanID().String()))
			}
		}
	}

	return h.inner.Handle(ctx, cleaned)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		sanitized[i] = sanitizeAttr(a)
	}
	combined := make([]slog.Attr, 0, len(h.attrs)+len(sanitized))
	combined = append(combined, h.attrs...)
	combined = append(combined, sanitized...)
	return &contextHandler{
		inner: h.inner.WithAttrs(sanitized),
		attrs: combined,
	}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{
		inner: h.inner.WithGroup(name),
		attrs: h.attrs,
	}
}

func sanitizeAttr(a slog.Attr) slog.Attr {
	if a.Key == "" {
		return a
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		if safeExactKeys[a.Key] {
			return a
		}
		return slog.String(a.Key, RedactString(v.String()))
	case slog.KindGroup:
		groupAttrs := v.Group()
		cleaned := make([]any, len(groupAttrs))
		for i, ga := range groupAttrs {
			cleaned[i] = sanitizeAttr(ga)
		}
		return slog.Group(a.Key, cleaned...)
	case slog.KindAny:
		switch raw := v.Any().(type) {
		case error:
			if raw != nil {
				return slog.String(a.Key, RedactString(raw.Error()))
			}
		case []byte:
			return slog.String(a.Key, RedactString(string(raw)))
		}
	}
	return a
}

func ExtractSQLState(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

type SQLLogEntry struct {
	Actor        string
	Database     string
	MigrationID  int
	JobID        string
	ExecutionID  int64
	Attempt      int
	BatchNo      int
	Query        string
	Params       []any
	Duration     time.Duration
	RowsAffected int64
	Status       string
	Err          error
}

func (e SQLLogEntry) baseAttrs() []any {
	attrs := []any{
		slog.String("actor", e.Actor),
		slog.String("database", e.Database),
		slog.Int("migration_id", e.MigrationID),
		slog.String("job_id", e.JobID),
		slog.Int64("execution_id", e.ExecutionID),
		slog.Int("attempt", e.Attempt),
		slog.Int("batch", e.BatchNo),
	}
	if e.Status != "" {
		attrs = append(attrs, slog.String("status", e.Status))
	}
	return attrs
}

func LogSQLStart(ctx context.Context, logger *slog.Logger, entry SQLLogEntry) {
	if logger == nil {
		logger = slog.Default()
	}
	if entry.Status == "" {
		entry.Status = "started"
	}
	attrs := append(entry.baseAttrs(),
		slog.String("query", RedactString(entry.Query)),
		slog.Any("params", RedactParams(entry.Params)),
	)
	logger.InfoContext(ctx, "sql.start", attrs...)
}

func LogSQLResult(ctx context.Context, logger *slog.Logger, entry SQLLogEntry) {
	if logger == nil {
		logger = slog.Default()
	}
	attrs := append(entry.baseAttrs(),
		slog.String("query", RedactString(entry.Query)),
		slog.Any("params", RedactParams(entry.Params)),
		slog.Float64("duration_ms", float64(entry.Duration.Microseconds())/1000.0),
		slog.Int64("rows", entry.RowsAffected),
	)
	if entry.Err != nil {
		if entry.Status == "" {
			entry.Status = "error"
		}
		attrs = append(attrs,
			slog.String("error", RedactString(entry.Err.Error())),
			slog.String("sqlstate", ExtractSQLState(entry.Err)),
		)
		logger.ErrorContext(ctx, "sql.result", attrs...)
		return
	}
	if entry.Status == "" {
		entry.Status = "ok"
	}
	logger.InfoContext(ctx, "sql.result", attrs...)
}

func LogTxOutcome(ctx context.Context, logger *slog.Logger, committed bool, entry SQLLogEntry) {
	if logger == nil {
		logger = slog.Default()
	}
	event := "tx.rollback"
	if committed {
		event = "tx.commit"
	}
	if entry.Status == "" {
		if entry.Err != nil {
			entry.Status = "error"
		} else if committed {
			entry.Status = "committed"
		} else {
			entry.Status = "rolled_back"
		}
	}
	attrs := append(entry.baseAttrs(),
		slog.Float64("duration_ms", float64(entry.Duration.Microseconds())/1000.0),
		slog.Int64("rows", entry.RowsAffected),
	)
	if entry.Err != nil {
		attrs = append(attrs,
			slog.String("error", RedactString(entry.Err.Error())),
			slog.String("sqlstate", ExtractSQLState(entry.Err)),
		)
		logger.ErrorContext(ctx, event, attrs...)
		return
	}
	logger.InfoContext(ctx, event, attrs...)
}
