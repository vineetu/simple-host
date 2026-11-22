package connector

import (
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is the connector (OAuth server and /mcp) alone on the shared
// feature-test app; its tokens reach the app's routes through BearerAuth.
type App struct {
	*featuretest.Base
	Conn *Handler
}

// NewApp mounts the connector for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, nil, eds...)
	service := accounts.New(b.Database)
	c := New(b.Database, b.URL, b.Admin, rt.SiteDomain, rt.ContentHost, "1.0.0", b.Gated, false, nil, auth.NewSignInAlerts(service, b.Mailer, b.URL), b.Ed)
	c.Register(b.Mux, b.Auth)
	b.Bearer = c.BearerAuth
	return &App{Base: b, Conn: c}
}

// SetLimits gives the connector the limits l.
func (a *App) SetLimits(l config.Limits) { a.Limits, a.Conn.Limits = l, l }
