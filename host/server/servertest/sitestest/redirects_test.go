package sitestest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

const publicPageCache = "public, max-age=0, must-revalidate"

// checkPageRedirect: GET and HEAD of request on host 301 to location with the
// query kept and the given Cache-Control.
func checkPageRedirect(t *testing.T, a *App, host, request, location, cache string) {
	t.Helper()
	const query = "?one=a%2Fb&one=two"
	for _, method := range []string{"GET", "HEAD"} {
		r := a.At(t, method, host, request+query, nil, nil)
		if r.Status != 301 || r.Header.Get("Location") != location+query || r.Header.Get("Cache-Control") != cache || method == "HEAD" && len(r.Body) != 0 {
			t.Fatalf("%s %s%s: %d %v, body %q", method, host, request, r.Status, r.Header, r.Body)
		}
	}
}

// A zip entry with a non-UTF-8 name is served by its percent-encoded bytes.
func TestZIPPageWithNonUTF8Name(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "zip-pages")
	a.MarkReady(t, owner.Handle)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	// This pair validates only when the published file shadows the first rule.
	for name, body := range map[string]string{"index.html": "home", "caf\xe9.html": "original bytes", "caf%E9.html": "literal percent", "_redirects": "/caf%E9 /old\n/old /caf%E9"} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/blog", buf.String(), map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/zip"}); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	for p, want := range map[string]string{"/caf%E9.html": "original bytes", "/caf%E9": "original bytes", "/caf%25E9": "literal percent"} {
		if r := a.At(t, "GET", owner.SiteHost("blog"), p, nil, nil); r.Status != 200 || string(r.Body) != want {
			t.Errorf("%s: %d %q", p, r.Status, r.Body)
		}
	}
}

// A site's _redirects never runs before the gate: a protected, offline or
// taken-down site never reveals a redirect target.
func TestRedirectsAfterGates(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	host := olive.SiteHost("trip")
	if r := a.files(t, olive, "POST", "trip", map[string]string{"index.html": "x", "_redirects": "/old https://example.com/secret 302!"}); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	a.MarkReady(t, olive.Handle)
	if r := a.At(t, "GET", host, "/old", nil, nil); r.Status != 302 || location(r) != "https://example.com/secret" {
		t.Fatal(r.Status, r.Header)
	}
	leaks := func(stage string) {
		t.Helper()
		if r := a.At(t, "GET", host, "/old", nil, nil); location(r) != "" || strings.Contains(string(r.Body), "example.com/secret") {
			t.Fatalf("%s leaked the target: %d %v", stage, r.Status, r.Header)
		}
	}
	a.protect(t, olive, "trip")
	leaks("passcode")
	if err := a.Disk.SetPasscodeMarker(olive.ID, "trip", false); err != nil {
		t.Fatal(err)
	}
	if r := a.api(t, olive, "PATCH", "/v1/sites/trip", map[string]bool{"offline": true}); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	leaks("offline")
	if r := a.api(t, olive, "PATCH", "/v1/sites/trip", map[string]bool{"offline": false}); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	a.suspend(t, olive, "trip")
	leaks("take-down")
}

// _redirects rules apply on every address (site host, person path, content
// host, claimed name, custom domain), after real files, with the folder
// slash redirect, and a rule loop is refused at deploy.
func TestRedirectsOnAddresses(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetSiteHosts("serve", a.CertDir)
	a.Hosts.SetPersonHosts("serve")
	owner := a.NewPerson(t, "redirect-owner")
	a.MarkReady(t, owner.Handle)
	handle := owner.Handle
	files := map[string]string{
		"index.html":  "home",
		"faq.html":    "faq",
		"x.html":      "catch-all shadow",
		"a/b/c.html":  "nested page",
		"assets/logo": "logo",
		"_redirects": `/old /new 302!
/faq/ /faq
/forced/ /new 302!
/_redirects /404.html 404
/* /index.html 302`,
	}
	if r := a.files(t, owner, "POST", "blog", files); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	siteHost, person := owner.SiteHost("blog"), handle+"."+rt.SiteDomain
	for _, tt := range []struct{ host, path, want string }{{siteHost, "/old", "/new"}, {person, "/blog/old", "/blog/new"}, {rt.ContentHost, "/internal/site-redirect/" + handle + "/blog/old", "/" + handle + "/blog/new"}} {
		if r := a.At(t, "GET", tt.host, tt.path, nil, nil); r.Status != 302 || location(r) != tt.want {
			t.Fatalf("%+v: %d %v", tt, r.Status, r.Header)
		}
	}
	if r := a.At(t, "GET", rt.ContentHost, "/internal/notfound", nil, map[string]string{"X-Original-URI": "/" + handle + "/blog/faq/?q=a%2Fb"}); r.Status != 404 || location(r) != "" {
		t.Fatal(r.Status, r.Header)
	}
	checkPageRedirect(t, a, rt.ContentHost, "/internal/passcode/"+handle+"/blog/%66aq/", "/"+handle+"/blog/%66aq", publicPageCache)
	checkPageRedirect(t, a, siteHost, "/faq/", "/faq", publicPageCache)
	// The redirect only removes the slash; the next request serves the page.
	checkPageRedirect(t, a, person, "/blog/a%2Fb/%2e/c/", "/blog/a%2Fb/%2e/c", publicPageCache)
	if r := a.At(t, "GET", person, "/blog/a%2Fb/%2e/c", nil, nil); r.Status != 200 || string(r.Body) != "nested page" {
		t.Fatal(r.Status, string(r.Body))
	}
	if r := a.At(t, "GET", siteHost, "/assets", nil, nil); r.Status != 404 || location(r) != "" {
		t.Fatal("a folder without a page:", r.Status, r.Header)
	}
	if r := a.At(t, "GET", siteHost, "/forced/", nil, nil); r.Status != 302 || location(r) != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	if r := a.At(t, "GET", siteHost, "/_redirects", nil, nil); r.Status != 404 {
		t.Fatal("the rules file served:", r.Status, string(r.Body))
	}
	if r := a.files(t, owner, "PUT", "blog", map[string]string{"index.html": "home", "x/index.html": "folder", "_redirects": "/x/ /x 301!"}); r.Status != 400 || r.Code() != "redirects_invalid" {
		t.Fatal(r.Status, string(r.Body))
	}
	claimed := uniq("redirect") + "." + rt.SiteDomain
	if r := a.bind(t, owner, "blog", claimed); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	if r := a.At(t, "GET", claimed, "/old", nil, nil); r.Status != 302 || location(r) != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	custom := uniq("redirect") + ".example.test"
	if r := a.bind(t, owner, "blog", custom); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	a.verify(t, a.SiteID(t, owner, "blog"), custom)
	checkPageRedirect(t, a, custom, "/internal/passcode/%66aq/", "/%66aq", publicPageCache)
	if r := a.At(t, "GET", custom, "/internal/passcode/old", nil, nil); r.Status != 302 || location(r) != "/new" {
		t.Fatal(r.Status, r.Header)
	}
}

// _redirects apply on a family address and on a flat group site host.
func TestRedirectsOnFamilyAndGroupSite(t *testing.T) {
	f := newFamilyApp(t)
	owner := f.NewPerson(t, "family-redirect")
	files := map[string]string{"index.html": "home", "_redirects": "/old /new 301!", "faq.html": "file"}
	if r := f.files(t, owner, "POST", "voucher-shop", files); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	f.liveFamily(t, owner, "redirects.fam.test", "voucher-", false, 0)
	if r := f.At(t, "GET", "shop.redirects.fam.test", "/old", nil, nil); r.Status != 301 || location(r) != "/new" {
		t.Fatal(r.Status, r.Header)
	}
	checkPageRedirect(t, f.App, "shop.redirects.fam.test", "/internal/family/%66aq/", "/%66aq", publicPageCache)

	b := NewApp(t)
	event := b.withHandle(t, b.NewPerson(t, "redirect-event"), uniq("weekend"))
	if r := b.files(t, event, "POST", "alpha", files); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	b.asGroupFlat()
	if r := b.At(t, "GET", "alpha--"+event.Handle+"."+rt.SiteDomain, "/old", nil, nil); r.Status != 301 || location(r) != "/new" {
		t.Fatal(r.Status, r.Header)
	}
}
