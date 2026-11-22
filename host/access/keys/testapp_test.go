package keys

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is the key routes alone on the shared feature-test app.
type App struct {
	*featuretest.Base
	Keys *Handler
}

// NewApp mounts the key routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, nil, eds...)
	h := &Handler{DB: b.Database, Limits: b.Limits}
	h.Register(b.Mux, b.Auth, b.Notice)
	return &App{Base: b, Keys: h}
}

// SetLimits gives the key routes the limits l.
func (a *App) SetLimits(l config.Limits) { a.Limits, a.Keys.Limits = l, l }
