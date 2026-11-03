package hackhttp

import (
	"os"
	"testing"

	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/testdb"
)

// testHackPages is the hack-event.html template cache every test fixture in
// this package shares (HackEventPage, RegisterHackPublic, and the tests
// that call renderHackEventPage/writeHackEventNotFound directly), built
// from the same embedded static/ tree main.go uses in production
// (handler.EmbeddedStatic()). cmd/server/main.go builds its own
// hackhttp.NewHackEventPages the same way; a test binary never runs main,
// so this package needs its own, test-only handler import (cleanup 3.2
// slice 3; the value itself replaces the old package-level
// SetEmbeddedStatic in slice 4).
var testHackPages = NewHackEventPages(handler.EmbeddedStatic())

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
