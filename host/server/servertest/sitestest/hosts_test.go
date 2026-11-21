package sitestest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Site hosts off: the site host never serves, no certificate is asked for,
// and the person path still works.
func TestSiteHostsOffLeavesPersonHosts(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetSiteHosts("off", a.CertDir)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	if r := a.At(t, "GET", olive.SiteHost("shop"), "/", nil, nil); r.Status == 200 && string(r.Body) == "<h1>shop</h1>" {
		t.Fatalf("off: site host served the site")
	}
	if r := a.At(t, "GET", olive.Handle+"."+rt.SiteDomain, "/shop/", nil, nil); r.Status != 200 {
		t.Fatalf("off: person host: %d", r.Status)
	}
	if _, err := os.Stat(filepath.Join(a.CertDir, "requests", olive.Handle)); err == nil {
		t.Fatalf("off: certificate requested")
	}
}

// Site hosts canonical: <site>.<handle> serves the site at its root once the
// handle's certificate is ready (the person path redirects there), keeps the
// person path until then, refuses anything outside the site, and follows an
// account's old handle, its own domain and the serve mode.
func TestSiteHostsCanonical(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "shop")
	if err := os.Remove(filepath.Join(a.CertDir, "ready", oscar.Handle)); err != nil {
		t.Fatal(err)
	}
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	person, oscarPerson := olive.Handle+"."+rt.SiteDomain, oscar.Handle+"."+rt.SiteDomain
	shop, blog := olive.SiteHost("shop"), olive.SiteHost("blog")

	// Certificate requests: only people without one, idempotently.
	if _, err := os.Stat(filepath.Join(a.CertDir, "requests", oscar.Handle)); err != nil {
		t.Fatalf("no request for %s: %v", oscar.Handle, err)
	}
	_ = os.Remove(filepath.Join(a.CertDir, "requests", olive.Handle))
	a.Sites.RequestSiteCertsForAll(ctx)
	if _, err := os.Stat(filepath.Join(a.CertDir, "requests", olive.Handle)); err == nil {
		t.Fatalf("request for a person who is ready")
	}

	// Addresses handed out.
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), `"https://`+shop+`/"`) {
		t.Fatalf("site_url: %s", r.Body)
	}
	if r := a.api(t, oscar, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+oscarPerson+"/shop/") {
		t.Fatalf("not-ready site_url: %s", r.Body)
	}

	// Serving at the root, and nothing outside the site.
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("site host: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/sub", nil, nil); r.Status != http.StatusMovedPermanently || location(r) != "/sub/" {
		t.Fatalf("dir without slash: %d %q", r.Status, location(r))
	}
	for _, p := range []string{"/nope.html", "/../../etc/passwd", "/internal/site-redirect/" + olive.Handle + "/blog/", "/.env"} {
		if r := a.At(t, "GET", shop, p, nil, nil); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := a.At(t, "POST", shop, "/", nil, nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST page: %d", r.Status)
	}
	for _, host := range []string{"nope." + person, "shop.nobody-here." + rt.SiteDomain} {
		if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 404 {
			t.Errorf("unknown %s: %d", host, r.Status)
		}
	}
	// Root-absolute links written for older addresses, never protocol-relative.
	if r := a.At(t, "GET", shop, "/"+olive.Handle+"/shop/sub/?x=1", nil, nil); r.Status != http.StatusMovedPermanently || location(r) != "/sub/?x=1" {
		t.Fatalf("content-host prefix: %d %q", r.Status, location(r))
	}
	for _, p := range []string{"//evil.example/x", "/shop//evil.example/x", "/" + olive.Handle + "/shop//evil.example"} {
		if loc := location(a.At(t, "GET", shop, p, nil, nil)); strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
			t.Fatalf("protocol-relative redirect for %s: %q", p, loc)
		}
	}

	// Old addresses redirect, path and query kept.
	if r := a.At(t, "GET", person, "/shop/sub/?q=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+shop+"/sub/?q=1" {
		t.Fatalf("person-path redirect: %d %q", r.Status, location(r))
	}
	if r := a.At(t, "GET", rt.ContentHost, "/internal/site-redirect/"+olive.Handle+"/shop/sub/?q=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+shop+"/sub/?q=1" {
		t.Fatalf("legacy redirect: %d %q", r.Status, location(r))
	}
	// A person without a certificate keeps the person-path form.
	if r := a.At(t, "GET", oscarPerson, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("fallback person path: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", oscar.SiteHost("shop"), "/x?y=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+oscarPerson+"/shop/x?y=1" {
		t.Fatalf("not-ready site host: %d %q", r.Status, location(r))
	}

	// A site with its own domain lives there.
	dom := "olive-blog." + rt.SiteDomain
	if r := a.bind(t, olive, "blog", dom); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", blog, "/a/b?q=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+dom+"/a/b?q=1" {
		t.Fatalf("domain redirect: %d %q", r.Status, location(r))
	}
	// Another person's handle is not a free name.
	if r := a.bind(t, olive, "shop", oscar.Handle+"."+rt.SiteDomain); r.Status != http.StatusConflict {
		t.Fatalf("claim someone's handle: %d %s", r.Status, r.Body)
	}

	// Serve mode: answers, but hands out the person-path form.
	a.Hosts.SetSiteHosts("serve", a.CertDir)
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != 200 {
		t.Fatalf("serve: site host: %d", r.Status)
	}
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+person+"/shop/") {
		t.Fatalf("serve: site_url: %s", r.Body)
	}
	a.Hosts.SetSiteHosts("canonical", a.CertDir)

	// An old handle kept as an alias follows the account.
	if _, err := accountsrepo.RenameHandle(ctx, a.Database, olive.ID, olive.Handle+"-new"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", shop, "/x?y=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+olive.Handle+"-new."+rt.SiteDomain+"/shop/x?y=1" {
		t.Fatalf("alias redirect: %d %q", r.Status, location(r))
	}
	a.MarkReady(t, olive.Handle+"-new")
	if r := a.At(t, "GET", shop, "/x?y=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://shop."+olive.Handle+"-new."+rt.SiteDomain+"/x?y=1" {
		t.Fatalf("alias redirect when ready: %d %q", r.Status, location(r))
	}
}

// Person hosts off: the name is a retired per-name host (legacy 301), and
// addresses handed out are the content-host path.
func TestPersonHostsOffKeepsPathModel(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetPersonHosts("off")
	a.Hosts.SetSiteHosts("off", "")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	if r := a.At(t, "GET", olive.Handle+"."+rt.SiteDomain, "/shop/", nil, nil); r.Status != http.StatusMovedPermanently {
		t.Fatalf("off: person host answered %d", r.Status)
	}
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+rt.ContentHost+"/"+olive.Handle+"/shop/") {
		t.Fatalf("off: site_url: %s", r.Body)
	}
}

// Person hosts serve, then canonical: <handle> serves its own sites by path
// (the person's page lists public ones), never another person's or anything
// outside a site, and the old content-host address follows the mode.
func TestPersonHostsServeAndCanonical(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetPersonHosts("serve")
	a.Hosts.SetSiteHosts("off", "")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "shop") // older same-named site: the legacy name lookup would pick it
	a.Deploy(t, olive, "shop")
	blogID := a.Deploy(t, olive, "blog")
	host := olive.Handle + "." + rt.SiteDomain
	if _, err := a.Database.Exec(`UPDATE sites SET visibility = 'public' WHERE id = $1`, blogID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", host, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("serve site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/shop/sub", nil, nil); r.Status != http.StatusMovedPermanently || location(r) != "/shop/sub/" {
		t.Fatalf("dir without slash: %d %q", r.Status, location(r))
	}
	for _, p := range []string{"/nope/", "/shop/../../../etc/passwd", "/shop/nope.html", "/internal/tls-ask?domain=x", "/mcp", "/.env"} {
		if r := a.At(t, "GET", host, p, nil, nil); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "/blog/") || strings.Contains(string(r.Body), "/shop/\"") {
		t.Fatalf("index: %d", r.Status)
	}
	// The old content-host address: served as-is until canonical, and its
	// directory redirect names the public path whatever headers say.
	if r := a.At(t, "GET", rt.ContentHost, "/internal/site-redirect/"+olive.Handle+"/shop/sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("content-host serve: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", rt.ContentHost, "/%69nternal/site-redirect/"+strings.ToUpper(olive.Handle)+"/shop/sub", nil, map[string]string{"X-Original-URI": "//evil.example/x"}); r.Status != http.StatusMovedPermanently || location(r) != "/"+olive.Handle+"/shop/sub/" {
		t.Fatalf("content-host dir redirect: %d %q", r.Status, location(r))
	}
	for _, p := range []string{"//evil.example/shop/sub", "/" + olive.Handle + "//evil.example/x", "/shop//evil.example"} {
		if loc := location(a.At(t, "GET", host, p, nil, nil)); strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
			t.Fatalf("protocol-relative redirect for %s: %q", p, loc)
		}
	}
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+rt.ContentHost+"/"+olive.Handle+"/shop/") {
		t.Fatalf("serve: site_url: %s", r.Body)
	}
	dom := "olive-blog." + rt.SiteDomain
	if r := a.bind(t, olive, "blog", dom); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/blog/a/b?q=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+dom+"/a/b?q=1" {
		t.Fatalf("domain redirect: %d %q", r.Status, location(r))
	}

	a.Hosts.SetPersonHosts("canonical")
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+host+"/shop/") {
		t.Fatalf("canonical site_url: %s", r.Body)
	}
	if r := a.At(t, "GET", rt.ContentHost, "/internal/site-redirect/"+olive.Handle+"/shop/sub/?q=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+host+"/shop/sub/?q=1" {
		t.Fatalf("content-host redirect: %d %q", r.Status, location(r))
	}
	if _, err := accountsrepo.RenameHandle(context.Background(), a.Database, olive.ID, olive.Handle+"-new"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", host, "/shop/x?y=1", nil, nil); r.Status != http.StatusMovedPermanently || location(r) != "https://"+olive.Handle+"-new."+rt.SiteDomain+"/shop/x?y=1" {
		t.Fatalf("alias redirect: %d %q", r.Status, location(r))
	}
}
