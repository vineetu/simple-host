package datatest

import (
	"os"
	"testing"

	"github.com/vsriram/simple-host/host/data/notifications"
	"github.com/vsriram/simple-host/host/data/push"

	"github.com/vsriram/simple-host/host/data/files"
	"github.com/vsriram/simple-host/host/data/kv"
	"github.com/vsriram/simple-host/host/data/resources"
	"github.com/vsriram/simple-host/host/data/rows"
	"github.com/vsriram/simple-host/host/data/sqlconsole"

	"github.com/vsriram/simple-host/host/data/visitors"

	"github.com/vsriram/simple-host/host/data/usage"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/testdb"
)

// TestMain runs this package's tests in a disposable per-process database
// when TESTDB_ISOLATE=1 (see host/testdb).
func TestMain(m *testing.M) { testdb.Main(m) }

// isolateQueueTest gives queue consumers their own database under the integration runner.
func isolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", testdb.Fresh(t))
	}
}

// allRoutes mounts every Data route on a.Mux.
func allRoutes(a *App) {
	notifications.Register(a.Mux, a.Data, a.Auth, a.Notice)
	push.Register(a.Mux, a.Data, a.PasscodeGate, a.Caller, a.Auth, a.Notice)
	a.Data.RegisterStorageRoutes(a.Mux)
	kv.Register(a.Mux, a.Data, a.Caller)
	rows.Register(a.Mux, a.Data, a.Caller)
	sqlconsole.Register(a.Mux, a.Data, a.Caller)
	files.Register(a.Mux, a.Data, a.Caller)
	resources.Register(a.Mux, a.Data, a.Caller)
	usage.Register(a.Mux, a.Data, a.Caller)
	visitors.Register(a.Mux, a.Data, a.Caller)
}

// newShop is NewShop with every Data route.
func newShop(t *testing.T, eds ...*edition.Edition) Shop {
	t.Helper()
	return NewShop(t, allRoutes, eds...)
}
