package migration

import (
	"encoding/json"
	"fmt"

	"github.com/pganalyze/pg_query_go/v6"
)

func validateSQL(sql string) error {
	parsed, err := pg_query.ParseToJSON(sql)
	if err != nil {
		return fmt.Errorf("%w: sintaks SQL migration tidak valid", InvalidInput)
	}

	var result struct {
		Statements []struct {
			Statement map[string]json.RawMessage `json:"stmt"`
		} `json:"stmts"`
	}
	if err := json.Unmarshal([]byte(parsed), &result); err != nil {
		return fmt.Errorf("gagal membaca hasil parser SQL: %w", err)
	}

	if len(result.Statements) == 0 {
		return fmt.Errorf("%w: SQL harus berisi setidaknya satu statement", InvalidInput)
	}

	for i, statement := range result.Statements {
		if len(statement.Statement) != 1 {
			return fmt.Errorf("hasil parser SQL statement ke-%d tidak dikenali", i+1)
		}

		for kind := range statement.Statement {
			switch kind {
			case "TransactionStmt":
				return fmt.Errorf("%w: statement ke-%d mengatur transaksi; transaksi dikelola executor", InvalidInput, i+1)
			case "VariableSetStmt", "DiscardStmt",
				"ExecuteStmt",
				"CopyStmt", "CreatedbStmt", "DropdbStmt",
				"AlterSystemStmt":
				return fmt.Errorf("%w: statement ke-%d (%s) tidak didukung untuk migration", InvalidInput, i+1, kind)
			}
		}
	}

	return nil
}
