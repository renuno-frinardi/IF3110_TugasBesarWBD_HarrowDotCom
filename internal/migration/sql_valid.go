package migration

import (
	"encoding/json"
	"fmt"

	"github.com/pganalyze/pg_query_go/v6"
)

func validateSQL(sql string) error {
	parsed, err := pg_query.ParseToJSON(sql)
	if err != nil {
		return fmt.Errorf("%w: invalid migration SQL syntax", InvalidInput)
	}

	var result struct {
		Statements []struct {
			Statement map[string]json.RawMessage `json:"stmt"`
		} `json:"stmts"`
	}
	if err := json.Unmarshal([]byte(parsed), &result); err != nil {
		return fmt.Errorf("Failed to read the SQL parser result: %w", err)
	}

	if len(result.Statements) == 0 {
		return fmt.Errorf("%w: SQL must contain at least one statement", InvalidInput)
	}

	for i, statement := range result.Statements {
		if len(statement.Statement) != 1 {
			return fmt.Errorf("Unrecognized SQL parser result for statement %d", i+1)
		}

		for kind := range statement.Statement {
			switch kind {
			case "TransactionStmt":
				return fmt.Errorf("%w: statement %d controls transactions, let the application manage transactions", InvalidInput, i+1)
			case "VariableSetStmt", "DiscardStmt",
				"ExecuteStmt",
				"CopyStmt", "CreatedbStmt", "DropdbStmt",
				"AlterSystemStmt":
				return fmt.Errorf("%w: statement %d (%s) is not supported for migrations", InvalidInput, i+1, kind)
			}
		}
	}

	return nil
}

func ValidateSQL(sql string) error {
	return validateSQL(sql)
}
