package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unlisted sites carry X-Robots-Tag noindex and a Disallow robots.txt on
// every address; public ones do not; the switch takes effect on the next
// request (unlisted.go). Needs DB_DSN.
func TestUnlistedNoindexEveryAddress(t *testing.T) {
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

	// A new site starts unlisted, and its folder says so from the start.
	if !a.sites.disk.IsUnlisted(uid, "shop") {
		t.Fatal("new site has no unlisted marker")
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
	check := func(when string, want bool) {
		t.Helper()
		for _, x := range addrs() {
			if pending && x.robots != "" {
				x.path, x.robots = "/", "" // a 302 to the person path until the certificate is ready
			}
			r := a.at(t, "GET", x.host, x.path, nil, nil)
			got := r.header.Get("X-Robots-Tag") == "noindex, nofollow"
			if got != want || r.status >= 400 {
				t.Errorf("%s: %s %s%s: %d X-Robots-Tag %q, want noindex=%v", when, x.name, x.host, x.path, r.status, r.header.Get("X-Robots-Tag"), want)
			}
			if x.robots == "" {
				continue
			}
			rb := a.at(t, "GET", x.host, x.robots, nil, nil)
			if want && (rb.status != 200 || string(rb.body) != unlistedRobotsTxt || rb.header.Get("X-Robots-Tag") == "") {
				t.Errorf("%s: %s robots.txt: %d %q", when, x.name, rb.status, rb.body)
			}
			if !want && (rb.status != 404 || rb.header.Get("X-Robots-Tag") != "") {
				t.Errorf("%s: %s robots.txt on a public site: %d %q", when, x.name, rb.status, rb.header.Get("X-Robots-Tag"))
			}
		}
	}
	check("unlisted, person cert pending", true)
	markReady(t, siteDir, oh)
	pending = false
	check("unlisted", true)

	// The site's own robots.txt wins; the header still applies.
	if r := a.at(t, "GET", "blog."+oh+"."+pcSiteDomain, "/robots.txt", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Allow: /own") || r.header.Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("own robots.txt: %d %q %q", r.status, r.body, r.header.Get("X-Robots-Tag"))
	}

	// Public: no header, no robots.txt of ours, at once.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "public"}, okey); r.status != 200 {
		t.Fatalf("public: %d %s", r.status, r.body)
	}
	check("public", false)
	// And back.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "unlisted"}, okey); r.status != 200 {
		t.Fatalf("unlisted: %d %s", r.status, r.body)
	}
	check("unlisted again", true)

	// A claimed name (Go serves it) and a custom domain (nginx hands the
	// unlisted site's requests to the app through the passcode path).
	for _, domain := range []string{"shop-x." + pcSiteDomain, "shop.example.org"} {
		if _, err := a.database.ExecContext(ctx, `UPDATE sites SET custom_domain = $2, domain_status = 'active', domain_verified_at = now() WHERE user_id = $1 AND name = 'shop'`, uid, domain); err != nil {
			t.Fatal(err)
		}
		prefix := ""
		if !strings.HasSuffix(domain, pcSiteDomain) {
			prefix = "/internal/passcode"
		}
		for _, vis := range []string{"unlisted", "public"} {
			if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": vis}, okey); r.status != 200 {
				t.Fatalf("%s: %d", vis, r.status)
			}
			want := vis == "unlisted"
			r := a.at(t, "GET", domain, prefix+"/", nil, nil)
			if r.status != 200 || string(r.body) != "<h1>shop</h1>" || (r.header.Get("X-Robots-Tag") != "") != want {
				t.Errorf("%s %s: %d %q X-Robots-Tag %q", domain, vis, r.status, r.body, r.header.Get("X-Robots-Tag"))
			}
			rb := a.at(t, "GET", domain, prefix+"/robots.txt", nil, nil)
			if want != (rb.status == 200 && string(rb.body) == unlistedRobotsTxt) {
				t.Errorf("%s %s robots.txt: %d %q", domain, vis, rb.status, rb.body)
			}
			// The site host now redirects to the domain, header and all.
			if r := a.at(t, "GET", siteHost, "/", nil, nil); r.status != http.StatusFound || (r.header.Get("X-Robots-Tag") != "") != want {
				t.Errorf("site host redirect %s: %d %q", vis, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
	}
	if _, err := a.database.ExecContext(ctx, `UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL WHERE user_id = $1 AND name = 'shop'`, uid); err != nil {
		t.Fatal(err)
	}

	// An address family: files from disk by nginx, misses through the app.
	a.deploy(t, olive, "meera")
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 1)
	for _, path := range []string{"/internal/passcode/", "/internal/passcode/robots.txt", "/robots.txt"} {
		r := a.at(t, "GET", "meera.quotes.fam.test", path, nil, nil)
		if r.status != 200 || r.header.Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Errorf("family %s: %d %q", path, r.status, r.header.Get("X-Robots-Tag"))
		}
	}

	// The boot sync puts a lost marker back, and takes a stale one away.
	a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "unlisted"}, okey)
	if err := os.Remove(filepath.Join(a.sites.disk.SiteDir(uid, "shop"), "unlisted")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.sites.disk.SiteDir(uid, "blog"), "unlisted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.at(t, "PUT", pcSiteDomain, "/v1/sites/blog/visibility", map[string]string{"visibility": "public"}, okey)
	if err := os.WriteFile(filepath.Join(a.sites.disk.SiteDir(uid, "blog"), "unlisted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.sites.SyncSuspendMarkers(ctx)
	if !a.sites.disk.IsUnlisted(uid, "shop") || a.sites.disk.IsUnlisted(uid, "blog") {
		t.Fatalf("boot sync: shop=%v blog=%v", a.sites.disk.IsUnlisted(uid, "shop"), a.sites.disk.IsUnlisted(uid, "blog"))
	}
}
