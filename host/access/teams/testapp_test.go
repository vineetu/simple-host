package teams

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is the team routes alone on the shared feature-test app.
type App struct{ *featuretest.Base }

// NewApp mounts the team routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, nil, eds...)
	if b.Ed.Identity().PersonalSites { // as the composition mounts them
		(&Handler{DB: b.Database, Cutoff: b.Cutoff()}).Register(b.Mux, b.Auth, b.Notice)
	}
	return &App{Base: b}
}
