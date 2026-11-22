package editors

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is the site-editor routes alone on the shared feature-test app.
type App struct{ *featuretest.Base }

// NewApp mounts the editor routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, nil, eds...)
	Register(b.Mux, b.Database, b.Auth, b.Notice)
	return &App{Base: b}
}
