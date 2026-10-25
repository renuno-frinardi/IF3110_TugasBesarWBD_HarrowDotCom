package explain

import "testing"

func TestIsPureDDLUsesParsedStatementKind(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		{name: "create table as select", sql: "CREATE TABLE copy AS SELECT 1", want: false},
		{name: "create table", sql: "CREATE TABLE users (id integer)", want: true},
		{name: "alter table", sql: "ALTER TABLE users ADD COLUMN name text", want: true},
		{name: "select after comments", sql: "-- comment\nSELECT 1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPureDDL(tt.sql); got != tt.want {
				t.Fatalf("IsPureDDL(%q) = %v, want %v", tt.sql, got, tt.want)
			}
		})
	}
}

func TestExplainRejectsMultipleStatements(t *testing.T) {
	_, err := ExplainPostgres(nil, nil, "SELECT 1; SELECT 2;", false)
	if err != ErrMultipleStatements {
		t.Fatalf("ExplainPostgres error = %v, want %v", err, ErrMultipleStatements)
	}
}
