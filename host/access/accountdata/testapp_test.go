package accountdata

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is a person's data routes alone on the shared feature-test app.
type App struct{ *featuretest.Base }

// NewApp mounts the export and erasure routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	return NewAppMailer(t, nil, eds...)
}

// NewAppMailer is NewApp with its mail sent to mailer.
func NewAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, mailer, eds...)
	lim := b.Limits
	siteOps := httpx.NewRateLimiterFor(lim.RateSiteOps.Burst, lim.RateSiteOps.Every)
	exports := httpx.NewRateLimiterFor(lim.RateExport.Burst, lim.RateExport.Every)
	(&Handler{Database: b.Database, Hosts: b.Hosts, Limits: lim, Mailer: b.Mailer, Sites: b.SitesSvc(), SiteFiles: b.SiteFiles(),
		SiteArchive: b.SiteArchive(), Cutoff: b.Cutoff()}).Register(b.Mux, b.Auth, siteOps, exports)
	return &App{Base: b}
}
