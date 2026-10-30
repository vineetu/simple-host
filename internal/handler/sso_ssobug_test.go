package handler

import (
	"context"
	"net/url"
	"strings"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

// A page with <meta name="referrer" content="no-referrer"> (the owner's
// wes-intake page, 2026-10-30) sends no Origin and no Referer on its own GETs,
// and Origin: null on its writes. Fetch Metadata still says same-origin, so
// the page reads and saves its own data; other origins stay refused.
func TestNoReferrerPageUsesItsOwnData(t *testing.T) {
	s := newPresetSite(t)
	cookie := strings.TrimPrefix(s.callers["owner-site"]["Cookie"], visitorCookieHost+"=")
	s.must(t, "PUT", "/resources/intake", map[string]string{"kind": "kv", "preset": "private"}, s.key, 201)

	get := browser(s.host, cookie, "Origin", "")
	r := s.a.at(t, "GET", s.host, "/v1/sites/shop/me", nil, get)
	if r.status != 200 {
		t.Fatalf("me from a no-referrer page: %d %s", r.status, r.body)
	}
	if me := r.json(t); me["signed_in"] != true || me["site_owner"] != true {
		t.Fatalf("me: %v", me)
	}
	s.must(t, "PUT", "/kv/intake/keys/vineet", `{"value":{"a":1}}`, browser(s.host, cookie, "Origin", "null"), 200)
	s.must(t, "GET", "/kv/intake/keys/vineet", nil, get, 200)

	// Without Fetch Metadata saying same-origin, nothing is attributed.
	for _, h := range []map[string]string{
		browser(s.host, cookie, "Origin", "", "Sec-Fetch-Site", "cross-site"),
		browser(s.host, cookie, "Origin", "", "Sec-Fetch-Site", "same-site"),
		browser(s.host, cookie, "Origin", "null", "Sec-Fetch-Site", "cross-site"),
	} {
		if r := s.a.at(t, "GET", s.host, "/v1/sites/shop/me", nil, h); r.status == 200 && r.json(t)["site_owner"] == true {
			t.Fatalf("owner rights without same-origin: %v", h)
		}
		if r := s.do(t, "GET", "/kv/intake/keys/vineet", nil, h); r.status == 200 {
			t.Fatalf("cross-origin read: %v %s", h, r.body)
		}
	}
}

// The owner tapped a site link inside another app: sign-in started in that
// app's browser (the nonce cookie lives there) and finished in Safari. The
// code is still refused, but the page now says why and Try again signs in
// in this browser.
func TestEstablishOtherBrowserOffersTryAgain(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.newPerson(t, "bugowner")
	viewer := a.newPerson(t, "bugviewer")
	a.deploy(t, owner, "one")
	_, h := a.userID(t, owner)
	markReady(t, dir, h)
	host := "one." + h + "." + pcSiteDomain
	pass := a.ssoPass(t, viewer.key)
	uid, _ := a.userID(t, viewer)
	if err := db.RememberSSOConsent(context.Background(), a.database, uid, a.siteID(t, owner, "one")); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, nonce, want string }{
		{"other browser", "", "different browser or app"},
		{"newer sign-in", "another", "newer sign-in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, _ := a.ssoStartAt(t, host)
			r := a.ssoHop(t, state, pass)
			if r.status != 302 {
				t.Fatalf("hop: %d", r.status)
			}
			u, _ := url.Parse(r.header.Get("Location"))
			var headers map[string]string
			if tc.nonce != "" {
				headers = nonceCookie(tc.nonce)
			}
			r = a.at(t, "GET", host, u.RequestURI(), nil, headers)
			body := string(r.body)
			if r.status != 400 || !strings.Contains(body, tc.want) {
				t.Fatalf("establish: %d %s", r.status, body)
			}
			if !strings.Contains(body, `href="https://`+host+`/page?ok=1"`) || !strings.Contains(body, "Try again") {
				t.Fatalf("no Try again to the site: %s", body)
			}
			// Replay of the burned code: still refused, still a way back.
			r = a.at(t, "GET", host, u.RequestURI(), nil, nil)
			if r.status != 400 || !strings.Contains(string(r.body), `href="https://`+host+`/"`) {
				t.Fatalf("replay: %d %s", r.status, r.body)
			}
		})
	}
	// Try again in this browser: a fresh hop with its own nonce works.
	a.finishSSO(t, host, pass)
}
