package operator

import (
	"testing"

	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/ops"
)

// App is the operator's account routes alone on the shared feature-test app.
type App struct{ *featuretest.Base }

// NewApp mounts the operator routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, nil, eds...)
	siteOps := httpx.NewRateLimiterFor(b.Limits.RateSiteOps.Burst, b.Limits.RateSiteOps.Every)
	(&Handler{Database: b.Database, Ed: b.Ed, Reporting: ops.NewReporting(b.Database)}).Register(b.Mux, b.Auth, siteOps)
	return &App{Base: b}
}
