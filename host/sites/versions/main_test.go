package versions_test

import (
	"testing"

	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
	"github.com/vsriram/simple-host/host/sites/versions"
	"github.com/vsriram/simple-host/host/testdb"
)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see host/testdb).
func TestMain(m *testing.M) { testdb.Main(m) }

// newApp is the fixture with the versions routes mounted beside deploy's.
func newApp(t *testing.T) *fixture.App {
	t.Helper()
	a := fixture.New(t)
	versions.Register(a.Mux, a.H, a.Auth, a.Notice, a.SiteOps)
	return a
}
