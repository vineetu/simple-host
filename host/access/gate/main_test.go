package gate

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	operationsrepo "github.com/vsriram/simple-host/host/ops/repo"
	"github.com/vsriram/simple-host/host/testdb"
)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see host/testdb).
func TestMain(m *testing.M) { testdb.Main(m) }

var schemaErr struct {
	once sync.Once
	err  error
}

// testDB is the test's pool (testdb.DB), its schema checked once per process.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	database := testdb.DB(t)
	schemaErr.once.Do(func() { schemaErr.err = operationsrepo.VerifySchema(context.Background(), database) })
	if schemaErr.err != nil {
		t.Fatalf("test database is behind the schema: %v", schemaErr.err)
	}
	return database
}
