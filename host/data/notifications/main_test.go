package notifications

import (
	"os"
	"testing"

	"github.com/vsriram/simple-host/host/data/sqlconsole"

	"github.com/vsriram/simple-host/host/data/resources"

	"github.com/vsriram/simple-host/host/data/push"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/server/servertest/datatest"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// isolateQueueTest gives queue consumers their own database under the integration runner.
func isolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", testdb.Fresh(t))
	}
}

// routes mounts the notification and push routes and the resource
// declarations and tables they name. Notifications of storage writes are proved where
// every route is mounted (servertest/datatest).
func routes(a *datatest.App) {
	Register(a.Mux, a.Data, a.Auth, a.Notice)
	push.Register(a.Mux, a.Data, a.PasscodeGate, a.Caller, a.Auth, a.Notice)
	resources.Register(a.Mux, a.Data, a.Caller)
	sqlconsole.Register(a.Mux, a.Data, a.Caller)
}

// newShop is datatest.NewShop with those routes.
func newShop(t *testing.T, eds ...*edition.Edition) datatest.Shop {
	t.Helper()
	return datatest.NewShop(t, routes, eds...)
}
