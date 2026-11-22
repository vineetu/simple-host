package signin

import (
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
)

// App is the sign-in routes alone on the shared feature-test app.
type App struct {
	*featuretest.Base
	Users *Handler
}

// NewApp mounts the sign-in routes for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	return NewAppMailer(t, nil, eds...)
}

// NewAppMailer is NewApp with its mail sent to mailer.
func NewAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *App {
	t.Helper()
	b := featuretest.NewBase(t, mailer, eds...)
	service := accounts.New(b.Database)
	ip := httpx.NewRateLimiterFor(b.Limits.RateSigninIP.Burst, b.Limits.RateSigninIP.Every)
	h := New(b.Database, service, b.Mailer, b.URL, b.Ed, ip, b.EmailLimiter, auth.NewSignInAlerts(service, b.Mailer, b.URL), &httpx.Caches{})
	h.Register(b.Mux, b.Auth, b.Notice)
	return &App{Base: b, Users: h}
}

// NewAccountApp is an App with a capturing mailbox, synchronous sign-in
// alerts and a roomy email limiter.
func NewAccountApp(t *testing.T, eds ...*edition.Edition) (*App, *featuretest.Mailbox) {
	t.Helper()
	mb := &featuretest.Mailbox{}
	a := NewAppMailer(t, mb, eds...)
	a.Users.Alerts.Inline = true
	a.RoomyMail()
	return a, mb
}

// SetLimits gives the sign-in routes the limits l.
func (a *App) SetLimits(l config.Limits) { a.Limits, a.Users.Limits = l, l }
