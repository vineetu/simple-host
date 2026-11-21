package servertest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/httpx"
)

// testNonce is a sign-in nonce whose hash tests store directly.
const TestNonce = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNonceHash(n string) sql.NullString {
	sum := sha256.Sum256([]byte(n))
	return sql.NullString{String: base64.RawURLEncoding.EncodeToString(sum[:]), Valid: true}
}

func NonceCookie(n string) map[string]string {
	return map[string]string{"Cookie": accesshttp.VisitorNonceCookieHost + "=" + n}
}

// fakeProvider signs in whoever the callback's code names.
type FakeProvider struct{}

func (FakeProvider) Name() string { return "google" }
func (FakeProvider) AuthCodeURL(state, _ string) string {
	return "https://accounts.example.test/auth?state=" + url.QueryEscape(state)
}
func (FakeProvider) Exchange(_ context.Context, code, _ string) (accounts.ProviderIdentity, error) {
	return accounts.ProviderIdentity{Provider: "google", UserID: "g-" + code, Email: code + "@example.test", EmailVerified: true}, nil
}

// withOAuth mounts visitor Google sign-in (fake provider) on the app.
func (a *PrivateApp) WithOAuth(t *testing.T) *accesshttp.OAuthHandler {
	t.Helper()
	oh := &accesshttp.OAuthHandler{
		Database:      a.Database,
		Limits:        a.Sites.Limits,
		Cfg:           accesshttp.OAuthSettings{PublicBaseURL: "https://" + PcSiteDomain, SiteDomain: PcSiteDomain, ContentHost: PcContentHost, CNAMETarget: "cname." + PcSiteDomain},
		Accounts:      accounts.New(a.Database),
		SignIn:        accounts.NewProviderSignIn(accounts.New(a.Database), map[string]accounts.Provider{"google": FakeProvider{}}),
		IPLimiter:     httpx.NewRateLimiter(10000, 10000),
		SSOKeyLimiter: httpx.NewRateLimiter(10000, 10000),
		LookupIP:      func(context.Context, string) ([]net.IP, error) { return nil, nil },
		LookupCNAME:   func(context.Context, string) (string, error) { return "cname." + PcSiteDomain + ".", nil },
	}
	oh.SetPersonSiteResolver(a.Sites.Hosts.ReturnSite)
	oh.SetPasscodeCheck(a.Access.PasscodeLetsIn)
	oh.Register(a.Mux)
	return oh
}

// signInStart runs the site-host start in a browser and returns the OAuth
// state and the nonce cookie it was given.
func (a *PrivateApp) SignInStart(t *testing.T, host, returnTo string) (state, nonce string) {
	t.Helper()
	r := a.At(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil)
	loc, _ := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || loc == nil || loc.Host != "accounts.example.test" {
		t.Fatalf("start on %s: %d %q %s", host, r.Status, r.Header.Get("Location"), r.Body)
	}
	sc := r.Header.Values("Set-Cookie")
	if len(sc) != 1 || !strings.HasPrefix(sc[0], accesshttp.VisitorNonceCookieHost+"=") {
		t.Fatalf("start cookie: %v", sc)
	}
	c := sc[0]
	for _, want := range []string{"Path=/", "Max-Age=600", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(c, want) {
			t.Errorf("nonce cookie lacks %s: %s", want, c)
		}
	}
	if strings.Contains(strings.ToLower(c), "domain=") {
		t.Errorf("nonce cookie has a Domain: %s", c)
	}
	nonce = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], accesshttp.VisitorNonceCookieHost+"=")
	if strings.Contains(r.Header.Get("Location"), nonce) || strings.Contains(r.Header.Get("Location"), TestNonceHash(nonce).String) {
		t.Errorf("nonce or its hash left the site host: %s", r.Header.Get("Location"))
	}
	return loc.Query().Get("state"), nonce
}

// signInCallback completes the provider leg as whoever (in any browser) and
// returns the establish URL it hands back.
func (a *PrivateApp) SignInCallback(t *testing.T, state, who, host string) *url.URL {
	t.Helper()
	r := a.At(t, "GET", PcSiteDomain, "/v1/auth/oauth/google/callback?state="+url.QueryEscape(state)+"&code="+who, nil, nil)
	loc, _ := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || loc == nil || !strings.EqualFold(loc.Host, host) || loc.Path != "/v1/visitor/establish" {
		t.Fatalf("callback: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	if len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("callback set a cookie: %v", r.Header.Values("Set-Cookie"))
	}
	return loc
}
