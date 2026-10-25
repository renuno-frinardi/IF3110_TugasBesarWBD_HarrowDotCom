package store

import "testing"

func TestMariaStatements(t *testing.T) {
	statements, err := MariaDBStatements("-- komentar\nINSERT INTO barang VALUES (1, 'it''s; COMMIT;'); # komentar\nSELECT * FROM `barang`;")
	if err != nil || len(statements) != 2 {
		t.Fatalf("SQL splitting failed: %v, %v", statements, err)
	}
	if err := ValidateMariaDBStatements(statements); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "SELECT 'belum ditutup", "/*! COMMIT */", "SELECT 1; COMMIT;", "CREATE TABLE barang (id INT)", "SET autocommit=1"} {
		statements, err := MariaDBStatements(query)
		if err == nil {
			err = ValidateMariaDBStatements(statements)
		}
		if err == nil {
			t.Fatalf("SQL must be rejected: %s", query)
		}
	}
}
