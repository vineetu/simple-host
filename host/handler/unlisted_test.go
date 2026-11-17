package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Profile listing never changes search indexing, on any address. Needs DB_DSN.
func TestProfileListingIndexableEveryAddress(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": map[string]string{
		"index.html": "<h1>blog</h1>",
		"robots.txt": "User-agent: *\nAllow: /own\n",
	}}, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusCreated {
		t.Fatalf("deploy blog: %d %s", r.status, r.body)
	}
	uid, oh := a.userID(t, olive)
	okey := map[string]string{"X-API-Key": olive.key}
	ctx := context.Background()
	siteHost := "shop." + oh + "." + pcSiteDomain
	person := oh + "." + pcSiteDomain

	// Even a leftover marker from an older binary has no serving effect.
	if err := os.WriteFile(filepath.Join(a.sites.disk.SiteDir(uid, "shop"), "unlisted"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Each address form, as the visitor (or nginx's rewrite) reaches it.
	type addr struct{ name, host, path, robots string }
	addrs := func() []addr {
		return []addr{
			{"person path (certificate pending)", person, "/shop/", ""},
			{"legacy content host", pcContentHost, "/internal/site-redirect/" + oh + "/shop/", ""},
			{"site host", siteHost, "/", "/robots.txt"},
		}
	}
	pending := true
	check := func(when string) {
		t.Helper()
		// A person path serves the file until the site host is ready, then
		// redirects to that file on its canonical address.
		personRobots := a.at(t, "GET", person, "/shop/robots.txt", nil, nil)
		if (personRobots.status != 404 && personRobots.status != 302) || personRobots.header.Get("X-Robots-Tag") != "" {
			t.Errorf("%s: person robots: %d %q", when, personRobots.status, personRobots.header.Get("X-Robots-Tag"))
		}
		for _, x := range addrs() {
			if pending && x.robots != "" {
				x.path, x.robots = "/", "" // a 302 to the person path until the certificate is ready
			}
			r := a.at(t, "GET", x.host, x.path, nil, nil)
			if r.header.Get("X-Robots-Tag") != "" || r.status >= 400 {
				t.Errorf("%s: %s %s%s: %d X-Robots-Tag %q", when, x.name, x.host, x.path, r.status, r.header.Get("X-Robots-Tag"))
			}
			if x.robots == "" {
				continue
			}
			rb := a.at(t, "GET", x.host, x.robots, nil, nil)
			if rb.status != 404 || rb.header.Get("X-Robots-Tag") != "" {
				t.Errorf("%s: %s robots.txt on a public site: %d %q", when, x.name, rb.status, rb.header.Get("X-Robots-Tag"))
			}
		}
	}
	check("unlisted, person cert pending")
	markReady(t, siteDir, oh)
	pending = false
	check("unlisted")

	// The site's own robots.txt is served as supplied.
	if r := a.at(t, "GET", "blog."+oh+"."+pcSiteDomain, "/robots.txt", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Allow: /own") || r.header.Get("X-Robots-Tag") != "" {
		t.Fatalf("own robots.txt: %d %q %q", r.status, r.body, r.header.Get("X-Robots-Tag"))
	}

	// Public: no header, no robots.txt of ours, at once.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "public"}, okey); r.status != 200 {
		t.Fatalf("public: %d %s", r.status, r.body)
	}
	check("public")
	// And back.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "unlisted"}, okey); r.status != 200 {
		t.Fatalf("unlisted: %d %s", r.status, r.body)
	}
	check("unlisted again")

	// Profile listing never affects indexing on claimed or own addresses either.
	shopID := a.siteID(t, olive, "shop")
	setVis := func(vis string) {
		t.Helper()
		if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": vis}, okey); r.status != 200 {
			t.Fatalf("%s: %d %s", vis, r.status, r.body)
		}
	}
	exempt := func(when, ownHost string) {
		t.Helper()
		for _, x := range []struct{ host, path string }{{ownHost, "/"}, {siteHost, "/"}, {person, "/shop/"}, {pcContentHost, "/internal/site-redirect/" + oh + "/shop/"}} {
			if r := a.at(t, "GET", x.host, x.path, nil, nil); r.status >= 400 || r.header.Get("X-Robots-Tag") != "" {
				t.Errorf("%s: %s%s: %d X-Robots-Tag %q", when, x.host, x.path, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
		if rb := a.at(t, "GET", ownHost, "/robots.txt", nil, nil); string(rb.body) == "User-agent: *\nDisallow: /\n" || rb.header.Get("X-Robots-Tag") != "" {
			t.Errorf("%s: %s/robots.txt: %d %q", when, ownHost, rb.status, rb.body)
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
	claimed := "olv" + strings.ToLower(strings.TrimPrefix(oh, "olive-")) + "." + pcSiteDomain
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	toggle("claimed name", claimed)
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain", nil, okey); r.status/100 != 2 {
		t.Fatalf("release: %d %s", r.status, r.body)
	}
	check("name released")

	// Connecting or removing a custom domain preserves redirects and indexing.
	if _, err := a.database.ExecContext(ctx, `UPDATE sites SET custom_domain = 'shop.example.org', domain_status = 'pending' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.syncDomainRedirect(ctx, shopID)
	if _, err := a.database.ExecContext(ctx, `UPDATE sites SET domain_status = 'active', domain_verified_at = now() WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.syncDomainRedirect(ctx, shopID)
	for _, vis := range []string{"unlisted", "public", "unlisted"} {
		setVis(vis)
		// The shared hosts redirect to the domain, without the header.
		for _, x := range []struct{ host, path string }{{siteHost, "/"}, {person, "/shop/"}, {pcContentHost, "/internal/domain-redirect/" + oh + "/shop/"}} {
			if r := a.at(t, "GET", x.host, x.path, nil, nil); r.status != http.StatusFound || r.header.Get("X-Robots-Tag") != "" {
				t.Errorf("custom domain, %s: %s%s: %d %q", vis, x.host, x.path, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain", nil, okey); r.status/100 != 2 {
		t.Fatalf("remove domain: %d %s", r.status, r.body)
	}
	check("domain removed")

	// Family addresses follow the same indexing rule.
	a.deploy(t, olive, "meera")
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "mee", true, 1)
	for _, vis := range []string{"public", "unlisted"} {
		if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/meera/visibility", map[string]string{"visibility": vis}, okey); r.status != 200 {
			t.Fatalf("meera %s: %d", vis, r.status)
		}
		for _, path := range []string{"/internal/passcode/", "/robots.txt", "/missing.html"} {
			r := a.at(t, "GET", "ra.quotes.fam.test", path, nil, nil)
			if r.header.Get("X-Robots-Tag") != "" || string(r.body) == "User-agent: *\nDisallow: /\n" {
				t.Errorf("family %s %s: %d %q", vis, path, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me/address-families/quotes.fam.test", nil, okey); r.status/100 != 2 {
		t.Fatalf("remove family: %d %s", r.status, r.body)
	}

}
