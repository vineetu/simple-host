package accesstest

import (
	"context"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestSSORedteamNavigation(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "rednavowner")
	viewer := a.NewPerson(t, "rednavviewer")
	a.Deploy(t, owner, "site")
	handle := owner.Handle
	host := "site." + handle + "." + rt.SiteDomain
	pass := a.SSOPass(t, viewer.Key)
	uid := viewer.ID
	site := ssoSiteID(t, a, owner, "site")
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, site); err != nil {
		t.Fatal(err)
	}
	state, _ := a.SSOStartAt(t, host)
	for _, headers := range []map[string]string{
		{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Purpose": "prefetch;prerender"},
		{"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "empty"},
		{"Sec-Fetch-Mode": "cors"}, {"Sec-Fetch-Dest": "iframe"}, {"Purpose": "prefetch"},
	} {
		for _, path := range []string{"/v1/sso/start?return_to=" + url.QueryEscape("https://"+host+"/"), "/v1/sso/hop?state=" + state, "/v1/visitor/oauth/google?return_to=" + url.QueryEscape("https://"+host+"/")} {
			target := host
			if strings.Contains(path, "/hop?") {
				target = a.AppHost()
			}
			headers["Cookie"] = accesshttp.SSOCookieHost + "=" + pass
			got := a.At(t, "GET", target, path, nil, headers)
			if got.Status < 400 || len(got.Header.Values("Set-Cookie")) != 0 {
				t.Fatalf("background navigation accepted: %s %d", path, got.Status)
			}
		}
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id=$1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("background session: %d %v", n, err)
	}
	if err := a.Database.QueryRow(`SELECT count(*) FROM sso_hops WHERE site_id=$1`, site).Scan(&n); err != nil || n != 1 {
		t.Fatalf("background hop: %d %v", n, err)
	}
	old := a.At(t, "GET", a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + pass})
	if old.Status != 200 || !strings.Contains(string(old.Body), "Continue as") {
		t.Fatal("metadata-free client silently minted")
	}
}

func TestSSORedteamListedConsentAndPending(t *testing.T) {
	for _, mode := range []string{"complete", "wrong nonce", "abandon", "rebind"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := newSSOApp(t)
			owner := a.NewPerson(t, "redowner")
			viewer := a.NewPerson(t, "redviewer")
			a.Deploy(t, owner, "site")
			handle := owner.Handle
			host := "site." + handle + "." + rt.SiteDomain
			site := ssoSiteID(t, a, owner, "site")
			uid := viewer.ID
			if _, err := a.Database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, site, viewer.Email); err != nil {
				t.Fatal(err)
			}
			pass := a.SSOPass(t, viewer.Key)
			state, nonce := a.SSOStartAt(t, host)
			page := a.SSOHop(t, state, pass)
			if page.Status != 200 {
				t.Fatal("listed viewer bypassed consent", page.Status)
			}
			check := func(sessions, signins int) {
				t.Helper()
				for table, want := range map[string]int{"visitor_sessions": sessions, "site_visitor_signins": signins} {
					var n int
					if err := a.Database.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=$1 AND site_id=$2`, uid, site).Scan(&n); err != nil || n != want {
						t.Fatalf("%s count=%d want=%d err=%v", table, n, want, err)
					}
				}
			}
			check(0, 0)
			csp := page.Header.Get("Content-Security-Policy")
			if !strings.Contains(csp, "script-src 'self'") || strings.Contains(strings.Split(strings.Split(csp, "script-src")[1], ";")[0], "'unsafe-inline'") || !strings.Contains(csp, "frame-ancestors 'none'") {
				t.Fatal("unsafe consent CSP", csp)
			}
			hop := a.SSOConsent(t, state, pass, ConsentToken(t, page), "https://"+a.AppHost(), "same-origin")
			if hop.Status != 302 {
				t.Fatal(hop.Status)
			}
			check(1, 0)
			var shortLived bool
			if err := a.Database.QueryRow(`SELECT expires_at <= created_at + interval '61 seconds' AND idle_expires_at <= created_at + interval '61 seconds' FROM visitor_sessions WHERE user_id=$1 AND site_id=$2`, uid, site).Scan(&shortLived); err != nil || !shortLived {
				t.Fatal("pending session exceeded handoff lifetime", err)
			}
			u, _ := url.Parse(hop.Header.Get("Location"))
			switch mode {
			case "wrong nonce":
				nonce = "wrong"
			case "rebind":
				a.Deploy(t, owner, "replacement")
				a.Database.Exec(`UPDATE sites SET name='renamed' WHERE id=$1`, site)
				a.Database.Exec(`UPDATE sites SET name='site' WHERE user_id=(SELECT user_id FROM sites WHERE id=$1) AND name='replacement'`, site)
			case "abandon":
				a.Database.Exec(`UPDATE visitor_sessions SET expires_at=now()-interval '1 second' WHERE user_id=$1`, uid)
				a.Database.Exec(`UPDATE visitor_establish_tokens SET expires_at=now()-interval '1 second' WHERE once=$1`, u.Query().Get("once"))
				if n, err := accountsrepo.SweepVisitorAuthCount(context.Background(), a.Database); err != nil || n < 2 {
					t.Fatalf("expired handoff/session cleanup: removed=%d err=%v", n, err)
				}
				check(0, 0)
				return
			}
			done := a.At(t, "GET", host, u.RequestURI(), nil, NonceCookie(nonce))
			if mode == "complete" {
				if done.Status != 302 {
					t.Fatal(done.Status)
				}
				check(1, 1)
				id, _ := hex.DecodeString(ResponseCookie(t, done, accesshttp.VisitorCookieHost))
				sess, err := accountsrepo.GetVisitorSession(context.Background(), a.Database, id, 180*24*time.Hour)
				if err != nil || time.Until(sess.ExpiresAt) < time.Hour {
					t.Fatal("pending session not promoted", err)
				}
				state, _ = a.SSOStartAt(t, host)
				if again := a.SSOHop(t, state, pass); again.Status != 302 {
					t.Fatal("consent not remembered")
				}
			} else {
				if done.Status < 400 {
					t.Fatal("failed handoff accepted", mode)
				}
				check(0, 0)
			}
		})
	}
}

func TestSSORedteamPasscodeIsolation(t *testing.T) {
	a := NewApp(t)
	a.EnablePasscodes(t)
	l := a.Access.Limits
	l.RatePasscodeIP = config.Rate{Burst: 2, Every: time.Hour}
	l.RatePasscodeSite = config.Rate{Burst: 5, Every: time.Hour}
	a.SetLimits(l)
	owner, alice, bob := a.NewPerson(t, "isoowner"), a.NewPerson(t, "isoalice"), a.NewPerson(t, "isobob")
	sid := a.Deploy(t, owner, "shop")
	host := owner.SiteHost("shop")
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, owner.Header()); r.Status != 200 {
		t.Fatal(r.Status)
	}
	row, err := sitesrepo.GetSitePasscode(context.Background(), a.Database, sid)
	if err != nil {
		t.Fatal(err)
	}
	check := func(user, code string) (time.Duration, bool) {
		t.Helper()
		req := httptest.NewRequest("POST", "https://"+host+"/", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		if user != "" {
			pass, undo, err := accountsrepo.IssueInternalVisitorKey(accountsrepo.InternalVisitorPass{UserID: user, SiteID: sid, Host: host})
			if err != nil {
				t.Fatal(err)
			}
			defer undo()
			req.Header.Set(accesshttp.VisitorPassHeader, pass)
		}
		wait, ok, err := a.Access.CheckVisitorPasscode(req, row, code)
		if err != nil {
			t.Fatal(err)
		}
		return wait, ok
	}
	check(alice.ID, "wrong")
	check(alice.ID, "wrong")
	if wait, ok := check(alice.ID, "123456"); wait == 0 || ok {
		t.Fatal("attacker not limited")
	}
	if wait, ok := check(bob.ID, "123456"); wait != 0 || !ok {
		t.Fatal("shared AI egress locked other user")
	}
	if wait, ok := check("", "123456"); wait != 0 || !ok {
		t.Fatal("AI locked browser")
	}
	// Exhaust the separate AI site budget using further accounts.
	check(bob.ID, "wrong")
	check(bob.ID, "wrong")
	check(owner.ID, "wrong")
	if wait, ok := check("", "123456"); wait != 0 || !ok {
		t.Fatal("AI site budget locked browser")
	}
}

func TestSSORedteamCustomBinding(t *testing.T) {
	a, o := newSSOApp(t)
	owner := a.NewPerson(t, "customredowner")
	viewer := a.NewPerson(t, "customredviewer")
	a.Deploy(t, owner, "first")
	a.Deploy(t, owner, "second")
	site := ssoSiteID(t, a, owner, "first")
	other := ssoSiteID(t, a, owner, "second")
	host := "consent.example.test"
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain=$1,domain_verified_at=NULL WHERE id=$2`, host, site); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := o.SanitizeReturnTo(context.Background(), "https://"+host+"/"); err == nil {
		t.Fatal("pending domain accepted")
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_verified_at=now() WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := o.SanitizeReturnTo(context.Background(), "https://"+host+"/"); err != nil {
		t.Fatal("verified domain rejected", err)
	}
	pass := a.SSOPass(t, viewer.Key)
	state, nonce := a.SSOStartAt(t, host)
	page := a.SSOHop(t, state, pass)
	hop := a.SSOConsent(t, state, pass, ConsentToken(t, page), "https://"+a.AppHost(), "same-origin")
	if hop.Status != 302 {
		t.Fatal(hop.Status)
	}
	a.Database.Exec(`UPDATE sites SET custom_domain=NULL,domain_verified_at=NULL WHERE id=$1`, site)
	a.Database.Exec(`UPDATE sites SET custom_domain=$1,domain_verified_at=now() WHERE id=$2`, host, other)
	u, _ := url.Parse(hop.Header.Get("Location"))
	done := a.At(t, "GET", host, u.RequestURI(), nil, NonceCookie(nonce))
	if done.Status != 400 {
		t.Fatal("rebound handoff accepted", done.Status)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE site_id=$1`, site).Scan(&n); err != nil || n != 0 {
		t.Fatal("rebound pending session survived", n, err)
	}
}

func TestSSORedteamSignoutCascadeOnlyOnce(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "logoutredowner")
	viewer := a.NewPerson(t, "logoutredviewer")
	a.Deploy(t, owner, "site")
	handle := owner.Handle
	host := "site." + handle + "." + rt.SiteDomain
	pass := a.SSOPass(t, viewer.Key)
	session, _ := a.FinishSSO(t, host, pass)
	signout := func() Resp {
		return a.At(t, "POST", host, "/v1/visitor/logout", nil, map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + session, "Origin": "https://" + host, "X-SH-CSRF": "1"})
	}
	if r := signout(); r.Status != 200 {
		t.Fatal(r.Status)
	}
	// A stale site's second request cannot silently revoke a fresh browser family.
	next := a.NewPerson(t, "logoutrednext")
	nextPass := a.SSOPass(t, next.Key)
	again := signout()
	u, _ := url.Parse(again.JSON(t)["redirect_url"].(string))
	page := a.At(t, "GET", a.AppHost(), u.RequestURI(), nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + nextPass})
	if page.Status != 200 {
		t.Fatal("stale site did not require confirmation")
	}
	if _, err := accountsrepo.GetSSOPass(context.Background(), a.Database, accountsrepo.HashAPIKey(nextPass), 180*24*time.Hour); err != nil {
		t.Fatal("stale site revoked new family")
	}
}

func TestSSORedteamLegacyOAuthUsesConsentFlow(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "legacyredowner")
	a.Deploy(t, owner, "site")
	handle := owner.Handle
	host := "site." + handle + "." + rt.SiteDomain
	r := a.At(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape("https://"+host+"/"), nil, map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
	if r.Status != 302 || !strings.Contains(r.Header.Get("Location"), "/v1/sso/hop?") {
		t.Fatal("legacy provider bypassed consent", r.Status)
	}
}
