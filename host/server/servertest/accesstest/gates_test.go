package accesstest

// Access's visitor gates, each rule refused and allowed once: StorageCaller
// (which caller a data route gets, through a.Probe), PasscodeGate (the
// gate-probe route) and SiteGate (a page on the site's own host).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// gateSite is ann's live site "notes" and bob, another account.
type gateSite struct {
	*App
	Ann, Bob Person
	SiteID   string
	Host     string // the site's own host
}

func newGateSite(t *testing.T) *gateSite {
	t.Helper()
	a := NewApp(t)
	g := &gateSite{App: a, Ann: a.NewPerson(t, "gate-ann"), Bob: a.NewPerson(t, "gate-bob")}
	g.SiteID = a.Deploy(t, g.Ann, "notes")
	g.Host = g.Ann.SiteHost("notes")
	return g
}

// gatePass is an AI visitor's pass for who on siteID at host.
func gatePass(t *testing.T, who Person, siteID, host string) string {
	t.Helper()
	pass, revoke, err := accountsrepo.IssueInternalVisitorKey(model.InternalVisitorPass{UserID: who.ID, SiteID: siteID, Host: host, AppName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revoke)
	return pass
}

func gateWant(t *testing.T, what string, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status || (code != "" && r.Code() != code) {
		t.Fatalf("%s: %d %s (want %d %s)", what, r.Status, r.Body, status, code)
	}
}

// Who the data routes are told is calling.
func TestGateStorageCaller(t *testing.T) {
	g := newGateSite(t)
	g.Deploy(t, g.Bob, "board")
	sess := g.Session(t, g.Bob, g.SiteID, g.Host)
	pass := gatePass(t, g.Bob, g.SiteID, g.Host)
	boardHost := g.Bob.SiteHost("board")
	for _, tc := range []struct {
		name, host string
		h          map[string]string
		status     int
		ok         func(CallJSON) bool
	}{
		{"owner key", rt.SiteDomain, g.Ann.Header(), 200, func(c CallJSON) bool { return c.Owner && c.SiteID == g.SiteID && c.AccountID == g.Ann.ID }},
		{"another account's key", rt.SiteDomain, g.Bob.Header(), 403, nil},
		{"admin key", rt.SiteDomain, map[string]string{"X-API-Key": g.Admin}, 200, func(c CallJSON) bool { return c.Owner }},
		{"no key on the apex", rt.SiteDomain, nil, 403, nil},
		{"no key on the content host", rt.ContentHost, rt.Browser(rt.ContentHost, ""), 403, nil},
		{"a browser from another origin", g.Host, rt.Browser(g.Host, "", "Origin", "https://evil.example"), 403, nil},
		{"a browser on the site's host", g.Host, rt.Browser(g.Host, ""), 200, func(c CallJSON) bool { return !c.Owner && c.VisitorID == "" && c.PasscodeOK && c.ViewersOK }},
		{"a visitor session", g.Host, rt.Browser(g.Host, sess), 200, func(c CallJSON) bool { return !c.Owner && c.VisitorID == g.Bob.ID && c.OnOwnAddress }},
		{"a visitor pass", g.Host, map[string]string{accesshttp.VisitorPassHeader: pass, "Origin": "https://" + g.Host}, 200, func(c CallJSON) bool { return c.VisitorAI && !c.Owner }},
		{"the pass on another site", boardHost, map[string]string{accesshttp.VisitorPassHeader: pass, "Origin": "https://" + boardHost}, 403, nil},
	} {
		site := "notes"
		if tc.host == boardHost {
			site = "board"
		}
		r, c := g.Probe(t, tc.host, site, tc.h)
		if r.Status != tc.status || (tc.ok != nil && !tc.ok(c)) {
			t.Fatalf("%s: %d %s", tc.name, r.Status, r.Body)
		}
	}

	// An editor's key with owner=<account> acts for the site, its file links
	// scoped to the editor; a non-editor's key with owner= is refused. (The
	// probe also answers on storage/usage, a route editors may use.)
	bea := teamNewEditor(t, g.App, g.Ann, "notes")
	asFor := func(p Person) (Resp, CallJSON) {
		r := g.At(t, "GET", rt.SiteDomain, "/v1/sites/notes/storage/usage?owner="+g.Ann.Handle, nil, p.Header())
		var c CallJSON
		_ = json.Unmarshal(r.Body, &c)
		return r, c
	}
	if r, c := asFor(bea); r.Status != 200 || !c.Owner || c.SiteID != g.SiteID || c.LinkScope != "as:"+bea.ID {
		t.Fatalf("editor's key: %d %s", r.Status, r.Body)
	}
	if r, _ := asFor(g.Bob); r.Status < 400 {
		t.Fatalf("non-editor's key: %d %s", r.Status, r.Body)
	}
}

// Operator holds and offline beat every caller but the owner (offline only).
func TestGateStorageCallerHolds(t *testing.T) {
	g := newGateSite(t)
	admin := map[string]string{"X-API-Key": g.Admin}
	visitor := rt.Browser(g.Host, "")
	probe := func(what string, h map[string]string, host string, status int, code string) {
		t.Helper()
		r, _ := g.Probe(t, host, "notes", h)
		gateWant(t, what, r, status, code)
	}

	// Offline: visitors refused, the owner not.
	if err := sitesrepo.SetSiteOffline(context.Background(), g.Database, g.SiteID, true); err != nil {
		t.Fatal(err)
	}
	probe("offline visitor", visitor, g.Host, 403, "site_offline")
	probe("offline owner", g.Ann.Header(), rt.SiteDomain, 200, "")
	if err := sitesrepo.SetSiteOffline(context.Background(), g.Database, g.SiteID, false); err != nil {
		t.Fatal(err)
	}

	// A suspended site: everyone, the owner included.
	gateWant(t, "suspend site", g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/suspend", map[string]string{"reason": "abuse"}, admin), 200, "")
	probe("suspended site owner", g.Ann.Header(), rt.SiteDomain, 403, "site_suspended")
	probe("suspended site visitor", visitor, g.Host, 403, "site_suspended")
	gateWant(t, "restore site", g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/restore", nil, admin), 200, "")
	probe("restored visitor", visitor, g.Host, 200, "")

	// A suspended account: its key and its site's visitors.
	gateWant(t, "suspend account", g.At(t, "POST", rt.SiteDomain, "/v1/admin/users/"+g.Ann.ID+"/suspend", map[string]string{"reason": "abuse"}, admin), 200, "")
	if r, _ := g.Probe(t, rt.SiteDomain, "notes", g.Ann.Header()); r.Status == 200 {
		t.Fatalf("suspended account's key: %d %s", r.Status, r.Body)
	}
	if r, _ := g.Probe(t, g.Host, "notes", visitor); r.Status == 200 {
		t.Fatalf("suspended account's site: %d %s", r.Status, r.Body)
	}
}

// The passcode and named-viewers rules, as the Call carries them.
func TestGateStorageCallerProtection(t *testing.T) {
	p := NewPasscodeSite(t)
	gateWant(t, "lock", p.Lock(t, "gate-pass-1"), 200, "")
	if r, c := p.Probe(t, p.Host, "trip", rt.Browser(p.Host, "")); r.Status != 200 || c.PasscodeOK {
		t.Fatalf("no unlock: %d %s", r.Status, r.Body)
	}
	ck := CookieHeader(t, p.Unlock(t, p.Host, "gate-pass-1", "/", nil))
	if r, c := p.Probe(t, p.Host, "trip", rt.Browser(p.Host, "", "Cookie", ck)); r.Status != 200 || !c.PasscodeOK {
		t.Fatalf("unlocked: %d %s", r.Status, r.Body)
	}

	gateWant(t, "unlock", p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey), 200, "")
	mom, dad := p.NewPerson(t, "gate-mom"), p.NewPerson(t, "gate-dad")
	gateWant(t, "grant", p.Grant(t, mom.Email), 200, "")
	if r, c := p.Probe(t, p.Host, "trip", rt.Browser(p.Host, p.Session(t, dad, p.SiteID, p.Host))); r.Status != 200 || c.ViewersOK {
		t.Fatalf("unlisted viewer: %d %s", r.Status, r.Body)
	}
	if r, c := p.Probe(t, p.Host, "trip", rt.Browser(p.Host, p.Session(t, mom, p.SiteID, p.Host))); r.Status != 200 || !c.ViewersOK {
		t.Fatalf("listed viewer: %d %s", r.Status, r.Body)
	}
}

// PasscodeGate on a page-facing data route.
func TestGatePasscodeGate(t *testing.T) {
	p := NewPasscodeSite(t)
	probe := func(host string, h map[string]string) Resp {
		return p.At(t, "GET", host, "/v1/sites/trip/gate-probe", nil, h)
	}
	gateWant(t, "open site", probe(p.Host, rt.Browser(p.Host, "")), 200, "")
	p.Lock(t, "gate-pass-2")
	gateWant(t, "locked, no unlock", probe(p.Host, rt.Browser(p.Host, "")), 403, "site_locked")
	gateWant(t, "locked, owner key", probe(rt.SiteDomain, p.Okey), 200, "")
	gateWant(t, "locked, admin key", probe(rt.SiteDomain, map[string]string{"X-API-Key": p.Admin}), 200, "")
	ck := CookieHeader(t, p.Unlock(t, p.Host, "gate-pass-2", "/", nil))
	gateWant(t, "locked, unlock cookie", probe(p.Host, rt.Browser(p.Host, "", "Cookie", ck)), 200, "")

	p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey)
	mom, dad := p.NewPerson(t, "gate-mom"), p.NewPerson(t, "gate-dad")
	p.Grant(t, mom.Email)
	gateWant(t, "unlisted viewer", probe(p.Host, rt.Browser(p.Host, p.Session(t, dad, p.SiteID, p.Host))), 403, "site_private")
	gateWant(t, "listed viewer", probe(p.Host, rt.Browser(p.Host, p.Session(t, mom, p.SiteID, p.Host))), 200, "")
}

// SiteGate on a page: take-down and offline win over everything.
func TestGateSitePageHolds(t *testing.T) {
	g := newGateSite(t)
	admin := map[string]string{"X-API-Key": g.Admin}
	page := func() Resp { return g.At(t, "GET", g.Host, "/", nil, nil) }
	gateWant(t, "live", page(), 200, "")

	gateWant(t, "suspend", g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/suspend", map[string]string{"reason": "abuse"}, admin), 200, "")
	if r := page(); r.Status != http.StatusGone {
		t.Fatalf("taken down: %d %s", r.Status, r.Body)
	}
	g.At(t, "POST", rt.SiteDomain, "/v1/admin/sites/"+g.SiteID+"/restore", nil, admin)
	gateWant(t, "restored", page(), 200, "")

	if err := g.Disk.SetOffline(g.Ann.ID, "notes", true); err != nil {
		t.Fatal(err)
	}
	if r := page(); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	if err := g.Disk.SetOffline(g.Ann.ID, "notes", false); err != nil {
		t.Fatal(err)
	}
	gateWant(t, "back online", page(), 200, "")
}

// What a visitor tool may show: identity values never, field names yes.
func TestGateVisitorPrivateData(t *testing.T) {
	out := accesshttp.VisitorPrivateData(accesshttp.VisitorStructure{Value: map[string]string{"email": "TEXT", "label": "private@example.com"}}).(map[string]any)
	if out["email"] != "TEXT" || out["label"] == "private@example.com" {
		t.Fatalf("%+v", out)
	}
}
