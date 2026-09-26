package handler

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
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/oauth"
)

// testNonce is a sign-in nonce whose hash tests store directly.
const testNonce = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testNonceHash(n string) sql.NullString {
	sum := sha256.Sum256([]byte(n))
	return sql.NullString{String: base64.RawURLEncoding.EncodeToString(sum[:]), Valid: true}
}

func nonceCookie(n string) map[string]string {
	return map[string]string{"Cookie": visitorNonceCookieHost + "=" + n}
}

// fakeProvider signs in whoever the callback's code names.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "google" }
func (fakeProvider) AuthCodeURL(state, _ string) string {
	return "https://accounts.example.test/auth?state=" + url.QueryEscape(state)
}
func (fakeProvider) Exchange(_ context.Context, code, _ string) (oauth.Identity, error) {
	return oauth.Identity{Provider: "google", UserID: "g-" + code, Email: code + "@example.test", EmailVerified: true}, nil
}

// withOAuth mounts visitor Google sign-in (fake provider) on the app.
func (a *privateApp) withOAuth(t *testing.T) *OAuthHandler {
	t.Helper()
	oh := &OAuthHandler{
		database:    a.database,
		cfg:         config.Config{PublicBaseURL: "https://" + pcSiteDomain, SiteDomain: pcSiteDomain, ContentHost: pcContentHost, CNAMETarget: "cname." + pcSiteDomain},
		providers:   map[string]oauth.Provider{"google": fakeProvider{}},
		ipLimiter:   newRateLimiter(10000, 10000),
		lookupIP:    func(context.Context, string) ([]net.IP, error) { return nil, nil },
		lookupCNAME: func(context.Context, string) (string, error) { return "cname." + pcSiteDomain + ".", nil },
	}
	oh.SetPersonSiteResolver(a.sites.PersonReturnSite)
	oh.Register(a.mux)
	return oh
}

// signInStart runs the site-host start in a browser and returns the OAuth
// state and the nonce cookie it was given.
func (a *privateApp) signInStart(t *testing.T, host, returnTo string) (state, nonce string) {
	t.Helper()
	r := a.at(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil)
	loc, _ := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusFound || loc == nil || loc.Host != "accounts.example.test" {
		t.Fatalf("start on %s: %d %q %s", host, r.status, r.header.Get("Location"), r.body)
	}
	sc := r.header.Values("Set-Cookie")
	if len(sc) != 1 || !strings.HasPrefix(sc[0], visitorNonceCookieHost+"=") {
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
	nonce = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], visitorNonceCookieHost+"=")
	if strings.Contains(r.header.Get("Location"), nonce) || strings.Contains(r.header.Get("Location"), testNonceHash(nonce).String) {
		t.Errorf("nonce or its hash left the site host: %s", r.header.Get("Location"))
	}
	return loc.Query().Get("state"), nonce
}

// signInCallback completes the provider leg as whoever (in any browser) and
// returns the establish URL it hands back.
func (a *privateApp) signInCallback(t *testing.T, state, who, host string) *url.URL {
	t.Helper()
	r := a.at(t, "GET", pcSiteDomain, "/v1/auth/oauth/google/callback?state="+url.QueryEscape(state)+"&code="+who, nil, nil)
	loc, _ := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusFound || loc == nil || !strings.EqualFold(loc.Host, host) || loc.Path != "/v1/visitor/establish" {
		t.Fatalf("callback: %d %q %s", r.status, r.header.Get("Location"), r.body)
	}
	if len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("callback set a cookie: %v", r.header.Values("Set-Cookie"))
	}
	return loc
}

func TestVisitorSignInBoundToStartingBrowser(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	a.withOAuth(t)
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, oscar, "cafe")
	_, sh := a.userID(t, oscar) // no certificate yet: person-path fallback
	personHost := sh + "." + pcSiteDomain
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	a.deploy(t, olive, "pots")
	_, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	okey := map[string]string{"X-API-Key": olive.key}

	siteHost := "shop." + oh + "." + pcSiteDomain
	claimed := "blog-" + oh + "." + pcSiteDomain
	custom := "pots-" + oh + ".example.test"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/pots/domain", map[string]string{"domain": custom}, okey); r.status != 200 {
		t.Fatalf("bind custom: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_verified_at = now(), domain_status = 'active' WHERE custom_domain = $1`, custom); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, host, site, path string }{
		{"site host", siteHost, "shop", "/page.html?x=1"},
		{"claimed name", claimed, "blog", "/page.html?x=1"},
		{"custom domain", custom, "pots", "/page.html?x=1"},
		{"person host fallback", personHost, "cafe", "/cafe/page.html?x=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host
			returnTo := "https://" + host + tc.path
			// Each case is its own client, clear of the establish rate limit.
			ip := "198.51.100." + map[string]string{"shop": "1", "blog": "2", "pots": "3", "cafe": "4"}[tc.site]
			establish := func(loc *url.URL, headers map[string]string) resp {
				h := map[string]string{"X-Forwarded-For": ip}
				for k, v := range headers {
					h[k] = v
				}
				return a.at(t, "GET", host, loc.RequestURI(), nil, h)
			}
			me := func(sess string) map[string]any {
				return a.at(t, "GET", host, "/v1/sites/"+tc.site+"/me", nil, browser(host, sess)).json(t)
			}

			// Attacker starts and completes sign-in in their own browser, then
			// gets the victim's browser (no nonce) to open the establish link.
			state, attackerNonce := a.signInStart(t, host, returnTo)
			loc := a.signInCallback(t, state, "attacker", host)
			if r := establish(loc, nil); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
				t.Fatalf("no nonce accepted: %d %v", r.status, r.header.Values("Set-Cookie"))
			}
			// Burned: not even the attacker's own browser can use it now.
			if r := establish(loc, nonceCookie(attackerNonce)); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
				t.Fatalf("burned code redeemed: %d", r.status)
			}

			// Victim holds a nonce of their own sign-in: still refused.
			_, victimNonce := a.signInStart(t, host, returnTo)
			state, attackerNonce = a.signInStart(t, host, returnTo)
			loc = a.signInCallback(t, state, "attacker", host)
			if r := establish(loc, nonceCookie(victimNonce)); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
				t.Fatalf("wrong nonce accepted: %d %v", r.status, r.header.Values("Set-Cookie"))
			}
			if r := establish(loc, nonceCookie(attackerNonce)); r.status != http.StatusBadRequest {
				t.Fatalf("code reusable after a wrong nonce: %d", r.status)
			}

			// A plain (non-__Host-) cookie a sibling host could plant does not count.
			state, attackerNonce = a.signInStart(t, host, returnTo)
			loc = a.signInCallback(t, state, "attacker", host)
			if r := establish(loc, map[string]string{"Cookie": visitorNonceCookieHTTP + "=" + attackerNonce}); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
				t.Fatalf("planted plain nonce accepted: %d", r.status)
			}

			// The real flow: same browser from start to establish.
			state, nonce := a.signInStart(t, host, returnTo)
			loc = a.signInCallback(t, state, "visitor", host)
			r := establish(loc, nonceCookie(nonce))
			if r.status != http.StatusFound || r.header.Get("Location") != returnTo {
				t.Fatalf("real establish: %d %q %s", r.status, r.header.Get("Location"), r.body)
			}
			var sess string
			cleared := false
			for _, c := range r.header.Values("Set-Cookie") {
				if strings.HasPrefix(c, visitorCookieHost+"=") {
					sess = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], visitorCookieHost+"=")
				}
				if strings.HasPrefix(c, visitorNonceCookieHost+"=;") && strings.Contains(c, "Max-Age=0") {
					cleared = true
				}
			}
			if sess == "" || !cleared {
				t.Fatalf("establish cookies: %v", r.header.Values("Set-Cookie"))
			}
			if m := me(sess); m["signed_in"] != true || m["email"] != "visitor@example.test" {
				t.Fatalf("/me after sign-in: %v", m)
			}
			if r := establish(loc, nonceCookie(nonce)); r.status != http.StatusBadRequest {
				t.Fatalf("code reused: %d", r.status)
			}
		})
	}

	// The apex start sends a site sign-in to the site host's start; it sets
	// no cookie and issues no state itself.
	returnTo := "https://" + siteHost + "/a?b=c"
	r := a.at(t, "GET", pcSiteDomain, "/v1/auth/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil)
	if want := "https://" + siteHost + "/v1/visitor/oauth/google?return_to=" + url.QueryEscape(returnTo); r.status != http.StatusFound || r.header.Get("Location") != want || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("apex start: %d %q %v", r.status, r.header.Get("Location"), r.header.Values("Set-Cookie"))
	}
	// The site-host start refuses a return_to on another host, and owner flows.
	for _, bad := range []string{"https://" + claimed + "/", "https://" + pcSiteDomain + "/?cn=" + testNonceHash("x").String, "https://evil.example/"} {
		if r := a.at(t, "GET", siteHost, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(bad), nil, nil); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
			t.Errorf("start with return_to %s: %d", bad, r.status)
		}
	}
	// A site state with no nonce (issued before this fix) never yields a code.
	siteID := a.siteID(t, olive, "shop")
	if _, err := a.database.Exec(`INSERT INTO oauth_states (state, provider, code_verifier, return_to, host, site_id, purpose, expires_at) VALUES ('legacy-'||$1, 'google', 'v', $2, $3, $4, 'site', $5)`,
		oh, returnTo, siteHost, siteID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/auth/oauth/google/callback?state=legacy-"+oh+"&code=attacker", nil, nil); r.status != http.StatusBadRequest {
		t.Fatalf("unbound state completed: %d %q", r.status, r.header.Get("Location"))
	}
}
