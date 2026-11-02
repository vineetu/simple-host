package analytics

import (
	"testing"

	"github.com/vsriram/simple-host/internal/testdb"
)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see internal/testdb).
func TestMain(m *testing.M) { testdb.Main(m) }
