package papermetrics

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"os"
	"strings"
	"testing"
	"time"
)

func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN not set")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("paper_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	d, err := sql.Open("postgres", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	ddl, err := os.ReadFile("../../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(string(ddl)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return d
}
