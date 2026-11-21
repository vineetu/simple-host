package papermetrics

import (
	"database/sql"
	"testing"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/products/hack/testdb"
)

// isolatedDB is a fresh clone of the composed schema template: the collector
// reads whole tables, so each test needs its own database.
func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	return d
}
