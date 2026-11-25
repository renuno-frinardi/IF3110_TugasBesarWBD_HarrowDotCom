package async

import (
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

func TestBuildBatchUpQueryCursorProgressionAndValidPostgres(t *testing.T) {
	tpl := `UPDATE benchmark_events SET processed = true
WHERE {{batch_column}} IN (
	SELECT {{batch_column}} FROM benchmark_events
	WHERE {{batch_column}} > {{last_id}}
	ORDER BY {{batch_column}} ASC
	LIMIT {{batch_size}}
)`
	cfg := migration.BatchConfig{Column: "id", Size: 250}

	wrapped1, dml1, err := BuildBatchUpQuery(tpl, cfg, "")
	if err != nil {
		t.Fatalf("first batch BuildBatchUpQuery failed: %v", err)
	}
	if !strings.Contains(dml1, `"id" IS NOT NULL`) {
		t.Errorf("expected first batch to use IS NOT NULL predicate, got:\n%s", dml1)
	}
	if _, err := pg_query.ParseToJSON(wrapped1); err != nil {
		t.Fatalf("first batch wrapped SQL is not valid PostgreSQL:\n%s\nerr: %v", wrapped1, err)
	}

	wrapped2, dml2, err := BuildBatchUpQuery(tpl, cfg, "250")
	if err != nil {
		t.Fatalf("second batch BuildBatchUpQuery failed: %v", err)
	}
	if !strings.Contains(dml2, `"id" > 250`) || !strings.Contains(dml2, `LIMIT 250`) {
		t.Errorf("expected cursor predicate \"id\" > 250 and LIMIT 250, got:\n%s", dml2)
	}
	if _, err := pg_query.ParseToJSON(wrapped2); err != nil {
		t.Fatalf("second batch wrapped SQL is not valid PostgreSQL:\n%s\nerr: %v", wrapped2, err)
	}
}

func TestBuildBatchUpQueryShorthandLimitNormalizationAndCTE(t *testing.T) {
	shorthand := `UPDATE users SET active = true WHERE {{batch_column}} > {{last_id}} ORDER BY {{batch_column}} LIMIT {{batch_size}}`
	wrapped, _, err := BuildBatchUpQuery(shorthand, migration.BatchConfig{Column: "id", Size: 100}, "500")
	if err != nil {
		t.Fatalf("shorthand batch query failed: %v", err)
	}
	if _, err := pg_query.ParseToJSON(wrapped); err != nil {
		t.Fatalf("expected rewritten shorthand query to be valid PostgreSQL:\n%s\nerr: %v", wrapped, err)
	}

	withCTE := `WITH batch_ids AS (
		SELECT {{batch_column}} FROM orders WHERE {{batch_column}} > {{last_id}} ORDER BY {{batch_column}} ASC LIMIT {{batch_size}}
	)
	UPDATE orders SET archived = true FROM batch_ids WHERE orders.{{batch_column}} = batch_ids.{{batch_column}}`
	wrappedCTE, _, err := BuildBatchUpQuery(withCTE, migration.BatchConfig{Column: "order_code", Size: 50}, "ORD'001")
	if err != nil {
		t.Fatalf("CTE batch query failed: %v", err)
	}
	if !strings.Contains(wrappedCTE, `'ORD''001'`) {
		t.Errorf("expected single quotes in text cursor to be SQL-escaped, got:\n%s", wrappedCTE)
	}
	if _, err := pg_query.ParseToJSON(wrappedCTE); err != nil {
		t.Fatalf("expected wrapped CTE query to be valid PostgreSQL:\n%s\nerr: %v", wrappedCTE, err)
	}
}

func TestBuildBatchDownQueryAndUnsafeRejection(t *testing.T) {
	cfg := migration.BatchConfig{Column: "id", Size: 500}
	downTpl := `UPDATE benchmark_events SET processed = false WHERE {{batch_column}} BETWEEN {{range_from}} AND {{range_to}}`

	downSQL, err := BuildBatchDownQuery(downTpl, cfg, "501", "1000")
	if err != nil {
		t.Fatalf("BuildBatchDownQuery failed: %v", err)
	}
	if !strings.Contains(downSQL, `"id" BETWEEN 501 AND 1000`) {
		t.Errorf("unexpected rendered down SQL: %s", downSQL)
	}
	if _, err := pg_query.ParseToJSON(downSQL); err != nil {
		t.Fatalf("rendered down SQL is not valid PostgreSQL: %v", err)
	}

	if _, err := BuildBatchDownQuery("UPDATE benchmark_events SET processed = false", cfg, "1", "500"); err == nil {
		t.Fatal("expected unscoped down SQL without range placeholders to be rejected")
	}

	if _, _, err := BuildBatchUpQuery(
		"UPDATE t SET x=1 WHERE {{batch_column}}>{{last_id}} LIMIT {{batch_size}}",
		migration.BatchConfig{Column: "id; DROP TABLE t;--", Size: 10},
		"0",
	); err == nil {
		t.Fatal("expected SQL injection in cursor column to be rejected")
	}
}
