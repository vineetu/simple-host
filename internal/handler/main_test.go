package handler

import (
	"os"
	"testing"

	"github.com/vsriram/simple-host/internal/testdb"
)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see internal/testdb).
func TestMain(m *testing.M) { testdb.Main(m) }

// isolateQueueTest gives queue consumers their own database under the integration runner.
func isolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", testdb.Fresh(t))
	}
}
