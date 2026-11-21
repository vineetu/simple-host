package accesstest

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// A page with <meta name="referrer" content="no-referrer"> (the owner's
// wes-intake page, 2026-10-30) sends no Origin and no Referer on its own GETs,
// and Origin: null on its writes. Fetch Metadata still says same-origin, so
// the page reads and saves its own data; other origins stay refused.
func TestNoReferrerPageUsesItsOwnData(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	cookie := strings.TrimPrefix(s.Callers["owner-site"]["Cookie"], accesshttp.VisitorCookieHost+"=")
	s.Must(t, "PUT", "/resources/intake", map[string]string{"kind": "kv"}, s.Key, 201)

	get := servertest.Browser(s.Host, cookie, "Origin", "")
	r := s.A.At(t, "GET", s.Host, "/v1/sites/shop/me", nil, get)
	if r.Status != 200 {
		t.Fatalf("me from a no-referrer page: %d %s", r.Status, r.Body)
	}
	if me := r.JSON(t); me["signed_in"] != true || me["site_owner"] != true {
		t.Fatalf("me: %v", me)
	}
	s.Must(t, "PUT", "/kv/intake/keys/vineet", `{"value":{"a":1}}`, servertest.Browser(s.Host, cookie, "Origin", "null"), 200)
	s.Must(t, "GET", "/kv/intake/keys/vineet", nil, get, 200)

	// Without Fetch Metadata saying same-origin, nothing is attributed.
	for _, h := range []map[string]string{
		servertest.Browser(s.Host, cookie, "Origin", "", "Sec-Fetch-Site", "cross-site"),
		servertest.Browser(s.Host, cookie, "Origin", "", "Sec-Fetch-Site", "same-site"),
		servertest.Browser(s.Host, cookie, "Origin", "null", "Sec-Fetch-Site", "cross-site"),
	} {
		if r := s.A.At(t, "GET", s.Host, "/v1/sites/shop/me", nil, h); r.Status == 200 && r.JSON(t)["site_owner"] == true {
			t.Fatalf("owner rights without same-origin: %v", h)
		}
		if r := s.Do(t, "GET", "/kv/intake/keys/vineet", nil, h); r.Status == 200 {
			t.Fatalf("cross-origin read: %v %s", h, r.Body)
		}
	}
}

// The owner tapped a site link inside another app: sign-in started in that
// app's browser (the nonce cookie lives there) and finished in Safari. The
// code is still refused, but the page now says why and Try again signs in
// in this browser.
func TestEstablishOtherBrowserOffersTryAgain(t *testing.T) {
	a, dir, _ := newSSOApp(t)
	owner := a.NewPerson(t, "bugowner")
	viewer := a.NewPerson(t, "bugviewer")
	a.Deploy(t, owner, "one")
	_, h := a.UserID(t, owner)
	servertest.MarkReady(t, dir, h)
	host := "one." + h + "." + servertest.PcSiteDomain
	pass := a.SSOPass(t, viewer.Key)
	uid, _ := a.UserID(t, viewer)
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, uid, a.SiteID(t, owner, "one")); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, nonce, want string }{
		{"other browser", "", "different browser or app"},
		{"newer sign-in", "another", "newer sign-in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, _ := a.SSOStartAt(t, host)
			r := a.SSOHop(t, state, pass)
			if r.Status != 302 {
				t.Fatalf("hop: %d", r.Status)
			}
			u, _ := url.Parse(r.Header.Get("Location"))
			var headers map[string]string
			if tc.nonce != "" {
				headers = servertest.NonceCookie(tc.nonce)
			}
			r = a.At(t, "GET", host, u.RequestURI(), nil, headers)
			body := string(r.Body)
			if r.Status != 400 || !strings.Contains(body, tc.want) {
				t.Fatalf("establish: %d %s", r.Status, body)
			}
			if !strings.Contains(body, `href="https://`+host+`/page?ok=1"`) || !strings.Contains(body, "Try again") {
				t.Fatalf("no Try again to the site: %s", body)
			}
			// Replay of the burned code: still refused, still a way back.
			r = a.At(t, "GET", host, u.RequestURI(), nil, nil)
			if r.Status != 400 || !strings.Contains(string(r.Body), `href="https://`+host+`/"`) {
				t.Fatalf("replay: %d %s", r.Status, r.Body)
			}
		})
	}
	// Try again in this browser: a fresh hop with its own nonce works.
	a.FinishSSO(t, host, pass)
}
