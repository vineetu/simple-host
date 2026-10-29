package handler

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/oauth"
)

func newSSOApp(t *testing.T) (*privateApp, string, *OAuthHandler) {
	t.Helper()
	a, dir := newSiteApp(t, "canonical")
	o := a.withOAuth(t)
	o.cfg.PublicBaseURL = a.srv.URL
	a.sites.SetSSOHop(o)
	return a, dir, o
}
func responseCookie(t *testing.T, r resp, name string) string {
	t.Helper()
	res := http.Response{Header: r.header}
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("missing %s cookie (status %d)", name, r.status)
	return ""
}
func (a *privateApp) ssoPass(t *testing.T, key string) string {
	t.Helper()
	r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"})
	if r.status != 204 {
		t.Fatalf("bootstrap: %d %s", r.status, r.body)
	}
	return responseCookie(t, r, ssoCookieHost)
}
func (a *privateApp) ssoStartAt(t *testing.T, host string) (state, nonce string) {
	t.Helper()
	r := a.at(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+host+"/page?ok=1"), nil, nil)
	if r.status != 302 {
		t.Fatalf("start: %d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.header.Get("Location"))
	return u.Query().Get("state"), responseCookie(t, r, visitorNonceCookieHost)
}
func (a *privateApp) ssoHop(t *testing.T, state, pass string) resp {
	return a.at(t, "GET", a.appHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass})
}
func consentToken(t *testing.T, r resp) string {
	t.Helper()
	m := regexp.MustCompile(`name="form_token" value="([^"]+)"`).FindSubmatch(r.body)
	if len(m) != 2 {
		t.Fatalf("not consent: %d %s", r.status, r.body)
	}
	return string(m[1])
}
func (a *privateApp) ssoConsent(t *testing.T, state, pass, token, origin, fetchSite string) resp {
	return a.at(t, "POST", a.appHost(), "/v1/sso/consent", url.Values{"state": {state}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": ssoCookieHost + "=" + pass, "Origin": origin, "Sec-Fetch-Site": fetchSite})
}
func (a *privateApp) finishSSO(t *testing.T, host, pass string) (string, string) {
	t.Helper()
	state, nonce := a.ssoStartAt(t, host)
	r := a.ssoHop(t, state, pass)
	if r.status == 200 {
		r = a.ssoConsent(t, state, pass, consentToken(t, r), "https://"+a.appHost(), "same-origin")
	}
	if r.status != 302 {
		t.Fatalf("hop: %d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.header.Get("Location"))
	r = a.at(t, "GET", host, u.RequestURI(), nil, nonceCookie(nonce))
	if r.status != 302 {
		t.Fatalf("establish: %d %s", r.status, r.body)
	}
	if r.header.Get("Location") != "https://"+host+"/page?ok=1" {
		t.Fatalf("return changed: %s", r.header.Get("Location"))
	}
	return responseCookie(t, r, visitorCookieHost), state
}
func TestSSOHopConsentAndRevocation(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "ssoowner")
	viewer := a.newPerson(t, "ssoviewer")
	a.deploy(t, owner, "one")
	a.deploy(t, owner, "two")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	one, two := "one."+handle+"."+pcSiteDomain, "two."+handle+"."+pcSiteDomain
	pass := a.ssoPass(t, viewer.key)
	state, nonce := a.ssoStartAt(t, one)
	r := a.ssoHop(t, state, pass)
	form := consentToken(t, r)
	if !strings.Contains(r.header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || r.header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("consent privacy headers")
	}
	for _, tc := range []struct{ name, token, origin, site string }{{"wrong token", "bad", "https://" + a.appHost(), "same-origin"}, {"sibling", form, "https://" + two, "same-site"}, {"opaque", form, "null", "cross-site"}, {"no origin", form, "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			r := a.ssoConsent(t, state, pass, tc.token, tc.origin, tc.site)
			if r.status != 403 {
				t.Fatalf("CSRF accepted: %d", r.status)
			}
		})
	}
	r = a.ssoConsent(t, state, pass, form, "null", "same-origin")
	if r.status != 302 {
		t.Fatalf("consent: %d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.header.Get("Location"))
	r = a.at(t, "GET", one, u.RequestURI(), nil, nonceCookie(nonce))
	if r.status != 302 {
		t.Fatalf("establish: %d %s", r.status, r.body)
	}
	first := responseCookie(t, r, visitorCookieHost)
	uid, _ := a.userID(t, viewer)
	site := a.siteID(t, owner, "one")
	denied := a.at(t, "DELETE", a.appHost(), "/v1/sso/consents/"+site, nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"})
	if denied.status != 401 {
		t.Fatal("cookie revoked consent without an account key")
	}
	forgotten := a.at(t, "DELETE", a.appHost(), "/v1/sso/consents/"+site, nil, map[string]string{"X-API-Key": viewer.key, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"})
	if forgotten.status != 204 {
		t.Fatalf("forget consent: %d %s", forgotten.status, forgotten.body)
	}
	stateAgain, _ := a.ssoStartAt(t, one)
	if page := a.ssoHop(t, stateAgain, pass); page.status != 200 {
		t.Fatal("revoked consent silently reused")
	}
	if err := db.RememberSSOConsent(context.Background(), a.database, uid, site); err != nil {
		t.Fatal(err)
	}
	if r := a.ssoHop(t, state, pass); r.status != 400 {
		t.Fatal("state replay")
	}
	state, _ = a.ssoStartAt(t, one)
	if r := a.ssoHop(t, state, pass); r.status != 302 {
		t.Fatal("consent not remembered")
	}
	second, _ := a.finishSSO(t, one, pass)
	third, _ := a.finishSSO(t, two, pass)
	if first == second || second == third {
		t.Fatal("session fixation")
	}
	for _, raw := range []string{first, second, third} {
		id, _ := hex.DecodeString(raw)
		s, err := db.GetVisitorSession(context.Background(), a.database, id)
		if err != nil || s.SSOPassHash.String != db.HashAPIKey(pass) {
			t.Fatalf("parent: %v", err)
		}
	}
	r = a.at(t, "POST", one, "/v1/visitor/logout", nil, map[string]string{"Cookie": visitorCookieHost + "=" + second, "X-SH-CSRF": "1", "Origin": "https://" + one, "Sec-Fetch-Site": "same-origin"})
	if r.status != 200 {
		t.Fatalf("logout: %d %s", r.status, r.body)
	}
	dest := r.json(t)["redirect_url"].(string)
	u, _ = url.Parse(dest)
	r = a.at(t, "GET", a.appHost(), u.RequestURI(), nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass})
	if r.status != 302 || responseCookie(t, r, ssoCookieHost) != "" {
		t.Fatal("apex cookie not cleared")
	}
	for _, raw := range []string{first, second, third} {
		id, _ := hex.DecodeString(raw)
		if _, err := db.GetVisitorSession(context.Background(), a.database, id); err != sql.ErrNoRows {
			t.Fatal("sibling session survived")
		}
	}
	if r := a.at(t, "GET", a.appHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.key}); r.status != 401 {
		t.Fatalf("dashboard key survived: %d", r.status)
	}
}
func TestSSONonceReplayExpiryAndRedirect(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "nonceowner")
	viewer := a.newPerson(t, "nonceviewer")
	a.deploy(t, owner, "one")
	_, h := a.userID(t, owner)
	markReady(t, dir, h)
	host := "one." + h + "." + pcSiteDomain
	pass := a.ssoPass(t, viewer.key)
	uid, _ := a.userID(t, viewer)
	site := a.siteID(t, owner, "one")
	if err := db.RememberSSOConsent(context.Background(), a.database, uid, site); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "wrong", "plain", "host", "expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			state, nonce := a.ssoStartAt(t, host)
			r := a.ssoHop(t, state, pass)
			if r.status != 302 {
				t.Fatalf("hop: %d", r.status)
			}
			u, _ := url.Parse(r.header.Get("Location"))
			headers := nonceCookie(nonce)
			target := host
			switch mode {
			case "missing":
				headers = nil
			case "wrong":
				headers = nonceCookie("attacker")
			case "plain":
				headers = map[string]string{"Cookie": visitorNonceCookieHTTP + "=" + nonce}
			case "host":
				target = a.appHost()
			case "expired":
				a.database.Exec(`UPDATE visitor_establish_tokens SET expires_at=now()-interval '1 second' WHERE once=$1`, u.Query().Get("once"))
			case "revoked":
				a.database.Exec(`DELETE FROM sso_passes WHERE id_hash=$1`, db.HashAPIKey(pass))
			}
			r = a.at(t, "GET", target, u.RequestURI(), nil, headers)
			if r.status == 302 {
				t.Fatal("unbound code accepted")
			}
			r = a.at(t, "GET", host, u.RequestURI(), nil, nonceCookie(nonce))
			if r.status != 400 {
				t.Fatalf("code replay: %d", r.status)
			}
		})
	}
	for _, bad := range []string{"https://evil.test/", "https://" + host + ".evil.test/", "https://" + host + "@evil.test/"} {
		r := a.at(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape(bad), nil, nil)
		if r.status != 400 {
			t.Fatalf("bad return accepted: %d", r.status)
		}
	}
}
func TestSSONamedViewerAutoHopAndParentExpiry(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "namedowner")
	viewer := a.newPerson(t, "namedviewer")
	a.deploy(t, owner, "named")
	_, h := a.userID(t, owner)
	markReady(t, dir, h)
	host := "named." + h + "." + pcSiteDomain
	site := a.siteID(t, owner, "named")
	uid, _ := a.userID(t, viewer)
	if _, err := a.database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, site, viewer.email); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	ownerID, _ := a.userID(t, owner)
	if err := a.sites.disk.SetPasscodeMarker(ownerID, "named", true); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "GET", host, "/", nil, nil)
	if r.status != 302 || !strings.Contains(r.header.Get("Location"), "/v1/sso/hop?") {
		t.Fatalf("auto hop: %d %s", r.status, r.body)
	}
	pass := a.ssoPass(t, viewer.key)
	u, _ := url.Parse(r.header.Get("Location"))
	if r = a.ssoHop(t, u.Query().Get("state"), pass); r.status != 302 {
		t.Fatalf("named viewer saw consent: %d", r.status)
	}
	raw, _ := a.finishSSO(t, host, pass)
	id, _ := hex.DecodeString(raw)
	for _, q := range []string{`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE user_id=$1`, `UPDATE api_keys SET expires_at=now()-interval '1 second' WHERE user_id=$1`, `UPDATE users SET suspended_at=now() WHERE id=$1`} {
		if _, err := a.database.Exec(q, uid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.GetSSOPass(context.Background(), a.database, db.HashAPIKey(pass)); err != sql.ErrNoRows {
			t.Fatal("expired/suspended pass accepted")
		}
		if _, err := db.GetVisitorSession(context.Background(), a.database, id); err != sql.ErrNoRows {
			t.Fatal("expired/suspended parent accepted on site")
		}
		a.database.Exec(`UPDATE sso_passes SET idle_expires_at=now()+interval '1 day' WHERE user_id=$1`, uid)
		a.database.Exec(`UPDATE api_keys SET expires_at=NULL WHERE user_id=$1`, uid)
		a.database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, uid)
	}
	r = a.at(t, "POST", a.appHost(), "/v1/me/sign-out", nil, map[string]string{"X-API-Key": viewer.key})
	if r.status != 204 {
		t.Fatalf("dashboard logout: %d", r.status)
	}
	if _, err := db.GetVisitorSession(context.Background(), a.database, id); err != sql.ErrNoRows {
		t.Fatal("dashboard logout left a site session")
	}
}
func TestSSOCookieBoundary(t *testing.T) {
	// A source-level tripwire supplements the behavioral check: a future
	// handler cannot casually acquire the cookie outside the SSO namespace.
	for _, root := range []string{".", "../auth"} {
		files, _ := filepath.Glob(root + "/*.go")
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || filepath.Base(file) == "sso.go" {
				continue
			}
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range []string{"__Host-sh_sso", `"sh_sso"`, "ssoCookie(", "ssoCookieName(", "ssoCookieHost", "ssoCookieHTTP"} {
				if strings.Contains(string(body), needle) {
					t.Fatalf("SSO cookie reader escaped into %s", file)
				}
			}
		}
	}
	for _, https := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://app.test/v1/sso/hop", nil)
		if https {
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		w := httptest.NewRecorder()
		setSSOCookie(w, r, "secret", 60)
		c := w.Result().Cookies()[0]
		if !c.HttpOnly || c.Secure != https || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
			t.Fatal("cookie scope")
		}
	}
	r := httptest.NewRequest("GET", "https://app.test/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: ssoCookieHost, Value: "secret"})
	if ssoCookie(r) != "" {
		t.Fatal("reader outside SSO")
	}
	called := false
	w := httptest.NewRecorder()
	auth.Middleware("admin", "id", nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
	if called || w.Code != 401 {
		t.Fatal("middleware accepted SSO cookie")
	}
}
func TestSSOReturnAllowlist(t *testing.T) {
	for _, base := range []string{"https://simple-host.app", "https://simple-hack.app", "https://company.test"} {
		u, _ := url.Parse(base + "/v1/sso/hop?state=" + strings.Repeat("a", 64) + "&cn=" + testNonceHash(testNonce).String)
		if !ssoReturnToOK(u, base) {
			t.Fatal("provider return rejected")
		}
		u.RawQuery += "&return_to=https://evil.test"
		if ssoReturnToOK(u, base) {
			t.Fatal("provider open redirect")
		}
	}
}

func TestSSOStartGatesAndCookieIsolation(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.newPerson(t, "gatesowner")
	a.deploy(t, owner, "gated")
	uid, h := a.userID(t, owner)
	markReady(t, dir, h)
	host := "gated." + h + "." + pcSiteDomain
	site := a.siteID(t, owner, "gated")
	key := map[string]string{"X-API-Key": owner.key}
	pass := a.ssoPass(t, owner.key)
	start := "/v1/sso/start?return_to=" + url.QueryEscape("https://"+host+"/")
	if r := a.at(t, "GET", host, "/v1/sso/hop?state=anything", nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass}); r.status != 403 {
		t.Fatal("site host read apex pass")
	}
	for _, sfs := range []string{"same-site", "cross-site"} {
		r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.key, "Origin": "https://" + host, "Sec-Fetch-Site": sfs})
		if r.status != 403 {
			t.Fatal("cross-origin bootstrap")
		}
	}
	for _, dest := range []string{"iframe", "image", "empty"} {
		r := a.at(t, "GET", host, start, nil, map[string]string{"Sec-Fetch-Dest": dest, "Sec-Fetch-Site": "same-origin"})
		if r.status != 400 {
			t.Fatal("non-navigation hop")
		}
	}
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "PUT", a.appHost(), "/v1/sites/gated/lock", map[string]string{"passcode": "secret-passcode"}, key); r.status != 200 {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, start, nil, nil); r.status != 403 {
		t.Fatalf("locked hop: %d", r.status)
	}
	r := a.at(t, "POST", host, "/v1/site-unlock", url.Values{"passcode": {"secret-passcode"}, "next": {"/"}}.Encode(), map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin", "Content-Type": "application/x-www-form-urlencoded"})
	if r.status != 303 {
		t.Fatalf("unlock: %d %s", r.status, r.body)
	}
	unlock := strings.Split(r.header.Get("Set-Cookie"), ";")[0]
	r = a.at(t, "GET", host, start, nil, map[string]string{"Cookie": unlock})
	if r.status != 302 {
		t.Fatalf("unlocked hop: %d %s", r.status, r.body)
	}
	// The unlocked start cannot be used if the owner changes the code while
	// the browser is on the apex.
	u, _ := url.Parse(r.header.Get("Location"))
	nonce := responseCookie(t, r, visitorNonceCookieHost)
	hop := a.ssoHop(t, u.Query().Get("state"), pass)
	hop = a.ssoConsent(t, u.Query().Get("state"), pass, consentToken(t, hop), "https://"+a.appHost(), "same-origin")
	if hop.status != 302 {
		t.Fatalf("unlocked consent: %d", hop.status)
	}
	a.at(t, "PUT", a.appHost(), "/v1/sites/gated/lock", map[string]string{"passcode": "changed-passcode"}, key)
	dest, _ := url.Parse(hop.header.Get("Location"))
	r = a.at(t, "GET", host, dest.RequestURI(), nil, map[string]string{"Cookie": unlock + "; " + visitorNonceCookieHost + "=" + nonce})
	if r.status != 403 {
		t.Fatalf("changed lock bypassed: %d", r.status)
	}
	a.at(t, "DELETE", a.appHost(), "/v1/sites/gated/lock", nil, key)
	for _, q := range []string{`UPDATE sites SET offline_at=now() WHERE id=$1`, `UPDATE sites SET suspended_at=now() WHERE id=$1`} {
		if _, err := a.database.Exec(q, site); err != nil {
			t.Fatal(err)
		}
		if r := a.at(t, "GET", host, start, nil, nil); r.status == 302 {
			t.Fatal("unavailable site hop")
		}
		a.database.Exec(`UPDATE sites SET offline_at=NULL,suspended_at=NULL WHERE id=$1`, site)
	}
	// A plain cookie on HTTPS must not substitute for the apex __Host- pass.
	state, _ := a.ssoStartAt(t, host)
	r = a.at(t, "GET", a.appHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": ssoCookieHTTP + "=" + pass})
	if r.status != 200 || !strings.Contains(string(r.body), "SHSignIn.mount") {
		t.Fatal("plain apex cookie accepted")
	}
	// A missing or expired parent and a suspended account cannot bootstrap.
	a.database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, uid)
	if r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.key, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"}); r.status != 401 {
		t.Fatal("suspended bootstrap")
	}
	a.database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, uid)
	a.database.Exec(`UPDATE api_keys SET scope='deploy' WHERE key_hash=$1`, db.HashAPIKey(owner.key))
	if r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.key, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"}); r.status != 401 {
		t.Fatal("deploy key bootstrapped SSO")
	}
	a.database.Exec(`UPDATE api_keys SET scope='full' WHERE key_hash=$1`, db.HashAPIKey(owner.key))
	o.ipLimiter.mu.Lock()
	o.ipLimiter.capacity = 0
	o.ipLimiter.rate = 0
	o.ipLimiter.buckets = map[string]*tokenBucket{}
	o.ipLimiter.mu.Unlock()
	if r := a.at(t, "GET", host, start, nil, nil); r.status != 429 {
		t.Fatalf("IP limiter: %d", r.status)
	}
	o.ipLimiter.mu.Lock()
	o.ipLimiter.capacity = 10000
	o.ipLimiter.buckets = map[string]*tokenBucket{}
	o.ipLimiter.mu.Unlock()

	// DNS proof is mandatory for a custom host, even with a stored binding.
	a.database.Exec(`UPDATE sites SET custom_domain='unproved.example.test',domain_verified_at=now() WHERE id=$1`, site)
	o.lookupCNAME = func(context.Context, string) (string, error) { return "elsewhere.example.test", nil }
	r = a.at(t, "GET", "unproved.example.test", "/v1/sso/start?return_to=https%3A%2F%2Funproved.example.test%2F", nil, nil)
	if r.status == 302 {
		t.Fatal("unproved custom domain accepted")
	}
}

func TestSSOProviderAndEmailSignup(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.newPerson(t, "providerowner")
	a.deploy(t, owner, "page")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "page." + handle + "." + pcSiteDomain
	state, _ := a.ssoStartAt(t, host)
	ret := a.srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + testNonceHash(testNonce).String
	r := a.at(t, "GET", a.appHost(), "/v1/auth/oauth/google?return_to="+url.QueryEscape(ret), nil, nil)
	if r.status != 302 {
		t.Fatalf("provider start: %d %s", r.status, r.body)
	}
	u, _ := url.Parse(r.header.Get("Location"))
	r = a.at(t, "GET", a.appHost(), "/v1/auth/oauth/google/callback?state="+u.Query().Get("state")+"&code=sso-provider-"+handle, nil, nil)
	if r.status != 302 {
		t.Fatalf("provider callback: %d %s", r.status, r.body)
	}
	u, _ = url.Parse(r.header.Get("Location"))
	if u.Path != "/v1/sso/hop" || u.Query().Get("state") != state {
		t.Fatal("provider lost hop")
	}
	token := u.Query().Get("token")
	r = a.at(t, "POST", a.appHost(), "/v1/auth/verify", map[string]string{"token": token, "nonce": testNonce, "sso_state": state, "name": "dashboard sign-in"}, nil)
	if r.status != 200 {
		t.Fatalf("provider verify: %d %s", r.status, r.body)
	}
	pass := a.ssoPass(t, r.json(t)["api_key"].(string))
	p, err := db.GetSSOPass(context.Background(), a.database, db.HashAPIKey(pass))
	if err != nil {
		t.Fatal(err)
	}
	var source string
	err = a.database.QueryRow(`SELECT signup_source FROM users WHERE id=$1`, p.UserID).Scan(&source)
	if err != nil || source != "visitor_sso" {
		t.Fatalf("source: %s %v", source, err)
	}
	var sites int
	a.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1`, p.UserID).Scan(&sites)
	if sites != 0 {
		t.Fatal("visitor got sites")
	}
	owners, err := db.ListSiteOwners(context.Background(), a.database)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range owners {
		if owner.ID == p.UserID {
			t.Fatal("visitor counted as signup with a site")
		}
	}
	// Both methods use the apex account handlers' existing local geo block.
	const ip = "198.51.100.13"
	geo := buildGeo(t, ip, "IQ")
	a.users.SetSignupGeoBlock(geo, []string{"IQ"})
	o.SetSignupGeoBlock(geo, []string{"IQ"})
	for _, path := range []string{"/v1/auth", "/v1/auth/oauth/google/callback?state=x&code=x"} {
		method := "POST"
		var body any = map[string]string{"email": "blocked@example.test"}
		if strings.Contains(path, "callback") {
			method = "GET"
			body = nil
		}
		r := a.at(t, method, a.appHost(), path, body, map[string]string{"X-Forwarded-For": ip})
		if r.status != 403 {
			t.Fatalf("geo block: %s %d", path, r.status)
		}
	}
}

func TestSSOHackEventAndTeamHosts(t *testing.T) {
	a := newTeamSiteApp(t)
	organiser, member := a.newPerson(t, "ssohackorg"), a.newPerson(t, "ssohackmember")
	event := a.makeEvent(t, organiser)
	a.join(t, event, member, organiser)
	team, _ := a.startTeam(t, event, "SSO team", member)
	markReady(t, a.certDir, event)
	wantTS(t, "team", a.deployTeam(t, team, a.teamKey(t, event, member), "team"), 201, "")
	wantTS(t, "event website", a.api(t, "PUT", "/v1/hack/events/"+event+"/website/files?create=1", deployBody("event"), organiser.key), 201, "")
	o := NewOAuthHandler(a.database, config.Config{PublicBaseURL: a.srv.URL, SiteDomain: tsDomain, ContentHost: "sites." + tsDomain})
	o.SetPersonSiteResolver(a.sites.PersonReturnSite)
	o.SetPasscodeCheck(a.sites.PasscodeLetsIn)
	a.sites.SetSSOHop(o)
	o.Register(a.mux)
	pa := &privateApp{connectorApp: a.connectorApp, sites: a.sites, users: a.users, mux: a.mux}
	pass := pa.ssoPass(t, member.key)
	for _, host := range []string{team + "." + event + "." + tsDomain, event + "." + tsDomain} {
		raw, _ := pa.finishSSO(t, host, pass)
		id, _ := hex.DecodeString(raw)
		sess, err := db.GetVisitorSession(context.Background(), a.database, id)
		if err != nil || sess.Host != host {
			t.Fatalf("Hack site pass: %v", err)
		}
	}
}

type ssoOIDCProvider struct{ fakeProvider }

func (ssoOIDCProvider) Name() string { return "oidc" }
func (ssoOIDCProvider) Exchange(ctx context.Context, code, verifier string) (oauth.Identity, error) {
	id, err := (fakeProvider{}).Exchange(ctx, code, verifier)
	id.Provider = "oidc"
	return id, err
}
func TestSSOCompanyProvider(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.newPerson(t, "ssooidcowner")
	a.deploy(t, owner, "portal")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "portal." + handle + "." + pcSiteDomain
	o.providers["oidc"] = ssoOIDCProvider{}
	state, _ := a.ssoStartAt(t, host)
	ret := a.srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + testNonceHash(testNonce).String
	r := a.at(t, "GET", a.appHost(), "/v1/auth/oauth/oidc?return_to="+url.QueryEscape(ret), nil, nil)
	if r.status != 302 {
		t.Fatal("company provider start")
	}
	u, _ := url.Parse(r.header.Get("Location"))
	r = a.at(t, "GET", a.appHost(), "/v1/auth/oauth/oidc/callback?state="+u.Query().Get("state")+"&code=company-"+handle, nil, nil)
	if r.status != 302 {
		t.Fatalf("OIDC callback: %d", r.status)
	}
	u, _ = url.Parse(r.header.Get("Location"))
	if u.Path != "/v1/sso/hop" {
		t.Fatal("OIDC lost hop")
	}
	r = a.at(t, "POST", a.appHost(), "/v1/auth/verify", map[string]string{"token": u.Query().Get("token"), "nonce": testNonce, "sso_state": state}, nil)
	if r.status != 200 {
		t.Fatalf("OIDC verify: %d", r.status)
	}
	pass := a.ssoPass(t, r.json(t)["api_key"].(string))
	a.finishSSO(t, host, pass)
}

func TestSSOReplacementRetiresBrowserFamily(t *testing.T) {
	for _, switchPerson := range []bool{false, true} {
		t.Run(map[bool]string{false: "same account", true: "another account"}[switchPerson], func(t *testing.T) {
			a, dir, _ := newSSOApp(t)
			owner := a.newPerson(t, "replacementowner")
			viewer := a.newPerson(t, "replacementviewer")
			a.deploy(t, owner, "page")
			_, handle := a.userID(t, owner)
			markReady(t, dir, handle)
			host := "page." + handle + "." + pcSiteDomain
			pass := a.ssoPass(t, viewer.key)
			oldSession, _ := a.finishSSO(t, host, pass)
			uid, _ := a.userID(t, viewer)
			if switchPerson {
				other := a.newPerson(t, "replacementother")
				uid, _ = a.userID(t, other)
			}
			key, err := auth.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AddSignInKey(context.Background(), a.database, uid, key, "dashboard sign-in"); err != nil {
				t.Fatal(err)
			}
			r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Cookie": ssoCookieHost + "=" + pass, "Origin": "https://" + a.appHost(), "Sec-Fetch-Site": "same-origin"})
			if r.status != 204 {
				t.Fatalf("replace: %d", r.status)
			}
			nextPass := responseCookie(t, r, ssoCookieHost)
			id, _ := hex.DecodeString(oldSession)
			if _, err := db.GetVisitorSession(context.Background(), a.database, id); err != sql.ErrNoRows {
				t.Fatalf("old family survived: %v", err)
			}
			if r := a.at(t, "GET", a.appHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.key}); r.status != 401 {
				t.Fatal("old key survived")
			}
			newSession, _ := a.finishSSO(t, host, nextPass)
			r = a.at(t, "POST", host, "/v1/visitor/logout", nil, map[string]string{"Cookie": visitorCookieHost + "=" + newSession, "X-SH-CSRF": "1", "Origin": "https://" + host})
			if r.status != 200 {
				t.Fatalf("logout: %d", r.status)
			}
			id, _ = hex.DecodeString(newSession)
			if _, err := db.GetVisitorSession(context.Background(), a.database, id); err != sql.ErrNoRows {
				t.Fatal("new family survived logout")
			}
		})
	}
}

func TestSSOSignoutWithoutLiveLocalSession(t *testing.T) {
	for _, mode := range []string{"expired", "missing", "legacy", "different apex family"} {
		t.Run(mode, func(t *testing.T) {
			a, dir, _ := newSSOApp(t)
			owner := a.newPerson(t, "logoutowner")
			viewer := a.newPerson(t, "logoutviewer")
			a.deploy(t, owner, "one")
			a.deploy(t, owner, "two")
			_, handle := a.userID(t, owner)
			markReady(t, dir, handle)
			one, two := "one."+handle+"."+pcSiteDomain, "two."+handle+"."+pcSiteDomain
			pass := a.ssoPass(t, viewer.key)
			local, _ := a.finishSSO(t, one, pass)
			sibling, _ := a.finishSSO(t, two, pass)
			id, _ := hex.DecodeString(local)
			var err error
			switch mode {
			case "expired":
				_, err = a.database.Exec(`UPDATE visitor_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
			case "missing":
				_, err = a.database.Exec(`DELETE FROM visitor_sessions WHERE id=$1`, id)
			case "legacy":
				_, err = a.database.Exec(`UPDATE visitor_sessions SET sso_pass_hash=NULL WHERE id=$1`, id)
			case "different apex family":
				other := a.newPerson(t, "logoutother")
				viewer = other
				pass = a.ssoPass(t, other.key)
				sibling, _ = a.finishSSO(t, two, pass)
			}
			if err != nil {
				t.Fatal(err)
			}
			r := a.at(t, "POST", one, "/v1/visitor/logout", nil, map[string]string{"Cookie": visitorCookieHost + "=" + local, "X-SH-CSRF": "1", "Origin": "https://" + one})
			if r.status != 200 {
				t.Fatalf("logout must navigate: %d", r.status)
			}
			u, err := url.Parse(r.json(t)["redirect_url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			r = a.at(t, "GET", a.appHost(), u.RequestURI(), nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass})
			if r.status != 302 || responseCookie(t, r, ssoCookieHost) != "" || r.header.Get("Location") != "https://"+one+"/" {
				t.Fatal("apex did not complete sign-out")
			}
			id, _ = hex.DecodeString(sibling)
			if _, err := db.GetVisitorSession(context.Background(), a.database, id); err != sql.ErrNoRows {
				t.Fatal("sibling survived")
			}
			if r := a.at(t, "GET", a.appHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.key}); r.status != 401 {
				t.Fatal("apex key survived")
			}
			if r := a.at(t, "GET", a.appHost(), u.RequestURI(), nil, nil); r.status != 400 {
				t.Fatal("ticket replayed")
			}
		})
	}
}

func TestSSOSessionIssuanceLimits(t *testing.T) {
	for _, by := range []string{"IP", "key"} {
		t.Run(by, func(t *testing.T) {
			a, _, o := newSSOApp(t)
			one, two := a.newPerson(t, "limitone"), a.newPerson(t, "limittwo")
			o.ipLimiter.capacity = 1
			o.ipLimiter.rate = 0
			o.ssoKeyLimiter.capacity = 1
			o.ssoKeyLimiter.rate = 0
			request := func(key, ip string) resp {
				return a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "X-Forwarded-For": ip, "Origin": "https://" + a.appHost()})
			}
			if r := request(one.key, "192.0.2.1"); r.status != 204 {
				t.Fatalf("first: %d", r.status)
			}
			key, ip := two.key, "192.0.2.1"
			if by == "key" {
				key, ip = one.key, "192.0.2.2"
			}
			if r := request(key, ip); r.status != 429 || r.header.Get("Retry-After") == "" {
				t.Fatalf("limit: %d", r.status)
			}
		})
	}
}

func TestSSOSessionRetainsOnePassPerKey(t *testing.T) {
	a, _, _ := newSSOApp(t)
	p := a.newPerson(t, "boundedpasses")
	pass := a.ssoPass(t, p.key)
	r := a.at(t, "POST", a.appHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": p.key, "Cookie": ssoCookieHost + "=" + pass, "Origin": "https://" + a.appHost()})
	if r.status != 204 || r.header.Get("Set-Cookie") != "" {
		t.Fatal("valid pass was not reused")
	}
	// Parallel issuance without a cookie must still retain just one pass.
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			raw, err := randomHex(32)
			if err == nil {
				err = db.ReplaceSSOPass(context.Background(), a.database, db.HashAPIKey(raw), db.HashAPIKey(p.key), "", visitorSessionTTL(), visitorSessionIdle())
			}
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM sso_passes WHERE parent_key_hash=$1`, db.HashAPIKey(p.key)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retained %d passes: %v", n, err)
	}
	if _, err := db.GetSSOPass(context.Background(), a.database, db.HashAPIKey(pass)); err != sql.ErrNoRows {
		t.Fatal("replaced pass survived")
	}
}

func TestSSOSkillReleaseNotifiesPreviousVersion(t *testing.T) {
	version, err := PluginVersion()
	if err != nil {
		t.Fatal(err)
	}
	handler := NoticeMiddleware(version)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"ok": "yes"}) }))
	r := httptest.NewRequest("GET", "https://simple-host.app/v1/me", nil)
	r.Header.Set("X-Skill-Version", "0.27.84")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Header().Get("X-Skill-Notice") == "" {
		t.Fatal("pre-SSO skill must receive an upgrade notice")
	}
}

func TestSSOSignoutMissingSessionRequiresKnownSite(t *testing.T) {
	a, _, _ := newSSOApp(t)
	r := a.at(t, "POST", a.appHost(), "/v1/visitor/logout", nil, map[string]string{"X-SH-CSRF": "1", "Origin": "https://" + a.appHost()})
	if r.status != 500 || strings.Contains(string(r.body), "redirect_url") {
		t.Fatal("unknown site created a return ticket")
	}
}
