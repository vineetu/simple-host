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
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// ssoSiteID is the id of owner's site name.
func ssoSiteID(t *testing.T, a *App, owner Person, name string) string {
	t.Helper()
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id=$1 AND name=$2`, owner.ID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSSOHopConsentAndRevocation(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "ssoowner")
	viewer := a.NewPerson(t, "ssoviewer")
	a.Deploy(t, owner, "one")
	a.Deploy(t, owner, "two")
	handle := owner.Handle
	one, two := "one."+handle+"."+rt.SiteDomain, "two."+handle+"."+rt.SiteDomain
	pass := a.SSOPass(t, viewer.Key)
	state, nonce := a.SSOStartAt(t, one)
	r := a.SSOHop(t, state, pass)
	form := ConsentToken(t, r)
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
	r = a.At(t, "GET", one, u.RequestURI(), nil, NonceCookie(nonce))
	if r.Status != 302 {
		t.Fatalf("establish: %d %s", r.Status, r.Body)
	}
	first := ResponseCookie(t, r, accesshttp.VisitorCookieHost)
	uid := viewer.ID
	site := ssoSiteID(t, a, owner, "one")
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
	if r.Status != 200 || ResponseCookie(t, r, accesshttp.SSOCookieHost) != "" {
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
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "nonceowner")
	viewer := a.NewPerson(t, "nonceviewer")
	a.Deploy(t, owner, "one")
	h := owner.Handle
	host := "one." + h + "." + rt.SiteDomain
	pass := a.SSOPass(t, viewer.Key)
	uid := viewer.ID
	site := ssoSiteID(t, a, owner, "one")
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "wrong", "plain", "expired", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			state, nonce := a.SSOStartAt(t, host)
			r := a.SSOHop(t, state, pass)
			if r.Status != 302 {
				t.Fatalf("hop: %d", r.Status)
			}
			u, _ := url.Parse(r.Header.Get("Location"))
			headers := NonceCookie(nonce)
			switch mode {
			case "missing":
				headers = nil
			case "wrong":
				headers = NonceCookie("attacker")
			case "plain":
				headers = map[string]string{"Cookie": accesshttp.VisitorNonceCookieHTTP + "=" + nonce}
			case "expired":
				a.Database.Exec(`UPDATE visitor_establish_tokens SET expires_at=now()-interval '1 second' WHERE once=$1`, u.Query().Get("once"))
			case "revoked":
				a.Database.Exec(`DELETE FROM sso_passes WHERE id_hash=$1`, accountsrepo.HashAPIKey(pass))
			}
			r = a.At(t, "GET", host, u.RequestURI(), nil, headers)
			if r.Status == 302 {
				t.Fatal("unbound code accepted")
			}
			r = a.At(t, "GET", host, u.RequestURI(), nil, NonceCookie(nonce))
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
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "namedowner")
	viewer := a.NewPerson(t, "namedviewer")
	a.Deploy(t, owner, "named")
	h := owner.Handle
	host := "named." + h + "." + rt.SiteDomain
	site := ssoSiteID(t, a, owner, "named")
	uid := viewer.ID
	if _, err := a.Database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, site, viewer.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	if err := a.Disk.SetPasscodeMarker(ownerID, "named", true); err != nil {
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
	a, o := newSSOApp(t)
	owner := a.NewPerson(t, "gatesowner")
	a.Deploy(t, owner, "gated")
	uid, h := owner.ID, owner.Handle
	host := "gated." + h + "." + rt.SiteDomain
	site := ssoSiteID(t, a, owner, "gated")
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
	a.EnablePasscodes(t)
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
	nonce := ResponseCookie(t, r, accesshttp.VisitorNonceCookieHost)
	hop := a.SSOHop(t, u.Query().Get("state"), pass)
	hop = a.SSOConsent(t, u.Query().Get("state"), pass, ConsentToken(t, hop), "https://"+a.AppHost(), "same-origin")
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
	// DNS proof is mandatory for a custom host, even with a stored binding.
	a.Database.Exec(`UPDATE sites SET custom_domain='unproved.example.test',domain_verified_at=now() WHERE id=$1`, site)
	o.LookupCNAME = func(context.Context, string) (string, error) { return "elsewhere.example.test", nil }
	r = a.At(t, "GET", "unproved.example.test", "/v1/sso/start?return_to=https%3A%2F%2Funproved.example.test%2F", nil, nil)
	if r.Status == 302 {
		t.Fatal("unproved custom domain accepted")
	}
}

func TestSSOProviderAndEmailSignup(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "providerowner")
	a.Deploy(t, owner, "page")
	handle := owner.Handle
	host := "page." + handle + "." + rt.SiteDomain
	state, _ := a.SSOStartAt(t, host)
	ret := a.Srv.URL + "/v1/sso/hop?state=" + state + "&cn=" + NonceHash(TestNonce).String
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
	r = a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"token": token, "nonce": TestNonce, "sso_state": state, "name": "dashboard sign-in"}, nil)
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
}
func TestSSOReplacementRetiresBrowserFamily(t *testing.T) {
	for _, switchPerson := range []bool{false, true} {
		t.Run(map[bool]string{false: "same account", true: "another account"}[switchPerson], func(t *testing.T) {
			a, _ := newSSOApp(t)
			owner := a.NewPerson(t, "replacementowner")
			viewer := a.NewPerson(t, "replacementviewer")
			a.Deploy(t, owner, "page")
			handle := owner.Handle
			host := "page." + handle + "." + rt.SiteDomain
			pass := a.SSOPass(t, viewer.Key)
			oldSession, _ := a.FinishSSO(t, host, pass)
			pendingState, _ := a.SSOStartAt(t, host)
			pending := a.SSOHop(t, pendingState, pass)
			if pending.Status != 302 {
				t.Fatal("pending handoff failed", pending.Status)
			}
			pendingURL, _ := url.Parse(pending.Header.Get("Location"))
			uid := viewer.ID
			if switchPerson {
				other := a.NewPerson(t, "replacementother")
				uid = other.ID
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
			nextPass := ResponseCookie(t, r, accesshttp.SSOCookieHost)
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
	for _, mode := range []string{"expired", "no cookie", "different apex family", "swept parent"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := newSSOApp(t)
			owner := a.NewPerson(t, "logoutowner")
			viewer := a.NewPerson(t, "logoutviewer")
			a.Deploy(t, owner, "one")
			a.Deploy(t, owner, "two")
			handle := owner.Handle
			one, two := "one."+handle+"."+rt.SiteDomain, "two."+handle+"."+rt.SiteDomain
			pass := a.SSOPass(t, viewer.Key)
			local, _ := a.FinishSSO(t, one, pass)
			sibling, _ := a.FinishSSO(t, two, pass)
			id, _ := hex.DecodeString(local)
			var err error
			switch mode {
			case "expired":
				_, err = a.Database.Exec(`UPDATE visitor_sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
			case "no cookie":
				local = ""
			case "swept parent":
				_, err = a.Database.Exec(`UPDATE sso_passes SET idle_expires_at=now()-interval '1 second' WHERE id_hash=$1`, accountsrepo.HashAPIKey(pass))
				if err == nil {
					_, err = accountsrepo.SweepSSOCount(context.Background(), a.Database)
				}
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
			form := ConsentToken(t, r)
			if r.Status != 200 || !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || r.Header.Get("Set-Cookie") != "" {
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
			post := func(token, cookie, origin, site string) Resp {
				return a.At(t, "POST", a.AppHost(), "/v1/sso/signed-out", url.Values{"token": {u.Query().Get("token")}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": accesshttp.SSOCookieHost + "=" + cookie, "Origin": origin, "Sec-Fetch-Site": site})
			}
			for _, tc := range []struct{ token, cookie, origin, site string }{
				{form, pass, "https://" + one, "cross-site"},
				{"bad", pass, "https://" + a.AppHost(), "same-origin"},
				{form, "another-cookie", "https://" + a.AppHost(), "same-origin"},
			} {
				if r := post(tc.token, tc.cookie, tc.origin, tc.site); r.Status != 400 && r.Status != 403 {
					t.Fatalf("confirmation CSRF accepted: %d", r.Status)
				}
			}
			r = post(form, pass, "null", "same-origin")
			if r.Status != 200 || ResponseCookie(t, r, accesshttp.SSOCookieHost) != "" {
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

func TestSSOSignoutMissingSessionRequiresKnownSite(t *testing.T) {
	a, _ := newSSOApp(t)
	r := a.At(t, "POST", a.AppHost(), "/v1/visitor/logout", nil, map[string]string{"X-SH-CSRF": "1", "Origin": "https://" + a.AppHost()})
	if r.Status != 500 || strings.Contains(string(r.Body), "redirect_url") {
		t.Fatal("unknown site created a return ticket")
	}
}

func TestSSOActivityRefreshesParentIdle(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "idleowner")
	a.Deploy(t, owner, "page")
	uid, handle := owner.ID, owner.Handle
	host := "page." + handle + "." + rt.SiteDomain
	site := ssoSiteID(t, a, owner, "page")
	if _, err := a.Database.Exec(`UPDATE sites SET access='specific' WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	if err := a.Disk.SetPasscodeMarker(uid, "page", true); err != nil {
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
			var r Resp
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
