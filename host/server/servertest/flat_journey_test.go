package servertest

// Flat addresses (SITE_BASE_MOVE=flat, INTENT 2026-11-22) through the
// composed router: a new account's site at <site>--<handle>.<base> carries
// visitor sign-in, passcode, named viewers, previews and storage exactly as
// a two-label site host does, and an existing account's addresses do not
// change.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

const flatBase = "simple-host-flat.test"

// newFlatApp is NewPasscodeApp under a flat base; new reports whether olive
// is a new account (no certificate of her own) or an existing one.
func newFlatApp(t *testing.T, newAccount bool) *PasscodeApp {
	t.Helper()
	p := NewPasscodeApp(t)
	p.Sites.Hosts.SetSiteBase(flatBase, "flat")
	p.Sites.SetTeamSiteFlatHosts(false)
	accountsrepo.SetPlatformDomains(p.Sites.HandoutBase(), p.Sites.ServedBases()...)
	if newAccount {
		if err := os.Remove(filepath.Join(p.Sites.Hosts.Addressing().SiteCertDir, "ready", p.Handle)); err != nil {
			t.Fatal(err)
		}
		p.Person = p.Handle + "." + flatBase
		p.Host = "trip--" + p.Person
	}
	return p
}

func TestJourneyFlatAddressNewAccount(t *testing.T) {
	p := newFlatApp(t, true)
	ctx := context.Background()
	if r := p.At(t, "GET", PcSiteDomain, "/v1/sites", nil, p.Okey); !strings.Contains(string(r.Body), `"https://`+p.Host+`/"`) {
		t.Fatalf("address handed out: %s", r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/days/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("flat host: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Person, "/trip/days/", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+p.Host+"/days/" {
		t.Fatalf("person path: %d %q", r.Status, r.Header.Get("Location"))
	}
	// Storage and origins: the flat host is the site's own origin.
	if r := p.At(t, "GET", p.Host, PasscodeData, nil, Browser(p.Host, "")); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("storage on the flat host: %d %s", r.Status, r.Body)
	}
	if !p.Sites.Hosts.OriginIsSiteHost(ctx, p.SiteID, p.Host) || p.Sites.Hosts.OriginIsSiteHost(ctx, p.SiteID, "trip."+p.Handle+"."+PcSiteDomain) {
		t.Fatal("origin of the flat host")
	}
	if id, ok := p.Sites.Hosts.ReturnSite(ctx, p.Host, "/days/"); !ok || id != p.SiteID {
		t.Fatalf("sign-in return to the flat host: %q %v", id, ok)
	}
	// Visitor sign-in: the session is this host's and this site's only.
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "blog"}}, p.Okey); r.Status != http.StatusCreated {
		t.Fatalf("second site: %d %s", r.Status, r.Body)
	}
	vic := p.NewPerson(t, "vic")
	ck := p.Session(t, vic, p.SiteID, p.Host)
	if r := p.At(t, "GET", p.Host, "/v1/sites/trip/me", nil, Browser(p.Host, ck)); r.JSON(t)["signed_in"] != true {
		t.Fatalf("/me on the flat host: %s", r.Body)
	}
	blog := "blog--" + p.Person
	if r := p.At(t, "GET", blog, "/v1/sites/blog/me", nil, Browser(blog, ck)); r.JSON(t)["signed_in"] == true {
		t.Fatalf("trip's session accepted on blog: %s", r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/v1/sites/blog/me", nil, Browser(p.Host, ck)); r.Status != http.StatusForbidden && r.Status != http.StatusNotFound {
		t.Fatalf("blog's data through trip's host: %d %s", r.Status, r.Body)
	}
	// A preview link is on the flat host.
	r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/files?publish=false&keep=1", map[string]any{"files": map[string]string{"v2.html": "v2"}}, p.Okey)
	if link, _ := r.JSON(t)["preview_url"].(string); !strings.HasPrefix(link, "https://"+p.Host+"/__preview/") {
		t.Fatalf("preview link: %d %s", r.Status, r.Body)
	} else if r := p.At(t, "GET", p.Host, strings.TrimPrefix(link, "https://"+p.Host)+"v2.html", nil, nil); r.Status != 200 || string(r.Body) != "v2" {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	// Named viewers on blog: signed out asked to sign in, a listed viewer let in.
	mom := p.NewPerson(t, "mom")
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/blog/viewers", map[string]any{"emails": []string{mom.Email}}, p.Okey); r.Status != 200 {
		t.Fatalf("viewers: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", blog, "/", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("named viewers, signed out: %d", r.Status)
	}
	if r := p.At(t, "GET", blog, "/", nil, rgCookie(p.Session(t, mom, p.PrivateApp.SiteID(t, p.Olive, "blog"), blog))); r.Status != 200 || string(r.Body) != "blog" {
		t.Fatalf("named viewer: %d %s", r.Status, r.Body)
	}
	// Passcode: locked, unlocked on the flat host by its own cookie.
	if r := p.Lock(t, "flat-pass-1"); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/app.js", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("locked: %d", r.Status)
	} else {
		rgNoCanary(t, "locked flat host", r)
	}
	r = p.Unlock(t, p.Host, "flat-pass-1", "/app.js", nil)
	if r.Status != http.StatusSeeOther {
		t.Fatalf("unlock: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/app.js", nil, map[string]string{"Cookie": CookieHeader(t, r)}); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("unlocked: %d %s", r.Status, r.Body)
	}
}

// An existing account (a certificate of its own) keeps its two-label
// addresses, old and new sites alike; the flat shapes only redirect there.
// A new handle has no certificate, so a handle change moves it to flat.
func TestJourneyFlatExistingAccountUnchanged(t *testing.T) {
	p := newFlatApp(t, false)
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "blog"}}, p.Okey); r.Status != http.StatusCreated || !strings.Contains(string(r.Body), "https://blog."+p.Person+"/") {
		t.Fatalf("new site of an existing account: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/days/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("two-label host: %d", r.Status)
	}
	for host, want := range map[string]string{
		"trip--" + p.Handle + "." + flatBase: "https://" + p.Host + "/days/",
		p.Handle + "." + flatBase:            "https://" + p.Person + "/days/",
		"trip." + p.Handle + "." + flatBase:  "https://" + p.Host + "/days/",
	} {
		if r := p.At(t, "GET", host, "/days/", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != want {
			t.Errorf("%s: %d %q, want %q", host, r.Status, r.Header.Get("Location"), want)
		}
	}
	vic := p.NewPerson(t, "vic")
	flat := "trip--" + p.Handle + "." + flatBase
	if r := p.At(t, "GET", flat, "/v1/sites/trip/me", nil, Browser(flat, p.Session(t, vic, p.SiteID, flat))); r.Status != http.StatusNotFound {
		t.Fatalf("the API on the flat shape of a two-label site: %d %s", r.Status, r.Body)
	}
	// A handle change: the new handle has no certificate, so it is flat.
	next := p.Handle + "-x"
	if r := p.At(t, "PATCH", PcSiteDomain, "/v1/me", map[string]any{"handle": next}, p.Okey); r.Status != 200 {
		t.Fatalf("handle change: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/days/", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://trip--"+next+"."+flatBase+"/days/" {
		t.Fatalf("old address after a handle change: %d %q", r.Status, r.Header.Get("Location"))
	}
}

// The name rules for every account from now on: no "--" in a new site
// name, handle or team name, and <site>--<handle> must fit 63 characters.
func TestFlatNameRules(t *testing.T) {
	p := newFlatApp(t, true)
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/my--shop/files", map[string]any{"files": map[string]string{"index.html": "x"}}, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("site name with --: %d %s", r.Status, r.Body)
	}
	long := strings.Repeat("a", 62-len(p.Handle))
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/"+long+"/files", map[string]any{"files": map[string]string{"index.html": "x"}}, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("site name too long for its flat address: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/"+long[1:]+"/files", map[string]any{"files": map[string]string{"index.html": "x"}}, p.Okey); r.Status != http.StatusCreated {
		t.Fatalf("site name that fits: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PATCH", PcSiteDomain, "/v1/me", map[string]any{"handle": "ol--ive"}, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("handle with --: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PATCH", PcSiteDomain, "/v1/me", map[string]any{"handle": p.Handle + "x"}, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("handle too long for the longest site: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", PcSiteDomain, "/v1/teams", map[string]any{"name": "a--team"}, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("team name with --: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/xn/files", map[string]any{"files": map[string]string{"index.html": "x"}}, p.Okey); r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "xn--") {
		t.Fatalf("site named xn: %d %s", r.Status, r.Body)
	}
	// Restore runs the same check: a trashed site that no longer fits the
	// account's handle stays in Recently deleted.
	if r := p.At(t, "DELETE", PcSiteDomain, "/v1/sites/"+long[1:], nil, p.Okey); r.Status/100 != 2 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	uid, _ := p.UserID(t, p.Olive)
	if _, err := p.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, p.Handle+"q", uid); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/"+long[1:]+"/restore", nil, p.Okey); r.Status != http.StatusBadRequest {
		t.Fatalf("restore past the label limit: %d %s", r.Status, r.Body)
	}
	// A profile update that echoes the current handle is not judged again,
	// even when that handle predates today's rules.
	if _, err := p.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, "ol--"+p.Handle, uid); err != nil {
		t.Fatal(err)
	}
	if r := p.At(t, "PATCH", PcSiteDomain, "/v1/me", map[string]any{"handle": "ol--" + p.Handle, "display_name": "Olive"}, p.Okey); r.Status != 200 {
		t.Fatalf("unchanged handle echoed: %d %s", r.Status, r.Body)
	}
}
