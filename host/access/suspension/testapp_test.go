package suspension

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/host/web"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is the take-down routes alone on the shared feature-test app.
type App struct{ *featuretest.Base }

// NewApp mounts the suspension routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, nil, eds...)
	(&Handler{Hosts: b.Hosts, Sites: b.SitesSvc(), Cutoff: b.Cutoff(), Pages: web.NewAssetPipeline(b.Ed, "")}).Register(b.Mux, b.Auth)
	return &App{Base: b}
}
