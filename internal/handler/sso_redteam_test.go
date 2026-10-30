package handler

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

func TestSSORedteamNavigation(t *testing.T) {
	a, dir, o := newSSOApp(t)
	owner := a.newPerson(t, "rednavowner")
	viewer := a.newPerson(t, "rednavviewer")
	a.deploy(t, owner, "site")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "site." + handle + "." + pcSiteDomain
	pass := a.ssoPass(t, viewer.key)
	uid, _ := a.userID(t, viewer)
	site := a.siteID(t, owner, "site")
	if err := db.RememberSSOConsent(context.Background(), a.database, uid, site); err != nil {
		t.Fatal(err)
	}
	state, _ := a.ssoStartAt(t, host)
	for _, headers := range []map[string]string{
		{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Purpose": "prefetch;prerender"},
		{"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "empty"},
		{"Sec-Fetch-Mode": "cors"}, {"Sec-Fetch-Dest": "iframe"}, {"Purpose": "prefetch"},
	} {
		for _, path := range []string{"/v1/sso/start?return_to=" + url.QueryEscape("https://"+host+"/"), "/v1/sso/hop?state=" + state, "/v1/visitor/oauth/google?return_to=" + url.QueryEscape("https://"+host+"/")} {
			target := host
			if strings.Contains(path, "/hop?") {
				target = a.appHost()
			}
			headers["Cookie"] = ssoCookieHost + "=" + pass
			got := a.at(t, "GET", target, path, nil, headers)
			if got.status < 400 || len(got.header.Values("Set-Cookie")) != 0 {
				t.Fatalf("background navigation accepted: %s %d", path, got.status)
			}
		}
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id=$1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("background session: %d %v", n, err)
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM sso_hops WHERE site_id=$1`, site).Scan(&n); err != nil || n != 1 {
		t.Fatalf("background hop: %d %v", n, err)
	}
	old := a.at(t, "GET", a.appHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": ssoCookieHost + "=" + pass})
	if old.status != 200 || !strings.Contains(string(old.body), "Continue as") {
		t.Fatal("metadata-free client silently minted")
	}
	// No legacy start can overwrite the nonce in a background request.
	req := httptest.NewRequest("GET", "https://"+host+"/v1/visitor/oauth/google", nil)
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	rec := httptest.NewRecorder()
	o.startOnSite(rec, req)
	if rec.Code != 403 {
		t.Fatal(rec.Code)
	}
}

func TestSSORedteamListedConsentAndPending(t *testing.T) {
	for _, mode := range []string{"complete", "wrong nonce", "abandon", "offline", "rebind"} {
		t.Run(mode, func(t *testing.T) {
			a, dir, _ := newSSOApp(t)
			owner := a.newPerson(t, "redowner")
			viewer := a.newPerson(t, "redviewer")
			a.deploy(t, owner, "site")
			_, handle := a.userID(t, owner)
			markReady(t, dir, handle)
			host := "site." + handle + "." + pcSiteDomain
			site := a.siteID(t, owner, "site")
			uid, _ := a.userID(t, viewer)
			if _, err := a.database.Exec(`INSERT INTO site_viewers(site_id,email) VALUES($1,$2)`, site, viewer.email); err != nil {
				t.Fatal(err)
			}
			pass := a.ssoPass(t, viewer.key)
			state, nonce := a.ssoStartAt(t, host)
			page := a.ssoHop(t, state, pass)
			if page.status != 200 {
				t.Fatal("listed viewer bypassed consent", page.status)
			}
			check := func(sessions, signins int) {
				t.Helper()
				for table, want := range map[string]int{"visitor_sessions": sessions, "site_visitor_signins": signins} {
					var n int
					if err := a.database.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=$1 AND site_id=$2`, uid, site).Scan(&n); err != nil || n != want {
						t.Fatalf("%s count=%d want=%d err=%v", table, n, want, err)
					}
				}
			}
			check(0, 0)
			csp := page.header.Get("Content-Security-Policy")
			if !strings.Contains(csp, "script-src 'self'") || strings.Contains(strings.Split(strings.Split(csp, "script-src")[1], ";")[0], "'unsafe-inline'") || !strings.Contains(csp, "frame-ancestors 'none'") {
				t.Fatal("unsafe consent CSP", csp)
			}
			hop := a.ssoConsent(t, state, pass, consentToken(t, page), "https://"+a.appHost(), "same-origin")
			if hop.status != 302 {
				t.Fatal(hop.status)
			}
			check(1, 0)
			u, _ := url.Parse(hop.header.Get("Location"))
			switch mode {
			case "wrong nonce":
				nonce = "wrong"
			case "offline":
				a.database.Exec(`UPDATE sites SET offline_at=now() WHERE id=$1`, site)
			case "rebind":
				a.deploy(t, owner, "replacement")
				a.database.Exec(`UPDATE sites SET name='renamed' WHERE id=$1`, site)
				a.database.Exec(`UPDATE sites SET name='site' WHERE user_id=(SELECT user_id FROM sites WHERE id=$1) AND name='replacement'`, site)
			case "abandon":
				a.database.Exec(`UPDATE visitor_sessions SET expires_at=now()-interval '1 second' WHERE user_id=$1`, uid)
				a.database.Exec(`UPDATE visitor_establish_tokens SET expires_at=now()-interval '1 second' WHERE once=$1`, u.Query().Get("once"))
				if err := db.SweepVisitorAuth(context.Background(), a.database); err != nil {
					t.Fatal(err)
				}
				check(0, 0)
				return
			}
			done := a.at(t, "GET", host, u.RequestURI(), nil, nonceCookie(nonce))
			if mode == "complete" {
				if done.status != 302 {
					t.Fatal(done.status)
				}
				check(1, 1)
				id, _ := hex.DecodeString(responseCookie(t, done, visitorCookieHost))
				sess, err := db.GetVisitorSession(context.Background(), a.database, id)
				if err != nil || time.Until(sess.ExpiresAt) < time.Hour {
					t.Fatal("pending session not promoted", err)
				}
				state, _ = a.ssoStartAt(t, host)
				if again := a.ssoHop(t, state, pass); again.status != 302 {
					t.Fatal("consent not remembered")
				}
			} else {
				if done.status < 400 {
					t.Fatal("failed handoff accepted", mode)
				}
				check(0, 0)
			}
		})
	}
}

func TestSSORedteamPendingCap(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "capowner")
	a.deploy(t, owner, "site")
	uid, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "site." + handle + "." + pcSiteDomain
	site := a.siteID(t, owner, "site")
	pass := a.ssoPass(t, owner.key)
	if err := db.RememberSSOConsent(context.Background(), a.database, uid, site); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		state, _ := a.ssoStartAt(t, host)
		got := a.ssoHop(t, state, pass)
		want := 302
		if i == 8 {
			want = 429
		}
		if got.status != want {
			t.Fatalf("pending %d: %d", i, got.status)
		}
	}
}

func TestVisitorRedteamPasscodeIsolation(t *testing.T) {
	f := newVisitorFixture(t)
	if err := f.a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) {
		l.SitePasscodes = true
		l.RatePasscodeIP = config.Rate{Burst: 2, Every: time.Hour}
		l.RatePasscodeSite = config.Rate{Burst: 5, Every: time.Hour}
	})
	if r := f.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.key); r.status != 200 {
		t.Fatal(r.status)
	}
	row, err := db.GetSitePasscode(context.Background(), f.a.database, f.sid)
	if err != nil {
		t.Fatal(err)
	}
	check := func(user, code string) (time.Duration, bool) {
		t.Helper()
		req := httptest.NewRequest("POST", "https://"+f.host+"/", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		if user != "" {
			pass, undo, err := db.IssueInternalVisitorKey(db.InternalVisitorPass{UserID: user, SiteID: f.sid, Host: f.host})
			if err != nil {
				t.Fatal(err)
			}
			defer undo()
			req.Header.Set(visitorPassHeader, pass)
		}
		wait, ok, err := f.a.sites.checkVisitorPasscode(req, row, code)
		if err != nil {
			t.Fatal(err)
		}
		return wait, ok
	}
	check(f.ids["alice"], "wrong")
	check(f.ids["alice"], "wrong")
	if wait, ok := check(f.ids["alice"], "123456"); wait == 0 || ok {
		t.Fatal("attacker not limited")
	}
	if wait, ok := check(f.ids["bob"], "123456"); wait != 0 || !ok {
		t.Fatal("shared AI egress locked other user")
	}
	if wait, ok := check("", "123456"); wait != 0 || !ok {
		t.Fatal("AI locked browser")
	}
	// Exhaust the separate AI site budget using further accounts.
	check(f.ids["bob"], "wrong")
	check(f.ids["bob"], "wrong")
	check(f.ownerID, "wrong")
	if wait, ok := check("", "123456"); wait != 0 || !ok {
		t.Fatal("AI site budget locked browser")
	}
}

func TestSSORedteamConsentCSP(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	owner := a.newPerson(t, "cspowner")
	a.deploy(t, owner, "site")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	state, _ := a.ssoStartAt(t, "site."+handle+"."+pcSiteDomain)
	for _, path := range []string{"/", "/v1/sso/hop?state=" + state} {
		r := a.at(t, http.MethodGet, a.appHost(), path, nil, nil)
		csp := r.header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(strings.Split(strings.Split(csp, "script-src")[1], ";")[0], "'unsafe-inline'") {
			t.Fatal("unsafe apex CSP", path, csp)
		}
	}
}

func TestSSORedteamCustomBinding(t *testing.T) {
	a, _, o := newSSOApp(t)
	owner := a.newPerson(t, "customredowner")
	viewer := a.newPerson(t, "customredviewer")
	a.deploy(t, owner, "first")
	a.deploy(t, owner, "second")
	site := a.siteID(t, owner, "first")
	other := a.siteID(t, owner, "second")
	host := "consent.example.test"
	if _, err := a.database.Exec(`UPDATE sites SET custom_domain=$1,domain_verified_at=NULL WHERE id=$2`, host, site); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := o.sanitizeReturnTo(context.Background(), "https://"+host+"/"); err == nil {
		t.Fatal("pending domain accepted")
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_verified_at=now() WHERE id=$1`, site); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := o.sanitizeReturnTo(context.Background(), "https://"+host+"/"); err != nil {
		t.Fatal("verified domain rejected", err)
	}
	pass := a.ssoPass(t, viewer.key)
	state, nonce := a.ssoStartAt(t, host)
	page := a.ssoHop(t, state, pass)
	hop := a.ssoConsent(t, state, pass, consentToken(t, page), "https://"+a.appHost(), "same-origin")
	if hop.status != 302 {
		t.Fatal(hop.status)
	}
	a.database.Exec(`UPDATE sites SET custom_domain=NULL,domain_verified_at=NULL WHERE id=$1`, site)
	a.database.Exec(`UPDATE sites SET custom_domain=$1,domain_verified_at=now() WHERE id=$2`, host, other)
	u, _ := url.Parse(hop.header.Get("Location"))
	done := a.at(t, "GET", host, u.RequestURI(), nil, nonceCookie(nonce))
	if done.status != 400 {
		t.Fatal("rebound handoff accepted", done.status)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE site_id=$1`, site).Scan(&n); err != nil || n != 0 {
		t.Fatal("rebound pending session survived", n, err)
	}
}

func TestSSORedteamSignoutCascadeOnlyOnce(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "logoutredowner")
	viewer := a.newPerson(t, "logoutredviewer")
	a.deploy(t, owner, "site")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "site." + handle + "." + pcSiteDomain
	pass := a.ssoPass(t, viewer.key)
	session, _ := a.finishSSO(t, host, pass)
	signout := func() resp {
		return a.at(t, "POST", host, "/v1/visitor/logout", nil, map[string]string{"Cookie": visitorCookieHost + "=" + session, "Origin": "https://" + host, "X-SH-CSRF": "1"})
	}
	if r := signout(); r.status != 200 {
		t.Fatal(r.status)
	}
	// A stale site's second request cannot silently revoke a fresh browser family.
	next := a.newPerson(t, "logoutrednext")
	nextPass := a.ssoPass(t, next.key)
	again := signout()
	u, _ := url.Parse(again.json(t)["redirect_url"].(string))
	page := a.at(t, "GET", a.appHost(), u.RequestURI(), nil, map[string]string{"Cookie": ssoCookieHost + "=" + nextPass})
	if page.status != 200 || !strings.Contains(string(page.body), "Sign out of Simple Host on this browser?") {
		t.Fatal("stale site did not require confirmation")
	}
	if _, err := db.GetSSOPass(context.Background(), a.database, db.HashAPIKey(nextPass)); err != nil {
		t.Fatal("stale site revoked new family")
	}
}

func TestSSORedteamLegacyOAuthUsesConsentFlow(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "legacyredowner")
	a.deploy(t, owner, "site")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	host := "site." + handle + "." + pcSiteDomain
	r := a.at(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape("https://"+host+"/"), nil, map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
	if r.status != 302 || !strings.Contains(r.header.Get("Location"), "/v1/sso/hop?") {
		t.Fatal("legacy provider bypassed consent", r.status)
	}
}
