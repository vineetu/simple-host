package gate

import (
	"context"
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

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
				headers = map[string]string{"Cookie": VisitorNonceCookieHTTP + "=" + nonce}
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
	if r := a.At(t, "GET", host, "/v1/sso/hop?state=anything", nil, map[string]string{"Cookie": auth.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}); r.Status != 403 {
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
	nonce := ResponseCookie(t, r, VisitorNonceCookieHost)
	hop := a.SSOHop(t, u.Query().Get("state"), pass)
	hop = a.SSOConsent(t, u.Query().Get("state"), pass, ConsentToken(t, hop), "https://"+a.AppHost(), "same-origin")
	if hop.Status != 302 {
		t.Fatalf("unlocked consent: %d", hop.Status)
	}
	a.At(t, "PUT", a.AppHost(), "/v1/sites/gated/lock", map[string]string{"passcode": "changed-passcode"}, key)
	dest, _ := url.Parse(hop.Header.Get("Location"))
	r = a.At(t, "GET", host, dest.RequestURI(), nil, map[string]string{"Cookie": unlock + "; " + VisitorNonceCookieHost + "=" + nonce})
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
	r = a.At(t, "GET", a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": auth.SSOCookieHTTP + "=" + pass})
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
				r = a.At(t, "GET", host, "/", nil, map[string]string{"Cookie": VisitorCookieHost + "=" + raw})
			} else {
				r = a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": owner.Key, "Cookie": auth.SSOCookieHost + "=" + pass, "Origin": "https://" + a.AppHost()})
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
	if r := a.At(t, "GET", host, "/", nil, map[string]string{"Cookie": VisitorCookieHost + "=" + raw}); r.Status != 302 {
		t.Fatal("idle family still admitted")
	}
}
