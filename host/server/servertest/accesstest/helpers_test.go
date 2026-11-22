package accesstest

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	. "github.com/vsriram/simple-host/host/access/featuretest"
	"github.com/vsriram/simple-host/host/access/gate"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// SetLimits gives every Access handler of the fixture the limits l.
func (a *App) SetLimits(l config.Limits) {
	a.Access.Limits, a.Access.Passcode.Limits = l, l
	a.Users.SetLimits(l)
	a.Conn.Limits = l
}

// NewAccountApp is an App with a capturing mailbox, synchronous sign-in
// alerts and a roomy email limiter.
func NewAccountApp(t *testing.T, eds ...*edition.Edition) (*App, *Mailbox) {
	t.Helper()
	mb := &Mailbox{}
	a := NewAppMailer(t, mb, eds...)
	a.Users.Alerts.Inline = true
	a.Users.EmailLimiter.Capacity, a.Users.EmailLimiter.Rate = 1000, 1000
	return a, mb
}

// WithOAuth registers the OAuth sign-in routes with FakeProvider as Google.
func (a *App) WithOAuth(t *testing.T) *accesshttp.OAuthHandler {
	t.Helper()
	oh := &accesshttp.OAuthHandler{
		Database:      a.Database,
		Limits:        a.Access.Limits,
		Cfg:           accesshttp.OAuthSettings{PublicBaseURL: "https://" + rt.SiteDomain, SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, CNAMETarget: "cname." + rt.SiteDomain},
		Accounts:      accounts.New(a.Database),
		SignIn:        gate.NewProviderSignIn(accounts.New(a.Database), map[string]gate.Provider{"google": FakeProvider{}}),
		IPLimiter:     httpx.NewRateLimiter(10000, 10000),
		SSOKeyLimiter: httpx.NewRateLimiter(10000, 10000),
		LookupIP:      func(context.Context, string) ([]net.IP, error) { return nil, nil },
		LookupCNAME:   func(context.Context, string) (string, error) { return "cname." + rt.SiteDomain + ".", nil },
		Access:        a.Access,
	}
	oh.Register(a.Mux)
	return oh
}

// newSSOApp is an App with OAuth sign-in and the SSO hop on the app's own address.
func newSSOApp(t *testing.T) (*App, *accesshttp.OAuthHandler) {
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
func (FakeProvider) Exchange(_ context.Context, code, _ string) (gate.ProviderIdentity, error) {
	return gate.ProviderIdentity{Provider: "google", UserID: "g-" + code, Email: code + "@example.test", EmailVerified: true}, nil
}

// SSOOIDCProvider is FakeProvider as a company (OIDC) sign-in.
type SSOOIDCProvider struct{ FakeProvider }

func (SSOOIDCProvider) Name() string { return "oidc" }
func (SSOOIDCProvider) Exchange(ctx context.Context, code, verifier string) (gate.ProviderIdentity, error) {
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
