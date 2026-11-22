package profile

import (
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// App is the profile routes (and PATCH /v1/me) alone on the shared feature-test app.
type App struct {
	*featuretest.Base
	Users *Handler
}

// NewApp mounts the profile routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	return NewAppMailer(t, nil, eds...)
}

// NewAppMailer is NewApp with its mail sent to mailer.
func NewAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, mailer, eds...)
	service := accounts.New(b.Database)
	lim := b.Limits
	ip := httpx.NewRateLimiterFor(lim.RateSigninIP.Burst, lim.RateSigninIP.Every)
	check := httpx.NewRateLimiterFor(lim.RateHandleCheck.Burst, lim.RateHandleCheck.Every)
	h := New(b.Database, service, b.Mailer, b.URL, b.Ed, ip, b.EmailLimiter, check, auth.NewSignInAlerts(service, b.Mailer, b.URL))
	h.SetSites(b.Hosts, b.Disk, lim)
	h.Register(b.Mux, b.Auth, b.Notice)
	(&Patch{Database: b.Database, Hosts: b.Hosts, SiteFiles: b.SiteFiles(), Limits: lim, Ed: b.Ed}).Register(b.Mux, b.Auth)
	return &App{Base: b, Users: h}
}

// NewAccountApp is an App with a capturing mailbox, synchronous alerts and a
// roomy email limiter.
func NewAccountApp(t *testing.T, eds ...*edition.Edition) (*App, *featuretest.Mailbox) {
	t.Helper()
	mb := &featuretest.Mailbox{}
	a := NewAppMailer(t, mb, eds...)
	a.Users.Alerts.Inline = true
	a.RoomyMail()
	return a, mb
}
