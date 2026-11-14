package migrations

import (
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/testdb"
)

// TestMain keeps DB_DSN tests off the shared template when packages run in parallel.
func TestMain(m *testing.M) { testdb.Main(m) }

func TestDatabaseIsolation(t *testing.T) {
	if os.Getenv("TESTDB_ISOLATE") != "1" || os.Getenv("DB_DSN") == "" {
		return
	}
	template, err := url.Parse(os.Getenv("TESTDB_TEMPLATE_DSN"))
	if err != nil || template.Path == "" {
		t.Fatal("TestMain did not preserve the template database")
	}
	db, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var database string
	if err := db.QueryRow("SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database == strings.TrimPrefix(template.Path, "/") {
		t.Fatal("migration tests connected to the shared template")
	}
	// Keep this package's connection open while another fixture clones the
	// template, as happens when packages run concurrently.
	testdb.Fresh(t)
}
