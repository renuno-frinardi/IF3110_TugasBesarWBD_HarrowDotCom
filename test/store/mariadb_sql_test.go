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
	ddl, err := MariaDBStatements("CREATE TABLE barang2 (id INT); ALTER TABLE barang2 ADD x INT; DROP TABLE barang2;")
	if err != nil || len(ddl) != 3 {
		t.Fatalf("DDL splitting failed: %v, %v", ddl, err)
	}
	if err := ValidateMariaDBStatements(ddl); err != nil {
		t.Fatalf("DDL must be accepted: %v", err)
	}
	for _, query := range []string{"", "SELECT 'belum ditutup", "/*! COMMIT */", "SELECT 1; COMMIT;", "SET autocommit=1"} {
		statements, err := MariaDBStatements(query)
		if err == nil {
			err = ValidateMariaDBStatements(statements)
		}
		if err == nil {
			t.Fatalf("SQL must be rejected: %s", query)
		}
	}
}
