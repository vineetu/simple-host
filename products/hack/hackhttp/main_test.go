package hackhttp

import (
	"os"
	"testing"

	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/internal/webassets"
)

// testHackPages is the shared event template cache built from embedded assets.
var testHackPages = NewHackEventPages(webassets.Static)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see internal/testdb). Duplicated from
// internal/handler/main_test.go (cleanup 3.2 slice 3): every DB-backed test
// package needs its own TestMain for scripts/test-integration.sh's
// TESTDB_ISOLATE=1 to isolate it, and hackhttp is now one such package.
func TestMain(m *testing.M) { testdb.Main(m) }

// isolateQueueTest gives queue consumers their own database under the
// integration runner. Duplicated from internal/handler/main_test.go
// (cleanup 3.2 slice 3): hack_content_test.go, hack_notifications_test.go
// and hack_storage_presets_test.go all moved with it.
func isolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", testdb.Fresh(t))
	}
}
