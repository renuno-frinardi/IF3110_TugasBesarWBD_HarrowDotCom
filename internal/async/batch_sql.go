package async

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

var (
	identPattern            = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)
	firstBatchPredPattern   = regexp.MustCompile(`\{\{batch_column\}\}\s*>\s*\{\{last_id\}\}`)
	shorthandUpdateLimitPat = regexp.MustCompile(`(?is)^\s*UPDATE\s+([A-Za-z0-9_."]+)\s+SET\s+(.+?)\s+WHERE\s+(.+?)(?:\s+ORDER\s+BY\s+.+?)?\s+LIMIT\s+(\d+)\s*$`)
	shorthandDeleteLimitPat = regexp.MustCompile(`(?is)^\s*DELETE\s+FROM\s+([A-Za-z0-9_."]+)\s+WHERE\s+(.+?)(?:\s+ORDER\s+BY\s+.+?)?\s+LIMIT\s+(\d+)\s*$`)
)

func QuoteIdent(col string) (string, error) {
	trimmed := strings.TrimSpace(col)
	if !identPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: invalid batch cursor column %q", migration.InvalidInput, col)
	}
	return `"` + trimmed + `"`, nil
}

func FormatCursorLiteral(cursor string) string {
	trimmed := strings.TrimSpace(cursor)
	if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return strconv.FormatInt(n, 10)
	}
	escaped := strings.ReplaceAll(cursor, "'", "''")
	return "'" + escaped + "'"
}

func CanSafelyRevertBatchSQL(downSQL string) bool {
	trimmed := strings.TrimSpace(downSQL)
	if trimmed == "" {
		return false
	}
	for _, required := range []string{"{{batch_column}}", "{{range_from}}", "{{range_to}}"} {
		if !strings.Contains(trimmed, required) {
			return false
		}
	}
	return true
}

func BuildBatchUpQuery(upTemplate string, cfg migration.BatchConfig, lastCursor string) (wrappedSQL string, renderedDML string, err error) {
	quotedCol, err := QuoteIdent(cfg.Column)
	if err != nil {
		return "", "", err
	}
	if cfg.Size < 1 || cfg.Size > 100000 {
		return "", "", fmt.Errorf("%w: batch size must be between 1 and 100000", migration.InvalidInput)
	}
	for _, req := range []string{"{{batch_column}}", "{{last_id}}", "{{batch_size}}"} {
		if !strings.Contains(upTemplate, req) {
			return "", "", fmt.Errorf("%w: batch SQL missing placeholder %s", migration.InvalidInput, req)
		}
	}

	dml := strings.TrimSpace(upTemplate)
	dml = strings.TrimRight(dml, "; \t\r\n")

	if lastCursor == "" {
		dml = firstBatchPredPattern.ReplaceAllString(dml, quotedCol+" IS NOT NULL")
		dml = strings.ReplaceAll(dml, "{{last_id}}", "0")
	} else {
		dml = strings.ReplaceAll(dml, "{{last_id}}", FormatCursorLiteral(lastCursor))
	}
	dml = strings.ReplaceAll(dml, "{{batch_column}}", quotedCol)
	dml = strings.ReplaceAll(dml, "{{batch_size}}", strconv.Itoa(cfg.Size))

	if _, parseErr := pg_query.ParseToJSON(dml); parseErr != nil {
		if rewritten, ok := rewriteShorthandBatchDML(dml, quotedCol); ok {
			dml = rewritten
		}
	}

	wrapped, wrapErr := wrapDMLWithCursorBounds(dml, quotedCol)
	if wrapErr != nil {
		return "", "", wrapErr
	}
	return wrapped, dml, nil
}

func BuildBatchDownQuery(downTemplate string, cfg migration.BatchConfig, rangeFrom, rangeTo string) (string, error) {
	quotedCol, err := QuoteIdent(cfg.Column)
	if err != nil {
		return "", err
	}
	if !CanSafelyRevertBatchSQL(downTemplate) {
		return "", fmt.Errorf("%w: batch down SQL must include {{batch_column}}, {{range_from}}, and {{range_to}}", ErrCancelUnsafe)
	}
	if strings.TrimSpace(rangeFrom) == "" || strings.TrimSpace(rangeTo) == "" {
		return "", fmt.Errorf("%w: batch range boundaries cannot be empty", ErrCancelUnsafe)
	}

	dml := strings.TrimSpace(downTemplate)
	dml = strings.TrimRight(dml, "; \t\r\n")
	dml = strings.ReplaceAll(dml, "{{batch_column}}", quotedCol)
	dml = strings.ReplaceAll(dml, "{{range_from}}", FormatCursorLiteral(rangeFrom))
	dml = strings.ReplaceAll(dml, "{{range_to}}", FormatCursorLiteral(rangeTo))
	return dml, nil
}

func rewriteShorthandBatchDML(dml, quotedCol string) (string, bool) {
	if m := shorthandUpdateLimitPat.FindStringSubmatch(dml); len(m) == 5 {
		table, setExpr, whereCond, limitN := m[1], m[2], m[3], m[4]
		rewritten := fmt.Sprintf(
			"UPDATE %s SET %s WHERE %s IN (SELECT %s FROM %s WHERE %s ORDER BY %s ASC LIMIT %s)",
			table, setExpr, quotedCol, quotedCol, table, whereCond, quotedCol, limitN,
		)
		if _, err := pg_query.ParseToJSON(rewritten); err == nil {
			return rewritten, true
		}
	}
	if m := shorthandDeleteLimitPat.FindStringSubmatch(dml); len(m) == 4 {
		table, whereCond, limitN := m[1], m[2], m[3]
		rewritten := fmt.Sprintf(
			"DELETE FROM %s WHERE %s IN (SELECT %s FROM %s WHERE %s ORDER BY %s ASC LIMIT %s)",
			table, quotedCol, quotedCol, table, whereCond, quotedCol, limitN,
		)
		if _, err := pg_query.ParseToJSON(rewritten); err == nil {
			return rewritten, true
		}
	}
	return "", false
}

func wrapDMLWithCursorBounds(dml, quotedCol string) (string, error) {
	trimmed := strings.TrimSpace(dml)
	upper := strings.ToUpper(trimmed)

	if strings.HasPrefix(upper, "WITH ") {
		splitIdx := findTopLevelDMLAfterWith(trimmed)
		if splitIdx > 0 {
			ctePrefix := strings.TrimSpace(trimmed[:splitIdx])
			mainDML := strings.TrimSpace(trimmed[splitIdx:])
			wrapped := fmt.Sprintf(
				"%s, _harrow_batch_mut AS (%s RETURNING %s AS _harrow_cursor) "+
					"SELECT COUNT(*), COALESCE(MIN(_harrow_cursor)::text, ''), COALESCE(MAX(_harrow_cursor)::text, '') FROM _harrow_batch_mut",
				ctePrefix, mainDML, quotedCol,
			)
			return wrapped, nil
		}
	}

	wrapped := fmt.Sprintf(
		"WITH _harrow_batch_mut AS (%s RETURNING %s AS _harrow_cursor) "+
			"SELECT COUNT(*), COALESCE(MIN(_harrow_cursor)::text, ''), COALESCE(MAX(_harrow_cursor)::text, '') FROM _harrow_batch_mut",
		trimmed, quotedCol,
	)
	return wrapped, nil
}

func findTopLevelDMLAfterWith(sqlText string) int {
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < len(sqlText) && sqlText[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				rest := sqlText[i+1:]
				trimmedRest := strings.TrimLeft(rest, " \t\r\n")
				offset := (i + 1) + (len(rest) - len(trimmedRest))
				if strings.HasPrefix(trimmedRest, ",") {
					continue
				}
				upperRest := strings.ToUpper(trimmedRest)
				for _, kw := range []string{"UPDATE ", "DELETE ", "INSERT "} {
					if strings.HasPrefix(upperRest, kw) {
						return offset
					}
				}
			}
		}
	}
	return -1
}
