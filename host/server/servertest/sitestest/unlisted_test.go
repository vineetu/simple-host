package sitestest

import (
	"context"
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Profile listing never changes search indexing, on any address. Needs DB_DSN.
func TestProfileListingIndexableEveryAddress(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": map[string]string{
		"index.html": "<h1>blog</h1>",
		"robots.txt": "User-agent: *\nAllow: /own\n",
	}}, map[string]string{"X-API-Key": olive.Key})
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy blog: %d %s", r.Status, r.Body)
	}
	uid, oh := a.UserID(t, olive)
	okey := map[string]string{"X-API-Key": olive.Key}
	ctx := context.Background()
	siteHost := "shop." + oh + "." + servertest.PcSiteDomain
	person := oh + "." + servertest.PcSiteDomain

	// Even a leftover marker from an older binary has no serving effect.
	if err := os.WriteFile(filepath.Join(a.Sites.Disk.SiteDir(uid, "shop"), "unlisted"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Each address form, as the visitor (or nginx's rewrite) reaches it.
	type addr struct{ name, host, path, robots string }
	addrs := func() []addr {
		return []addr{
			{"person path (certificate pending)", person, "/shop/", ""},
			{"legacy content host", servertest.PcContentHost, "/internal/site-redirect/" + oh + "/shop/", ""},
			{"site host", siteHost, "/", "/robots.txt"},
		}
	}
	pending := true
	check := func(when string) {
		t.Helper()
		// A person path serves the file until the site host is ready, then
		// redirects to that file on its canonical address.
		personRobots := a.At(t, "GET", person, "/shop/robots.txt", nil, nil)
		if (personRobots.Status != 404 && personRobots.Status != 302) || personRobots.Header.Get("X-Robots-Tag") != "" {
			t.Errorf("%s: person robots: %d %q", when, personRobots.Status, personRobots.Header.Get("X-Robots-Tag"))
		}
		for _, x := range addrs() {
			if pending && x.robots != "" {
				x.path, x.robots = "/", "" // a 302 to the person path until the certificate is ready
			}
			r := a.At(t, "GET", x.host, x.path, nil, nil)
			if r.Header.Get("X-Robots-Tag") != "" || r.Status >= 400 {
				t.Errorf("%s: %s %s%s: %d X-Robots-Tag %q", when, x.name, x.host, x.path, r.Status, r.Header.Get("X-Robots-Tag"))
			}
			if x.robots == "" {
				continue
			}
			rb := a.At(t, "GET", x.host, x.robots, nil, nil)
			if rb.Status != 404 || rb.Header.Get("X-Robots-Tag") != "" {
				t.Errorf("%s: %s robots.txt on a public site: %d %q", when, x.name, rb.Status, rb.Header.Get("X-Robots-Tag"))
			}
		}
	}
	check("unlisted, person cert pending")
	servertest.MarkReady(t, siteDir, oh)
	pending = false
	check("unlisted")

	// The site's own robots.txt is served as supplied.
	if r := a.At(t, "GET", "blog."+oh+"."+servertest.PcSiteDomain, "/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Allow: /own") || r.Header.Get("X-Robots-Tag") != "" {
		t.Fatalf("own robots.txt: %d %q %q", r.Status, r.Body, r.Header.Get("X-Robots-Tag"))
	}

	// Public: no header, no robots.txt of ours, at once.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "public"}, okey); r.Status != 200 {
		t.Fatalf("public: %d %s", r.Status, r.Body)
	}
	check("public")
	// And back.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "unlisted"}, okey); r.Status != 200 {
		t.Fatalf("unlisted: %d %s", r.Status, r.Body)
	}
	check("unlisted again")

	// Profile listing never affects indexing on claimed or own addresses either.
	shopID := a.SiteID(t, olive, "shop")
	setVis := func(vis string) {
		t.Helper()
		if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": vis}, okey); r.Status != 200 {
			t.Fatalf("%s: %d %s", vis, r.Status, r.Body)
		}
	}
	exempt := func(when, ownHost string) {
		t.Helper()
		for _, x := range []struct{ host, path string }{{ownHost, "/"}, {siteHost, "/"}, {person, "/shop/"}, {servertest.PcContentHost, "/internal/site-redirect/" + oh + "/shop/"}} {
			if r := a.At(t, "GET", x.host, x.path, nil, nil); r.Status >= 400 || r.Header.Get("X-Robots-Tag") != "" {
				t.Errorf("%s: %s%s: %d X-Robots-Tag %q", when, x.host, x.path, r.Status, r.Header.Get("X-Robots-Tag"))
			}
		}
		if rb := a.At(t, "GET", ownHost, "/robots.txt", nil, nil); string(rb.Body) == "User-agent: *\nDisallow: /\n" || rb.Header.Get("X-Robots-Tag") != "" {
			t.Errorf("%s: %s/robots.txt: %d %q", when, ownHost, rb.Status, rb.Body)
		}
	}
	// Each own address, with the site unlisted and then public.
	toggle := func(kind, ownHost string) {
		t.Helper()
		exempt(kind+", unlisted", ownHost)
		setVis("public")
		exempt(kind+", public", ownHost)
		setVis("unlisted")
		exempt(kind+", unlisted again", ownHost)
	}

	// A claimed free name, claimed and released through the API.
	claimed := "olv" + strings.ToLower(strings.TrimPrefix(oh, "olive-")) + "." + servertest.PcSiteDomain
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	toggle("claimed name", claimed)
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/shop/domain", nil, okey); r.Status/100 != 2 {
		t.Fatalf("release: %d %s", r.Status, r.Body)
	}
	check("name released")

	// Connecting or removing a custom domain preserves redirects and indexing.
	if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET custom_domain = 'shop.example.org', domain_status = 'pending' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.Sites.Hosts.SyncDomainRedirect(ctx, shopID)
	if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET domain_status = 'active', domain_verified_at = now() WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.Sites.Hosts.SyncDomainRedirect(ctx, shopID)
	for _, vis := range []string{"unlisted", "public", "unlisted"} {
		setVis(vis)
		// The shared hosts redirect to the domain, without the header.
		for _, x := range []struct{ host, path string }{{siteHost, "/"}, {person, "/shop/"}, {servertest.PcContentHost, "/internal/domain-redirect/" + oh + "/shop/"}} {
			if r := a.At(t, "GET", x.host, x.path, nil, nil); r.Status != http.StatusFound || r.Header.Get("X-Robots-Tag") != "" {
				t.Errorf("custom domain, %s: %s%s: %d %q", vis, x.host, x.path, r.Status, r.Header.Get("X-Robots-Tag"))
			}
		}
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/shop/domain", nil, okey); r.Status/100 != 2 {
		t.Fatalf("remove domain: %d %s", r.Status, r.Body)
	}
	check("domain removed")

	// Family addresses follow the same indexing rule.
	a.Deploy(t, olive, "meera")
	a.LiveFamily(t, famDir, dns, olive, "quotes.fam.test", "mee", true, 1)
	for _, vis := range []string{"public", "unlisted"} {
		if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/meera/visibility", map[string]string{"visibility": vis}, okey); r.Status != 200 {
			t.Fatalf("meera %s: %d", vis, r.Status)
		}
		for _, path := range []string{"/internal/passcode/", "/robots.txt", "/missing.html"} {
			r := a.At(t, "GET", "ra.quotes.fam.test", path, nil, nil)
			if r.Header.Get("X-Robots-Tag") != "" || string(r.Body) == "User-agent: *\nDisallow: /\n" {
				t.Errorf("family %s %s: %d %q", vis, path, r.Status, r.Header.Get("X-Robots-Tag"))
			}
		}
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/me/address-families/quotes.fam.test", nil, okey); r.Status/100 != 2 {
		t.Fatalf("remove family: %d %s", r.Status, r.Body)
	}

}
