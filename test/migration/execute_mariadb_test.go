package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/explain"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

func TestMariaDBExecution(t *testing.T) {
	name := os.Getenv("TEST_MARIADB_DATABASE")
	if name == "" {
		t.Skip("TEST_MARIADB_DATABASE is not configured")
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("gunakan database dengan akhiran _test")
	}
	env, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal(err)
	}
	config := mysql.NewConfig()
	config.User, config.Passwd, config.DBName = env["MARIADB_USER"], env["MARIADB_PASSWORD"], name
	config.Net, config.Addr = "tcp", net.JoinHostPort("localhost", env["MARIADB_PORT"])
	target, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal("failed to open the test connection")
	}
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	table := fmt.Sprintf("barang_%d", time.Now().UnixNano())
	if _, err := target.ExecContext(ctx, "CREATE TABLE "+table+" (id INT PRIMARY KEY, nama VARCHAR(100), INDEX(nama)) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	defer target.Exec("DROP TABLE " + table)

	status, err := runMariaDBMigrationSQL(ctx, target, "INSERT INTO "+table+" VALUES (1, 'Buku'), (3, 'A; COMMIT;'), (2, 'Pensil');")
	if err != nil || status != S_Executed {
		t.Fatalf("commit failed: %s, %v", status, err)
	}
	_, rows, _, err := store.ReadMariaDBTable(ctx, target, name, table, 1)
	if err != nil || len(rows) != 3 {
		t.Fatalf("failed to read data: %v", err)
	}
	for i, row := range rows {
		if row[0] != fmt.Sprint(i+1) {
			t.Fatalf("primary key order is incorrect: %v", rows)
		}
	}

	status, err = runMariaDBMigrationSQL(ctx, target, "INSERT INTO "+table+" VALUES (4, 'Penghapus'); INSERT INTO "+table+" VALUES (1, 'Duplikat');")
	if err == nil || status != S_Failed {
		t.Fatalf("invalid SQL must fail: %s, %v", status, err)
	}
	var count int
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=4").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback failed: %d, %v", count, err)
	}

	status, err = runMariaDBMigrationSQL(ctx, target, "INSERT INTO "+table+" VALUES (4, 'Penghapus'); COMMIT;")
	if err == nil || status != S_Failed {
		t.Fatal("manual COMMIT must be rejected")
	}
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=4").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected SQL must not change data")
	}

	for _, analyze := range []bool{false, true} {
		plan, err := explain.ExplainMariaDB(ctx, target, "SELECT * FROM "+table+" WHERE id=1;", analyze)
		if err != nil || len(plan.PlanLines) == 0 {
			t.Fatalf("EXPLAIN/ANALYZE failed: %v", err)
		}
		if !strings.Contains(plan.Message, "key=PRIMARY") || !strings.Contains(plan.Message, "rows=") {
			t.Fatalf("the plan must show the index and row count: %s", plan.Message)
		}
		if analyze && !strings.Contains(plan.Message, "r_rows=") {
			t.Fatalf("ANALYZE must show the actual row count: %s", plan.Message)
		}
	}
	if _, err := explain.ExplainMariaDB(ctx, target, "UPDATE "+table+" SET nama='Berubah' WHERE id=1", true); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := target.QueryRowContext(ctx, "SELECT nama FROM "+table+" WHERE id=1").Scan(&value); err != nil || value != "Buku" {
		t.Fatalf("ANALYZE was not rolled back: %s, %v", value, err)
	}
	if _, err := explain.ExplainMariaDB(ctx, target, "DELETE FROM "+table+" WHERE id=1", true); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 3 {
		t.Fatalf("ANALYZE DELETE was not rolled back: %d, %v", count, err)
	}
}
