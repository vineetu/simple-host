package gate

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	appstorage "github.com/vsriram/simple-host/host/data"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"

	. "github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/idempotency"
)

// App is the site gate and the visitor's ways through it alone on the
// shared feature-test app: its routes, the probes of its data-route gates,
// and the site's pages served through SiteGate.
type App struct {
	*Base
	Access *Handler
}

// NewApp mounts the gate for ed (the default edition when none).
func NewApp(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	return NewAppMailer(t, email.NewResendSender("", "test@example.com"), eds...)
}

// NewAppMailer is NewApp with the given mailer.
func NewAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *App {
	t.Helper()
	b := NewBase(t, mailer, eds...)
	lim := b.Limits
	access := &Handler{Hosts: b.Hosts, Ed: b.Ed, Limits: lim, Database: b.Database, Disk: b.Disk, Mailer: b.Mailer,
		EmailLimiter: b.EmailLimiter, VisitorAuthLimiter: httpx.NewRateLimiterFor(lim.RateVisitorAuth.Burst, lim.RateVisitorAuth.Every),
		SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, ExportKey: []byte("accesstest-export-key-0123456789"), UploadLocks: &sync.Map{},
		AdminAPIKey: b.Admin, AdminUserID: b.AdminID, HTTPCaches: &httpx.Caches{}, CreateIdempotency: idempotency.New(lim)}
	access.Passcode.Limits = lim
	access.Notify = notify.NewEngine(b.Database, b.Ed, lim, rt.SiteDomain)
	access.SiteFiles = b.SiteFiles()
	siteOps := httpx.NewRateLimiterFor(lim.RateSiteOps.Burst, lim.RateSiteOps.Every)
	access.RegisterGateRoutes(b.Mux, b.Auth, b.Notice, siteOps)
	mountGateProbes(b.Mux, access)
	b.SiteGate = access.SiteGate()
	return &App{Base: b, Access: access}
}

// NewAccountApp is an App with a capturing mailbox and a roomy email limiter.
func NewAccountApp(t *testing.T, eds ...*edition.Edition) (*App, *Mailbox) {
	t.Helper()
	mb := &Mailbox{}
	a := NewAppMailer(t, mb, eds...)
	a.RoomyMail()
	return a, mb
}

// SetLimits gives the gate the limits l.
func (a *App) SetLimits(l config.Limits) {
	a.Limits, a.Access.Limits, a.Access.Passcode.Limits = l, l, l
}

// CallJSON is what the storage probe answers: the Call Access resolved.
type CallJSON struct {
	SiteID, OwnerID, SiteName, AccountID string
	Owner, VisitorAI                     bool
	VisitorID, LinkScope                 string
	PasscodeOK, ViewersOK, OnOwnAddress  bool
	CSRFOK                               bool
}

// mountGateProbes mounts the storage probe and the gate probe.
func mountGateProbes(mux *http.ServeMux, access *Handler) {
	probe := access.StorageCaller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := appstorage.CallFrom(r.Context())
		httpx.WriteJSON(w, http.StatusOK, CallJSON{SiteID: c.SiteID, OwnerID: c.OwnerID, SiteName: c.SiteName, AccountID: c.AccountID,
			Owner: c.Owner, VisitorAI: c.VisitorAI, VisitorID: c.VisitorID, LinkScope: c.LinkScope,
			PasscodeOK: c.PasscodeOK, ViewersOK: c.ViewersOK, OnOwnAddress: c.OnOwnAddress, CSRFOK: c.CSRFOK})
	}))
	mux.Handle("GET /v1/sites/{sitename}/storage/probe", probe)
	// The same probe on a data route editors may use (owner= by an editor
	// passes the scope gate only on editor routes).
	mux.Handle("GET /v1/sites/{sitename}/storage/usage", probe)
	mux.Handle("GET /v1/sites/{sitename}/gate-probe", access.PasscodeGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
}

// Probe asks the storage probe which Call Access resolves for this request.
func (a *App) Probe(t *testing.T, host, site string, headers map[string]string) (rt.Resp, CallJSON) {
	t.Helper()
	r := a.At(t, "GET", host, "/v1/sites/"+site+"/storage/probe", nil, headers)
	var c CallJSON
	if r.Status == http.StatusOK {
		if err := json.Unmarshal(r.Body, &c); err != nil {
			t.Fatal(err)
		}
	}
	return r, c
}

// TestPasscodeKey is the passcode encryption key of EnablePasscodes (tests only).
const TestPasscodeKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80="

// EnablePasscodes turns site passcodes on (a key, SITE_PASSCODES, a
// six-character minimum), as an install with PASSCODE_ENC_KEY has them.
func (a *App) EnablePasscodes(t *testing.T) {
	t.Helper()
	if err := a.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	a.Access.Limits.SitePasscodes, a.Access.Limits.PasscodeMinLength = true, 6
	a.Access.Passcode.Limits = a.Access.Limits
}

// WithOAuth registers the OAuth sign-in routes with FakeProvider as Google.
func (a *App) WithOAuth(t *testing.T) *OAuthHandler {
	t.Helper()
	oh := &OAuthHandler{
		Database:            a.Database,
		Limits:              a.Access.Limits,
		Cfg:                 OAuthSettings{PublicBaseURL: "https://" + rt.SiteDomain, SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, CNAMETarget: "cname." + rt.SiteDomain},
		Accounts:            accounts.New(a.Database),
		SignIn:              NewProviderSignIn(accounts.New(a.Database), map[string]Provider{"google": FakeProvider{}}),
		IPLimiter:           httpx.NewRateLimiter(10000, 10000),
		SSOKeyLimiter:       httpx.NewRateLimiter(10000, 10000),
		SSOSessionIPLimiter: httpx.NewRateLimiter(10000, 10000),
		LookupIP:            func(context.Context, string) ([]net.IP, error) { return nil, nil },
		LookupCNAME:         func(context.Context, string) (string, error) { return "cname." + rt.SiteDomain + ".", nil },
		Access:              a.Access,
	}
	oh.Register(a.Mux)
	return oh
}

// newSSOApp is an App with OAuth sign-in and the SSO hop on the app's own address.
func newSSOApp(t *testing.T) (*App, *OAuthHandler) {
	t.Helper()
	a := NewApp(t)
	o := a.WithOAuth(t)
	o.Cfg.PublicBaseURL = a.Srv.URL
	a.Access.SetSSOHop(o)
	return a, o
}

// NewPasscodeSite builds the App and olive's site.
func NewPasscodeSite(t *testing.T) *PasscodeSite {
	t.Helper()
	return NewPasscodeSiteOn(t, NewApp(t))
}

// NewPasscodeSiteOn is NewPasscodeSite on a.
func NewPasscodeSiteOn(t *testing.T, a *App) *PasscodeSite {
	t.Helper()
	a.EnablePasscodes(t)
	p := &PasscodeSite{App: a, Olive: a.NewPerson(t, "olive")}
	p.Okey = p.Olive.Header()
	p.SiteID = rt.Deploy(t, a.Database, a.Disk, p.Olive, "trip", map[string]string{
		"index.html":      "<h1>trip " + PasscodeCanary + "</h1>",
		"app.js":          "var s='" + PasscodeCanary + "';",
		"data.json":       `{"x":"` + PasscodeCanary + `"}`,
		"img.svg":         `<svg xmlns="http://www.w3.org/2000/svg"><text>` + PasscodeCanary + `</text></svg>`,
		"days/index.html": "day " + PasscodeCanary,
		"404.html":        "missing " + PasscodeCanary,
		"robots.txt":      "User-agent: *\nAllow: /\n# " + PasscodeCanary,
	})
	a.MarkReady(t, p.Olive)
	p.Person = p.Olive.Handle + "." + rt.SiteDomain
	p.Host = p.Olive.SiteHost("trip")
	p.PageDir = a.Disk.SiteDir(p.Olive.ID, "trip")
	return p
}

// FakeProvider is a Google that signs in g-<code> as <code>@example.test.
type FakeProvider struct{}

func (FakeProvider) Name() string { return "google" }
func (FakeProvider) AuthCodeURL(state, _ string) string {
	return "https://accounts.example.test/auth?state=" + url.QueryEscape(state)
}
func (FakeProvider) Exchange(_ context.Context, code, _ string) (ProviderIdentity, error) {
	return ProviderIdentity{Provider: "google", UserID: "g-" + code, Email: code + "@example.test", EmailVerified: true}, nil
}

// SSOOIDCProvider is FakeProvider as a company (OIDC) sign-in.
type SSOOIDCProvider struct{ FakeProvider }

func (SSOOIDCProvider) Name() string { return "oidc" }
func (SSOOIDCProvider) Exchange(ctx context.Context, code, verifier string) (ProviderIdentity, error) {
	id, err := (FakeProvider{}).Exchange(ctx, code, verifier)
	id.Provider = "oidc"
	return id, err
}

// ---- passcodes ----

// PasscodeCanary is in every file of a PasscodeSite.
const PasscodeCanary = "CANARY-7f3a91-do-not-leak"

// PasscodeSite is olive's site "trip" (a canary in every kind of file) on
// an App with passcodes on.
type PasscodeSite struct {
	*App
	Olive        Person
	SiteID, Host string
	Person       string // olive's person host
	Okey         map[string]string
	PageDir      string
}

// Lock sets the site's passcode.
func (p *PasscodeSite) Lock(t *testing.T, code string) Resp {
	t.Helper()
	return p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": code}, p.Okey)
}

// Unlock posts the gate's form from host.
func (p *PasscodeSite) Unlock(t *testing.T, host, code, next string, extra map[string]string) Resp {
	t.Helper()
	form := url.Values{"passcode": {code}, "next": {next}}.Encode()
	h := map[string]string{"Origin": "https://" + host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
	for k, v := range extra {
		h[k] = v
	}
	return p.At(t, "POST", host, "/v1/site-unlock", form, h)
}

// Grant names viewers of the site.
func (p *PasscodeSite) Grant(t *testing.T, emails ...string) Resp {
	t.Helper()
	return p.At(t, "POST", rt.SiteDomain, "/v1/sites/trip/viewers", map[string]any{"emails": emails}, p.Okey)
}

// CookieHeader is the unlock cookie r set, as a Cookie header value.
func CookieHeader(t *testing.T, r Resp) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if strings.Contains(c.Name, "sh_pass_") {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("no unlock cookie in %v", r.Header)
	return ""
}
