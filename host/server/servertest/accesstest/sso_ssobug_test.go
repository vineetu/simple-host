package accesstest

import (
	"context"
	"net/url"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// A page with <meta name="referrer" content="no-referrer"> sends no Origin
// and no Referer on its own GETs, and Origin: null on its writes. Fetch
// Metadata still says same-origin, so the owner's page is the owner; other
// origins stay refused.
func TestSSONoReferrerPageIsStillOwner(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "noref")
	sid := a.Deploy(t, owner, "shop")
	host := owner.SiteHost("shop")
	cookie := a.Session(t, owner, sid, host)
	for _, origin := range []string{"", "null"} {
		_, c := a.Probe(t, host, "shop", rt.Browser(host, cookie, "Origin", origin))
		if c.VisitorID != owner.ID {
			t.Fatalf("no-referrer page (Origin %q) lost its visitor: %+v", origin, c)
		}
	}
	r := a.At(t, "GET", host, "/v1/sites/shop/me", nil, rt.Browser(host, cookie, "Origin", ""))
	if me := r.JSON(t); r.Status != 200 || me["signed_in"] != true || me["site_owner"] != true {
		t.Fatalf("me from a no-referrer page: %d %v", r.Status, me)
	}
	// Without Fetch Metadata saying same-origin, nothing is attributed.
	for _, h := range []map[string]string{
		rt.Browser(host, cookie, "Origin", "", "Sec-Fetch-Site", "cross-site"),
		rt.Browser(host, cookie, "Origin", "", "Sec-Fetch-Site", "same-site"),
		rt.Browser(host, cookie, "Origin", "null", "Sec-Fetch-Site", "cross-site"),
	} {
		if _, c := a.Probe(t, host, "shop", h); c.VisitorID != "" {
			t.Fatalf("visitor attributed without same-origin: %v", h)
		}
	}
}

// The owner tapped a site link inside another app: sign-in started in that
// app's browser (the nonce cookie lives there) and finished in Safari. The
// code is refused, and signing in again in this browser works.
func TestSSOEstablishOtherBrowserIsRefused(t *testing.T) {
	a, _ := newSSOApp(t)
	owner := a.NewPerson(t, "bugowner")
	viewer := a.NewPerson(t, "bugviewer")
	site := a.Deploy(t, owner, "one")
	host := owner.SiteHost("one")
	pass := a.SSOPass(t, viewer.Key)
	if err := accountsrepo.RememberSSOConsent(context.Background(), a.Database, viewer.ID, site); err != nil {
		t.Fatal(err)
	}
	for name, headers := range map[string]map[string]string{"other browser": nil, "newer sign-in": NonceCookie("another")} {
		t.Run(name, func(t *testing.T) {
			state, _ := a.SSOStartAt(t, host)
			r := a.SSOHop(t, state, pass)
			if r.Status != 302 {
				t.Fatalf("hop: %d", r.Status)
			}
			u, _ := url.Parse(r.Header.Get("Location"))
			if r = a.At(t, "GET", host, u.RequestURI(), nil, headers); r.Status != 400 {
				t.Fatalf("establish: %d %s", r.Status, r.Body)
			}
			// Replay of the burned code: still refused.
			if r = a.At(t, "GET", host, u.RequestURI(), nil, nil); r.Status != 400 {
				t.Fatalf("replay: %d %s", r.Status, r.Body)
			}
		})
	}
	a.FinishSSO(t, host, pass)
}
