package gate

// Access's visitor gates, each rule refused and allowed once: StorageCaller
// (which caller a data route gets, through a.Probe), PasscodeGate (the
// gate-probe route) and SiteGate (a page on the site's own host).

import (
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	"github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
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

// What a visitor tool may show: identity values never, field names yes.
func TestGateVisitorPrivateData(t *testing.T) {
	out := access.VisitorPrivateData(access.VisitorStructure{Value: map[string]string{"email": "TEXT", "label": "private@example.com"}}).(map[string]any)
	if out["email"] != "TEXT" || out["label"] == "private@example.com" {
		t.Fatalf("%+v", out)
	}
}
