package accesstest

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func newSSOApp(t *testing.T) (*servertest.PrivateApp, string, *accesshttp.OAuthHandler) {
	t.Helper()
	a, dir := servertest.NewSiteApp(t, "canonical")
	o := a.WithOAuth(t)
	o.Cfg.PublicBaseURL = a.Srv.URL
	a.Access.SetSSOHop(o)
	return a, dir, o
}
func TestSSOHopConsentAndRevocation(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.NewPerson(t, "ssoowner")
	viewer := a.NewPerson(t, "ssoviewer")
	a.Deploy(t, owner, "one")
	a.Deploy(t, owner, "two")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	one, two := "one."+handle+"."+servertest.PcSiteDomain, "two."+handle+"."+servertest.PcSiteDomain
	pass := a.SSOPass(t, viewer.Key)
	state, nonce := a.SSOStartAt(t, one)
	r := a.SSOHop(t, state, pass)
	form := servertest.ConsentToken(t, r)
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || r.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("consent privacy headers")
	}
	for _, tc := range []struct{ name, token, origin, site string }{{"wrong token", "bad", "https://" + a.AppHost(), "same-origin"}, {"sibling", form, "https://" + two, "same-site"}, {"opaque", form, "null", "cross-site"}, {"no origin", form, "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			r := a.SSOConsent(t, state, pass, tc.token, tc.origin, tc.site)
			if r.Status != 403 {
				t.Fatalf("CSRF accepted: %d", r.Status)
			}
		})
	}
	r = a.SSOConsent(t, state, pass, form, "null", "same-origin")
	if r.Status != 302 {
		t.Fatalf("consent: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", one, u.RequestURI(), nil, servertest.NonceCookie(nonce))
	if r.Status != 302 {
		t.Fatalf("establish: %d %s", r.Status, r.Body)
	}
	first := servertest.ResponseCookie(t, r, accesshttp.VisitorCookieHost)
	uid, _ := a.UserID(t, viewer)
	site := a.SiteID(t, owner, "one")
	denied := a.At(t, "DELETE", a.AppHost(), "/v1/sso/consents/"+site, nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if denied.Status != 401 {
		t.Fatal("cookie revoked consent without an account key")
	}
	forgotten := a.At(t, "DELETE", a.AppHost(), "/v1/sso/consents/"+site, nil, map[string]string{"X-API-Key": viewer.Key, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if forgotten.Status != 204 {
		t.Fatalf("forget consent: %d %s", forgotten.Status, forgotten.Body)
	}
	stateAgain, _ := a.SSOStartAt(t, one)
	if page := a.SSOHop(t, stateAgain, pass); page.Status != 200 {
		t.Fatal("revoked consent silently reused")
	}
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
		t.Fatal(err)
	}
	if r := a.SSOHop(t, state, pass); r.Status != 400 {
		t.Fatal("state replay")
	}
	state, _ = a.SSOStartAt(t, one)
	if r := a.SSOHop(t, state, pass); r.Status != 302 {
		t.Fatal("consent not remembered")
	}
	second, _ := a.FinishSSO(t, one, pass)
	third, _ := a.FinishSSO(t, two, pass)
	if first == second || second == third {
		t.Fatal("session fixation")
	}
	for _, raw := range []string{first, second, third} {
		id, _ := hex.DecodeString(raw)
		s, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour)
		if err != nil || s.SSOPassHash.String != accountsrepo.HashAPIKey(pass) {
			t.Fatalf("parent: %v", err)
		}
	}
	r = a.At(t, "POST", one, "/v1/visitor/logout", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + second, "X-SH-CSRF": "1", "Origin": "https://" + one, "Sec-Fetch-Site": "same-origin"})
	if r.Status != 200 {
		t.Fatalf("logout: %d %s", r.Status, r.Body)
	}
	dest := r.JSON(t)["redirect_url"].(string)
	u, _ = url.Parse(dest)
	r = a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
	if r.Status != 200 || servertest.ResponseCookie(t, r, accesshttp.SSOCookieHost) != "" {
		t.Fatal("apex cookie not cleared")
	}
	for _, raw := range []string{first, second, third} {
		id, _ := hex.DecodeString(raw)
		if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
			t.Fatal("sibling session survived")
		}
	}
	if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 401 {
		t.Fatalf("dashboard key survived: %d", r.Status)
	}
}
func TestSSONonceReplayExpiryAndRedirect(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.NewPerson(t, "nonceowner")
	viewer := a.NewPerson(t, "nonceviewer")
	a.Deploy(t, owner, "one")
	_, h := a.UserID(t, owner)
	servertest.MarkReady(t, dir, h)
	host := "one." + h + "." + servertest.PcSiteDomain
	pass := a.SSOPass(t, viewer.Key)
	uid, _ := a.UserID(t, viewer)
	site := a.SiteID(t, owner, "one")
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "wrong", "plain", "host", "expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			state, nonce := a.SSOStartAt(t, host)
			r := a.SSOHop(t, state, pass)
			if r.Status != 302 {
				t.Fatalf("hop: %d", r.Status)
			}
			u, _ := url.Parse(r.Header.Get("Location"))
			headers := servertest.NonceCookie(nonce)
			target := host
			switch mode {
			case "missing":
				headers = nil
			case "wrong":
				headers = servertest.NonceCookie("attacker")
			case "plain":
				headers = map[string]string{"Cookie": accesshttp.VisitorNonceCookieHTTP + "=" + nonce}
			case "host":
				target = a.AppHost()
			case "expired":
				a.Database.Exec(`UPDATE visitor_establish_tokens SET expires_at=now()-interval '1 second' WHERE once=$1`, u.Query().Get("once"))
			case "revoked":
				a.Database.Exec(`DELETE FROM sso_passes WHERE id_hash=$1`, accountsrepo.HashAPIKey(pass))
			}
			r = a.At(t, "GET", target, u.RequestURI(), nil, headers)
			if r.Status == 302 {
				t.Fatal("unbound code accepted")
			}
			r = a.At(t, "GET", host, u.RequestURI(), nil, servertest.NonceCookie(nonce))
			if r.Status != 400 {
				t.Fatalf("code replay: %d", r.Status)
			}
		})
	}
	for _, bad := range []string{"https://evil.test/", "https://" + host + ".evil.test/", "https://" + host + "@evil.test/"} {
		r := a.At(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape(bad), nil, nil)
		if r.Status != 400 {
			t.Fatalf("bad return accepted: %d", r.Status)
		}
	}
}
func TestSSONamedViewerAutoHopAndParentExpiry(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.NewPerson(t, "namedowner")
	viewer := a.NewPerson(t, "namedviewer")
	a.Deploy(t, owner, "named")
	_, h := a.UserID(t, owner)
	servertest.MarkReady(t, dir, h)
	host := "named." + h + "." + servertest.PcSiteDomain
	site := a.SiteID(t, owner, "named")
	uid, _ := a.UserID(t, viewer)
	if _, err := a.Database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, site, viewer.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	ownerID, _ := a.UserID(t, owner)
	if err := a.Sites.Disk.SetPasscodeMarker(ownerID, "named", true); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "GET", host, "/", nil, nil)
	if r.Status != 302 || !strings.Contains(r.Header.Get("Location"), "/v1/sso/hop?") {
		t.Fatalf("auto hop: %d %s", r.Status, r.Body)
	}
	pass := a.SSOPass(t, viewer.Key)
	u, _ := url.Parse(r.Header.Get("Location"))
	if r = a.SSOHop(t, u.Query().Get("state"), pass); r.Status != 200 {
		t.Fatalf("named viewer skipped consent: %d", r.Status)
	}
	raw, _ := a.FinishSSO(t, host, pass)
	id, _ := hex.DecodeString(raw)
	for _, q := range []string{`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE user_id=$1`, `UPDATE api_keys SET expires_at=now()-interval '1 second' WHERE user_id=$1`, `UPDATE users SET suspended_at=now() WHERE id=$1`} {
		if _, err := a.Database.Exec(q, uid); err != nil {
			t.Fatal(err)
		}
		if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour); err != sql.ErrNoRows {
			t.Fatal("expired/suspended pass accepted")
		}
		if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
			t.Fatal("expired/suspended parent accepted on site")
		}
		a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()+interval '1 day' WHERE user_id=$1`, uid)
		a.Database.Exec(`UPDATE api_keys SET expires_at=NULL WHERE user_id=$1`, uid)
		a.Database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, uid)
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/me/sign-out", nil, map[string]string{"X-API-Key": viewer.Key})
	if r.Status != 204 {
		t.Fatalf("dashboard logout: %d", r.Status)
	}
	if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
		t.Fatal("dashboard logout left a site session")
	}
}

func TestSSOStartGatesAndCookieIsolation(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.NewPerson(t, "gatesowner")
	a.Deploy(t, owner, "gated")
	uid, h := a.UserID(t, owner)
	servertest.MarkReady(t, dir, h)
	host := "gated." + h + "." + servertest.PcSiteDomain
	site := a.SiteID(t, owner, "gated")
	key := map[string]string{"X-API-Key": owner.Key}
	pass := a.SSOPass(t, owner.Key)
	start := "/v1/sso/start?return_to=" + url.QueryEscape("https://"+host+"/")
	if r := a.At(t, "GET", host, "/v1/sso/hop?state=anything", nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}); r.Status != 403 {
		t.Fatal("site host read apex pass")
	}
	for _, sfs := range []string{"same-site", "cross-site"} {
		r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.Key, "Origin": "https://" + host, "Sec-Fetch-Site": sfs})
		if r.Status != 403 {
			t.Fatal("cross-origin bootstrap")
		}
	}
	for _, dest := range []string{"iframe", "image", "empty"} {
		r := a.At(t, "GET", host, start, nil, map[string]string{"Sec-Fetch-Dest": dest, "Sec-Fetch-Site": "same-origin"})
		if r.Status != 403 {
			t.Fatal("non-navigation hop")
		}
	}
	if err := a.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "PUT", a.AppHost(), "/v1/sites/gated/lock", map[string]string{"passcode": "secret-passcode"}, key); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, start, nil, nil); r.Status != 403 {
		t.Fatalf("locked hop: %d", r.Status)
	}
	r := a.At(t, "POST", host, "/v1/site-unlock", url.Values{"passcode": {"secret-passcode"}, "next": {"/"}}.Encode(), map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin", "Content-Type": "application/x-www-form-urlencoded"})
	if r.Status != 303 {
		t.Fatalf("unlock: %d %s", r.Status, r.Body)
	}
	unlock := strings.Split(r.Header.Get("Set-Cookie"), ";")[0]
	r = a.At(t, "GET", host, start, nil, map[string]string{"Cookie": unlock})
	if r.Status != 302 {
		t.Fatalf("unlocked hop: %d %s", r.Status, r.Body)
	}
	// The unlocked start cannot be used if the owner changes the code while
	// the browser is on the apex.
	u, _ := url.Parse(r.Header.Get("Location"))
	nonce := servertest.ResponseCookie(t, r, accesshttp.VisitorNonceCookieHost)
	hop := a.SSOHop(t, u.Query().Get("state"), pass)
	hop = a.SSOConsent(t, u.Query().Get("state"), pass, servertest.ConsentToken(t, hop), "https://"+a.AppHost(), "same-origin")
	if hop.Status != 302 {
		t.Fatalf("unlocked consent: %d", hop.Status)
	}
	a.At(t, "PUT", a.AppHost(), "/v1/sites/gated/lock", map[string]string{"passcode": "changed-passcode"}, key)
	dest, _ := url.Parse(hop.Header.Get("Location"))
	r = a.At(t, "GET", host, dest.RequestURI(), nil, map[string]string{"Cookie": unlock + "; " + accesshttp.VisitorNonceCookieHost + "=" + nonce})
	if r.Status != 403 {
		t.Fatalf("changed lock bypassed: %d", r.Status)
	}
	a.At(t, "DELETE", a.AppHost(), "/v1/sites/gated/lock", nil, key)
	for _, q := range []string{`UPDATE sites SET offline_at=now() WHERE id=$1`, `UPDATE sites SET suspended_at=now() WHERE id=$1`} {
		if _, err := a.Database.Exec(q, site); err != nil {
			t.Fatal(err)
		}
		if r := a.At(t, "GET", host, start, nil, nil); r.Status == 302 {
			t.Fatal("unavailable site hop")
		}
		a.Database.Exec(`UPDATE sites SET offline_at=NULL,suspended_at=NULL WHERE id=$1`, site)
	}
	// A plain cookie on HTTPS must not substitute for the apex __Host- pass.
	state, _ := a.SSOStartAt(t, host)
	r = a.At(t, "GET", a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": accesshttp.SSOCookieHTTP + "=" + pass})
	if r.Status != 200 || !strings.Contains(string(r.Body), "SHSignIn.mount") {
		t.Fatal("plain apex cookie accepted")
	}
	// A missing or expired parent and a suspended account cannot bootstrap.
	a.Database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, uid)
	if r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.Key, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"}); r.Status != 401 {
		t.Fatal("suspended bootstrap")
	}
	a.Database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, uid)
	a.Database.Exec(`UPDATE api_keys SET scope='deploy' WHERE key_hash=$1`, accountsrepo.HashAPIKey(owner.Key))
	// The scope gate answers 403 deploy_only_key first; the route itself 401.
	if r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.Key, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"}); r.Status != 401 && r.Status != 403 {
		t.Fatal("deploy key bootstrapped SSO")
	}
	a.Database.Exec(`UPDATE api_keys SET scope='full' WHERE key_hash=$1`, accountsrepo.HashAPIKey(owner.Key))
	o.IPLimiter.Reset(0, 0)
	if r := a.At(t, "GET", host, start, nil, nil); r.Status != 429 {
		t.Fatalf("IP limiter: %d", r.Status)
	}
	o.IPLimiter.Reset(10000, 0)

	// DNS proof is mandatory for a custom host, even with a stored binding.
	a.Database.Exec(`UPDATE sites SET custom_domain='unproved.example.test',domain_verified_at=now() WHERE id=$1`, site)
	o.LookupCNAME = func(context.Context, string) (string, error) { return "elsewhere.example.test", nil }
	r = a.At(t, "GET", "unproved.example.test", "/v1/sso/start?return_to=https%3A%2F%2Funproved.example.test%2F", nil, nil)
	if r.Status == 302 {
		t.Fatal("unproved custom domain accepted")
	}
}

func TestSSOProviderAndEmailSignup(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.NewPerson(t, "providerowner")
	a.Deploy(t, owner, "page")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	host := "page." + handle + "." + servertest.PcSiteDomain
	state, _ := a.SSOStartAt(t, host)
	ret := a.Srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + servertest.TestNonceHash(servertest.TestNonce).String
	r := a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/google?return_to="+url.QueryEscape(ret), nil, nil)
	if r.Status != 302 {
		t.Fatalf("provider start: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/google/callback?state="+u.Query().Get("state")+"&code=sso-provider-"+handle, nil, nil)
	if r.Status != 302 {
		t.Fatalf("provider callback: %d %s", r.Status, r.Body)
	}
	u, _ = url.Parse(r.Header.Get("Location"))
	if u.Path != "/v1/sso/hop" || u.Query().Get("state") != state {
		t.Fatal("provider lost hop")
	}
	token := u.Query().Get("token")
	r = a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"token": token, "nonce": servertest.TestNonce, "sso_state": state, "name": "dashboard sign-in"}, nil)
	if r.Status != 200 {
		t.Fatalf("provider verify: %d %s", r.Status, r.Body)
	}
	pass := a.SSOPass(t, r.JSON(t)["api_key"].(string))
	p, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	err = a.Database.QueryRow(`SELECT signup_source FROM users WHERE id=$1`, p.UserID).Scan(&source)
	if err != nil || source != "visitor_sso" {
		t.Fatalf("source: %s %v", source, err)
	}
	var sites int
	a.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1`, p.UserID).Scan(&sites)
	if sites != 0 {
		t.Fatal("visitor got sites")
	}
	owners, err := accountsrepo.ListSiteOwners(context.Background(), a.Database)
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
	a.Users.SetSignupGeoBlock(accountapp.NewSignupGeoBlock(geo, []string{"IQ"}))
	o.SetSignupGeoBlock(accountapp.NewSignupGeoBlock(geo, []string{"IQ"}))
	for _, path := range []string{"/v1/auth", "/v1/auth/oauth/google/callback?state=x&code=x"} {
		method := "POST"
		var body any = map[string]string{"email": "blocked@example.test"}
		if strings.Contains(path, "callback") {
			method = "GET"
			body = nil
		}
		r := a.At(t, method, a.AppHost(), path, body, map[string]string{"X-Forwarded-For": ip})
		if r.Status != 403 {
			t.Fatalf("geo block: %s %d", path, r.Status)
		}
	}
}
func TestSSOCompanyProvider(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.NewPerson(t, "ssooidcowner")
	a.Deploy(t, owner, "portal")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	host := "portal." + handle + "." + servertest.PcSiteDomain
	o.SignIn = accountapp.NewProviderSignIn(o.Accounts, map[string]accountapp.Provider{"google": servertest.FakeProvider{}, "oidc": servertest.SSOOIDCProvider{}})
	state, _ := a.SSOStartAt(t, host)
	ret := a.Srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + servertest.TestNonceHash(servertest.TestNonce).String
	r := a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/oidc?return_to="+url.QueryEscape(ret), nil, nil)
	if r.Status != 302 {
		t.Fatal("company provider start")
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", a.AppHost(), "/v1/auth/oauth/oidc/callback?state="+u.Query().Get("state")+"&code=company-"+handle, nil, nil)
	if r.Status != 302 {
		t.Fatalf("OIDC callback: %d", r.Status)
	}
	u, _ = url.Parse(r.Header.Get("Location"))
	if u.Path != "/v1/sso/hop" {
		t.Fatal("OIDC lost hop")
	}
	r = a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"token": u.Query().Get("token"), "nonce": servertest.TestNonce, "sso_state": state}, nil)
	if r.Status != 200 {
		t.Fatalf("OIDC verify: %d", r.Status)
	}
	pass := a.SSOPass(t, r.JSON(t)["api_key"].(string))
	a.FinishSSO(t, host, pass)
}

func TestSSOReplacementRetiresBrowserFamily(t *testing.T) {
	for _, switchPerson := range []bool{false, true} {
		t.Run(map[bool]string{false: "same account", true: "another account"}[switchPerson], func(t *testing.T) {
			a, dir, _ := newSSOApp(t)
			owner := a.NewPerson(t, "replacementowner")
			viewer := a.NewPerson(t, "replacementviewer")
			a.Deploy(t, owner, "page")
			_, handle := a.UserID(t, owner)
			servertest.MarkReady(t, dir, handle)
			host := "page." + handle + "." + servertest.PcSiteDomain
			pass := a.SSOPass(t, viewer.Key)
			oldSession, _ := a.FinishSSO(t, host, pass)
			pendingState, _ := a.SSOStartAt(t, host)
			pending := a.SSOHop(t, pendingState, pass)
			if pending.Status != 302 {
				t.Fatal("pending handoff failed", pending.Status)
			}
			pendingURL, _ := url.Parse(pending.Header.Get("Location"))
			uid, _ := a.UserID(t, viewer)
			if switchPerson {
				other := a.NewPerson(t, "replacementother")
				uid, _ = a.UserID(t, other)
			}
			key, err := auth.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if err := accountapp.New(a.Database).AddSignInKey(context.Background(), uid, key, "dashboard sign-in"); err != nil {
				t.Fatal(err)
			}
			r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Cookie": accesshttp.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
			if r.Status != 204 {
				t.Fatalf("replace: %d", r.Status)
			}
			nextPass := servertest.ResponseCookie(t, r, accesshttp.SSOCookieHost)
			if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatal("old pass survived")
			}
			var pendingCount int
			if err := a.Database.QueryRow(`SELECT count(*) FROM visitor_establish_tokens WHERE once=$1`, pendingURL.Query().Get("once")).Scan(&pendingCount); err != nil || pendingCount != 0 {
				t.Fatal("old pending code survived", err)
			}

			id, _ := hex.DecodeString(oldSession)
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatalf("old family survived: %v", err)
			}
			if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 401 {
				t.Fatal("old key survived")
			}
			newSession, _ := a.FinishSSO(t, host, nextPass)
			r = a.At(t, "POST", host, "/v1/visitor/logout", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + newSession, "X-SH-CSRF": "1", "Origin": "https://" + host})
			if r.Status != 200 {
				t.Fatalf("logout: %d", r.Status)
			}
			id, _ = hex.DecodeString(newSession)
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatal("new family survived logout")
			}
		})
	}
}

func TestSSOSignoutWithoutLiveLocalSession(t *testing.T) {
	for _, mode := range []string{"expired", "idle expired", "missing", "no cookie", "legacy", "different apex family", "swept parent"} {
		t.Run(mode, func(t *testing.T) {
			a, dir, _ := newSSOApp(t)
			owner := a.NewPerson(t, "logoutowner")
			viewer := a.NewPerson(t, "logoutviewer")
			a.Deploy(t, owner, "one")
			a.Deploy(t, owner, "two")
			_, handle := a.UserID(t, owner)
			servertest.MarkReady(t, dir, handle)
			one, two := "one."+handle+"."+servertest.PcSiteDomain, "two."+handle+"."+servertest.PcSiteDomain
			pass := a.SSOPass(t, viewer.Key)
			local, _ := a.FinishSSO(t, one, pass)
			sibling, _ := a.FinishSSO(t, two, pass)
			id, _ := hex.DecodeString(local)
			var err error
			switch mode {
			case "expired":
				_, err = a.Database.Exec(`UPDATE visitor_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
			case "idle expired":
				_, err = a.Database.Exec(`UPDATE visitor_sessions SET idle_expires_at=now()-interval '1 second' WHERE id=$1`, id)
			case "no cookie":
				local = ""
			case "swept parent":
				_, err = a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE id_hash=$1`, accountsrepo.HashAPIKey(pass))
				if err == nil {
					err = accountsrepo.SweepSSO(context.Background(), a.Database)
				}
			case "missing":
				_, err = a.Database.Exec(`DELETE FROM visitor_sessions WHERE id=$1`, id)
			case "legacy":
				_, err = a.Database.Exec(`UPDATE visitor_sessions SET sso_pass_hash=NULL WHERE id=$1`, id)
			case "different apex family":
				other := a.NewPerson(t, "logoutother")
				viewer = other
				pass = a.SSOPass(t, other.Key)
				sibling, _ = a.FinishSSO(t, two, pass)
			}
			if err != nil {
				t.Fatal(err)
			}
			r := a.At(t, "POST", one, "/v1/visitor/logout", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + local, "X-SH-CSRF": "1", "Origin": "https://" + one})
			if r.Status != 200 {
				t.Fatalf("logout must navigate: %d", r.Status)
			}
			u, err := url.Parse(r.JSON(t)["redirect_url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			r = a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
			form := servertest.ConsentToken(t, r)
			if r.Status != 200 || !strings.Contains(string(r.Body), "Sign out of Simple Host on this browser?") || !strings.Contains(string(r.Body), `href="https://`+one+`/">Cancel</a>`) || !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || r.Header.Get("Set-Cookie") != "" {
				t.Fatal("unbound link did not ask for confirmation")
			}
			if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 200 {
				t.Fatal("GET revoked another browser's key")
			}
			if mode != "swept parent" {
				sid, _ := hex.DecodeString(sibling)
				if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, sid, 180*24*time.Hour); err != nil {
					t.Fatal("GET revoked another browser's family")
				}
			}
			post := func(token, cookie, origin, site string) servertest.Resp {
				return a.At(t, "POST", a.AppHost(), "/v1/sso/signed-out", url.Values{"token": {u.Query().Get("token")}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": accesshttp.SSOCookieHost + "=" + cookie, "Origin": origin, "Sec-Fetch-Site": site})
			}
			for _, tc := range []struct{ token, cookie, origin, site string }{
				{form, pass, "https://" + one, "cross-site"},
				{form, pass, "https://" + one, "same-site"},
				{form, pass, "", ""},
				{"bad", pass, "https://" + a.AppHost(), "same-origin"},
				{form, "another-cookie", "https://" + a.AppHost(), "same-origin"},
			} {
				if r := post(tc.token, tc.cookie, tc.origin, tc.site); r.Status != 400 && r.Status != 403 {
					t.Fatalf("confirmation CSRF accepted: %d", r.Status)
				}
			}
			r = post(form, pass, "null", "same-origin")
			if r.Status != 200 || servertest.ResponseCookie(t, r, accesshttp.SSOCookieHost) != "" || !strings.Contains(string(r.Body), "localStorage.getItem('apiKey')") || !strings.Contains(string(r.Body), "fetch('/v1/me/sign-out'") || !strings.Contains(string(r.Body), `location.replace("https://`+one+`/")`) {
				t.Fatalf("apex did not complete sign-out: %d", r.Status)
			}
			if replay := post(form, pass, "https://"+a.AppHost(), "same-origin"); replay.Status != 400 {
				t.Fatal("confirmation replayed")
			}
			id, _ = hex.DecodeString(sibling)
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
				t.Fatal("sibling survived")
			}
			if mode != "swept parent" {
				if r := a.At(t, "GET", a.AppHost(), "/v1/me", nil, map[string]string{"X-API-Key": viewer.Key}); r.Status != 401 {
					t.Fatal("apex key survived")
				}
			} // The swept-parent key is revoked by the landing script (browser regression).
			if r := a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, nil); r.Status != 400 {
				t.Fatal("ticket replayed")
			}
		})
	}
}

func TestSSOSessionIssuanceLimits(t *testing.T) {
	for _, by := range []string{"IP", "key"} {
		t.Run(by, func(t *testing.T) {
			a, _, o := newSSOApp(t)
			one, two := a.NewPerson(t, "limitone"), a.NewPerson(t, "limittwo")
			o.IPLimiter.Capacity = 1
			o.IPLimiter.Rate = 0
			o.SSOKeyLimiter.Capacity = 1
			o.SSOKeyLimiter.Rate = 0
			request := func(key, ip string) servertest.Resp {
				return a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "X-Forwarded-For": ip, "Origin": "https://" + a.AppHost()})
			}
			if r := request(one.Key, "192.0.2.1"); r.Status != 204 {
				t.Fatalf("first: %d", r.Status)
			}
			key, ip := two.Key, "192.0.2.1"
			if by == "key" {
				key, ip = one.Key, "192.0.2.2"
			}
			if r := request(key, ip); r.Status != 429 || r.Header.Get("Retry-After") == "" {
				t.Fatalf("limit: %d", r.Status)
			}
		})
	}
}

func TestSSOSessionRetainsOnePassPerKey(t *testing.T) {
	a, _, _ := newSSOApp(t)
	p := a.NewPerson(t, "boundedpasses")
	pass := a.SSOPass(t, p.Key)
	r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": p.Key, "Cookie": accesshttp.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost()})
	if r.Status != 204 || r.Header.Get("Set-Cookie") != "" {
		t.Fatal("valid pass was not reused")
	}
	// Parallel issuance without a cookie must still retain just one pass.
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			raw, err := accesshttp.RandomHex(32)
			if err == nil {
				err = accountapp.New(a.Database).ReplaceSSOPass(context.Background(), accountsrepo.HashAPIKey(raw), accountsrepo.HashAPIKey(p.Key), "", a.Sites.Limits.VisitorSessionTTL, a.Sites.Limits.VisitorSessionIdle, a.Sites.Limits.KeyIdleExpiry)
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
	if err := a.Database.QueryRow(`SELECT count(*) FROM sso_passes WHERE parent_key_hash=$1`, accountsrepo.HashAPIKey(p.Key)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retained %d passes: %v", n, err)
	}
	if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(pass), 180*24*time.Hour); err != sql.ErrNoRows {
		t.Fatal("replaced pass survived")
	}
}

func TestSSOSignoutMissingSessionRequiresKnownSite(t *testing.T) {
	a, _, _ := newSSOApp(t)
	r := a.At(t, "POST", a.AppHost(), "/v1/visitor/logout", nil, map[string]string{"X-SH-CSRF": "1", "Origin": "https://" + a.AppHost()})
	if r.Status != 500 || strings.Contains(string(r.Body), "redirect_url") {
		t.Fatal("unknown site created a return ticket")
	}
}

func TestSSOActivityRefreshesParentIdle(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.NewPerson(t, "idleowner")
	a.Deploy(t, owner, "page")
	uid, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	host := "page." + handle + "." + servertest.PcSiteDomain
	site := a.SiteID(t, owner, "page")
	if _, err := a.Database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	if err := a.Sites.Disk.SetPasscodeMarker(uid, "page", true); err != nil {
		t.Fatal(err)
	}
	pass := a.SSOPass(t, owner.Key)
	raw, _ := a.FinishSSO(t, host, pass)
	id, _ := hex.DecodeString(raw)
	hash := accountsrepo.HashAPIKey(pass)
	for _, surface := range []string{"site", "dashboard"} {
		t.Run(surface, func(t *testing.T) {
			// Thirteen days of inactivity, followed by normal use before idle expiry.
			if _, err := a.Database.Exec(`UPDATE sso_passes SET created_at=now()-interval '13 days', idle_expires_at=now()+interval '1 day', expires_at=now()+interval '5 days' WHERE id_hash=$1`, hash); err != nil {
				t.Fatal(err)
			}
			var r servertest.Resp
			if surface == "site" {
				r = a.At(t, "GET", host, "/", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + raw})
			} else {
				r = a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.Key, "Cookie": accesshttp.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost()})
			}
			if r.Status != 200 && r.Status != 204 {
				t.Fatalf("use: %d", r.Status)
			}
			var idle, absolute time.Time
			if err := a.Database.QueryRow(`SELECT idle_expires_at,expires_at FROM sso_passes WHERE id_hash=$1`, hash).Scan(&idle, &absolute); err != nil {
				t.Fatal(err)
			}
			if !idle.Equal(absolute) || idle.Before(time.Now().Add(4*24*time.Hour)) {
				t.Fatal("idle was not extended to the absolute cap")
			}
			if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != nil {
				t.Fatal("active family expired", err)
			}
		})
	}
	if _, err := a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE id_hash=$1`, hash); err != nil {
		t.Fatal(err)
	}
	if err := accountsrepo.TouchVisitorSession(context.Background(), a.Database, id, 14*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour); err != sql.ErrNoRows {
		t.Fatal("truly idle family revived")
	}
	if r := a.At(t, "GET", host, "/", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + raw}); r.Status != 302 {
		t.Fatal("idle family still admitted")
	}
}

func TestSSOStartWithoutApexUsesLegacyProvider(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.NewPerson(t, "fallbackowner")
	a.Deploy(t, owner, "page")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	host := "page." + handle + "." + servertest.PcSiteDomain
	o.Cfg.PublicBaseURL = ""
	for _, provider := range []string{"google", "oidc"} {
		o.SignIn = accountapp.NewProviderSignIn(o.Accounts, map[string]accountapp.Provider{provider: servertest.SSOOIDCProvider{}})
		back := "https://" + host + "/page?ok=1"
		r := a.At(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape(back), nil, nil)
		if r.Status != 302 || r.Header.Get("Location") != "/v1/visitor/oauth/"+provider+"?return_to="+url.QueryEscape(back) {
			t.Fatalf("legacy fallback: %d %s", r.Status, r.Body)
		}
		if r := a.At(t, "GET", host, r.Header.Get("Location"), nil, nil); r.Status != 302 {
			t.Fatalf("legacy provider start: %d %s", r.Status, r.Body)
		}
	}
}

func TestSSOPublicRoutesRateLimited(t *testing.T) {
	for _, route := range []struct{ method, path string }{{"GET", "/v1/sso/hop"}, {"POST", "/v1/sso/consent"}, {"GET", "/v1/sso/signed-out"}, {"POST", "/v1/sso/signed-out"}} {
		t.Run(route.method+route.path, func(t *testing.T) {
			a, _, o := newSSOApp(t)
			o.IPLimiter.Capacity = 1
			o.IPLimiter.Rate = 0
			headers := map[string]string{"Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
			if r := a.At(t, route.method, a.AppHost(), route.path, nil, headers); r.Status == 429 {
				t.Fatal("first request limited")
			}
			if r := a.At(t, route.method, a.AppHost(), route.path, nil, headers); r.Status != 429 || r.Header.Get("Retry-After") == "" {
				t.Fatalf("missing rate limit: %d", r.Status)
			}
		})
	}
}
