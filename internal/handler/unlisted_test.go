package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unlisted sites with no own address carry X-Robots-Tag noindex and a
// Disallow robots.txt on every address; public ones, and unlisted ones with
// a claimed name, custom domain or family address, do not; each switch
// takes effect on the next request (unlisted.go). Needs DB_DSN.
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

	// An own address exempts an unlisted site (INTENT 2026-10-14): a
	// claimed name, a custom domain, an address family. While it has one,
	// no address of the site carries the header or our robots.txt, unlisted
	// or not; dropping it brings both back at once.
	shopID := a.siteID(t, olive, "shop")
	setVis := func(vis string) {
		t.Helper()
		if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": vis}, okey); r.status != 200 {
			t.Fatalf("%s: %d %s", vis, r.status, r.body)
		}
	}
	exempt := func(when, ownHost string) {
		t.Helper()
		if a.sites.disk.IsUnlisted(uid, "shop") {
			t.Errorf("%s: marker present on a site with its own address", when)
		}
		for _, x := range []struct{ host, path string }{{ownHost, "/"}, {siteHost, "/"}, {person, "/shop/"}, {pcContentHost, "/internal/site-redirect/" + oh + "/shop/"}} {
			if r := a.at(t, "GET", x.host, x.path, nil, nil); r.status >= 400 || r.header.Get("X-Robots-Tag") != "" {
				t.Errorf("%s: %s%s: %d X-Robots-Tag %q", when, x.host, x.path, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
		if rb := a.at(t, "GET", ownHost, "/robots.txt", nil, nil); string(rb.body) == unlistedRobotsTxt || rb.header.Get("X-Robots-Tag") != "" {
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
	check("name released", true)

	// A custom domain: not while it is pending, from its proof on (the
	// domain checker's syncDomainRedirect), and not after it is removed.
	// nginx serves its files from disk with no unlisted check of its own.
	if _, err := a.database.ExecContext(ctx, `UPDATE sites SET custom_domain = 'shop.example.org', domain_status = 'pending' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.syncDomainRedirect(ctx, shopID)
	if !a.sites.disk.IsUnlisted(uid, "shop") {
		t.Error("pending domain: marker gone before the domain is proven")
	}
	if _, err := a.database.ExecContext(ctx, `UPDATE sites SET domain_status = 'active', domain_verified_at = now() WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.syncDomainRedirect(ctx, shopID)
	if a.sites.disk.IsUnlisted(uid, "shop") {
		t.Error("proven domain: marker still present")
	}
	for _, vis := range []string{"unlisted", "public", "unlisted"} {
		setVis(vis)
		if a.sites.disk.IsUnlisted(uid, "shop") {
			t.Errorf("custom domain, %s: marker present", vis)
		}
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
	check("domain removed", true)

	// An address family: every site of the account it covers is exempt
	// from the moment the family is live; a site it does not cover is not.
	a.deploy(t, olive, "meera")
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "mee", true, 1)
	if a.sites.disk.IsUnlisted(uid, "meera") {
		t.Error("family address: meera still marked")
	}
	if !a.sites.disk.IsUnlisted(uid, "shop") {
		t.Error("family that does not cover shop: marker gone")
	}
	for _, vis := range []string{"public", "unlisted"} {
		if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/meera/visibility", map[string]string{"visibility": vis}, okey); r.status != 200 {
			t.Fatalf("meera %s: %d", vis, r.status)
		}
		for _, path := range []string{"/internal/passcode/", "/robots.txt", "/missing.html"} {
			r := a.at(t, "GET", "ra.quotes.fam.test", path, nil, nil)
			if r.header.Get("X-Robots-Tag") != "" || string(r.body) == unlistedRobotsTxt {
				t.Errorf("family %s %s: %d %q", vis, path, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me/address-families/quotes.fam.test", nil, okey); r.status/100 != 2 {
		t.Fatalf("remove family: %d %s", r.status, r.body)
	}
	if !a.sites.disk.IsUnlisted(uid, "meera") {
		t.Error("family removed: meera not marked again")
	}

	// The boot sync puts a lost marker back, and takes a stale one away,
	// including one on a site with its own address.
	a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/visibility", map[string]string{"visibility": "unlisted"}, okey)
	if _, err := a.database.ExecContext(ctx, `UPDATE sites SET custom_domain = 'meera.example.org', domain_status = 'active', domain_verified_at = now() WHERE user_id = $1 AND name = 'meera'`, uid); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.sites.disk.SiteDir(uid, "meera"), "unlisted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
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
	if !a.sites.disk.IsUnlisted(uid, "shop") || a.sites.disk.IsUnlisted(uid, "blog") || a.sites.disk.IsUnlisted(uid, "meera") {
		t.Fatalf("boot sync: shop=%v blog=%v meera=%v", a.sites.disk.IsUnlisted(uid, "shop"), a.sites.disk.IsUnlisted(uid, "blog"), a.sites.disk.IsUnlisted(uid, "meera"))
	}
}
