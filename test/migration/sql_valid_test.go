package migration

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSQL(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		invalid bool
	}{
		{"DDL_dan_DML", `CREATE TABLE items (id INT, name TEXT);
			ALTER TABLE items ADD COLUMN price INT;
			CREATE INDEX items_name_idx ON items (name);
			INSERT INTO items VALUES (1, 'book', 10);
			UPDATE items SET price = 20 WHERE id = 1;
			DELETE FROM items WHERE id = 1;
			DROP TABLE items;`, false},
		{"select", "SELECT 1;", false},
		{"komentar", "-- COMMIT;\nSELECT 1; /* ROLLBACK; */", false},
		{"string", "SELECT 'COMMIT;';", false},
		{"kosong", "", true},
		{"spasi", " \n\t ", true},
		{"hanya_komentar", "-- SELECT 1;\n/* komentar */", true},
		{"hanya_pemisah", "; ; ;", true},
		{"sintaks_salah", "CREATE TABLE;", true},
		{"statement_kedua_salah", "SELECT 1; SELECT FROM;", true},
		{"begin", "BEGIN;", true},
		{"commit", "COMMIT;", true},
		{"rollback", "ROLLBACK;", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSQL(test.sql)
			if test.invalid {
				if !errors.Is(err, InvalidInput) {
					t.Fatalf("invalid input should be rejected, got: %v", err)
				}
			} else if err != nil {
				t.Fatalf("SQL should be accepted: %v", err)
			}
		})
	}

	t.Run("posisi_statement_ditolak", func(t *testing.T) {
		err := validateSQL("SELECT 1; COMMIT;")
		if err == nil || !strings.Contains(err.Error(), "statement 2") {
			t.Fatalf("The error must identify the second statement: %v", err)
		}
	})
}
