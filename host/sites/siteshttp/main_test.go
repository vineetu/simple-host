package siteshttp

import (
	"testing"

	"github.com/vsriram/simple-host/host/testdb"
)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see host/testdb).
func TestMain(m *testing.M) { testdb.Main(m) }
