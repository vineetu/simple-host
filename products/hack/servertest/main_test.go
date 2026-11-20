package servertest

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	platformtestdb "github.com/vsriram/simple-host/host/testdb"
	hackdb "github.com/vsriram/simple-host/products/hack/db"

	"github.com/vsriram/simple-host/products/hack/hackhttp"
	"github.com/vsriram/simple-host/products/hack/testdb"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

// testHackPages is the shared event template cache built from embedded assets.
var testHackPages = hackhttp.NewHackEventPages(webassets.Static)

// TestMain keeps the moved tests in their own disposable package database.
func TestMain(m *testing.M) { testdb.Main(m) }

func isolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", testdb.Fresh(t))
	}
}

var schemaErr struct {
	once sync.Once
	err  error
}

// testDB is the test's pool (host/testdb.DB), its schema checked once per process.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	database := platformtestdb.DB(t)
	schemaErr.once.Do(func() { schemaErr.err = hackdb.VerifyHackSchema(context.Background(), database) })
	if schemaErr.err != nil {
		t.Fatalf("test database is behind the schema: %v", schemaErr.err)
	}
	return database
}
