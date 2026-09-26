package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

func TestSiteHostLabels(t *testing.T) {
	for host, want := range map[string]string{
		"shop.olive.example.app":   "shop/olive",
		"SHOP.Olive.example.app.":  "shop/olive",
		"olive.example.app":        "",
		"a.b.c.example.app":        "",
		"example.app":              "",
		".olive.example.app":       "",
		"shop..example.app":        "",
		"shop.olive.other.app":     "",
		"shop.olive.example.app.x": "",
	} {
		s, h, ok := siteHostLabels(host, "example.app")
		got := ""
		if ok {
			got = s + "/" + h
		}
		if got != want {
			t.Errorf("%q: got %q want %q", host, got, want)
		}
	}
}

// newSiteApp is the router with person hosts canonical and site hosts in
// mode, with a certificate hand-off directory in which only the handles in
// ready have a certificate.
func newSiteApp(t *testing.T, mode string) (*privateApp, string) {
	t.Helper()
	a := newPersonApp(t, "canonical")
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sites.SetSiteHosts(mode, dir)
	return a, dir
}

func markReady(t *testing.T, dir, handle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ready", handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSiteHostsOffLeavesPersonHosts(t *testing.T) {
	a, dir := newSiteApp(t, "off")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	if r := a.at(t, "GET", "shop."+oh+"."+pcSiteDomain, "/", nil, nil); r.status == 200 && string(r.body) == "<h1>shop</h1>" {
		t.Fatalf("off: site host served the site")
	}
	if r := a.at(t, "GET", oh+"."+pcSiteDomain, "/shop/", nil, nil); r.status != 200 {
		t.Fatalf("off: person host: %d", r.status)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", oh)); err == nil {
		t.Fatalf("off: certificate requested")
	}
}

func TestSiteHostsCanonical(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, oscar, "shop")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	_, oh := a.userID(t, olive)
	_, sh := a.userID(t, oscar)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	person := oh + "." + pcSiteDomain
	shop := "shop." + person
	blog := "blog." + person
	oscarPerson := sh + "." + pcSiteDomain
	oscarShop := "shop." + oscarPerson
	okey := map[string]string{"X-API-Key": olive.key}
	ctx := context.Background()

	// ---- certificate requests: only people without one ----------------------
	if _, err := os.Stat(filepath.Join(dir, "requests", sh)); err != nil {
		t.Fatalf("no request for %s: %v", sh, err)
	}
	// The issuer removes a request once served; a ready person is never asked for again.
	if err := os.Remove(filepath.Join(dir, "requests", oh)); err != nil {
		t.Fatal(err)
	}
	a.sites.requestSiteCertsForAll(ctx) // idempotent back-fill
	if _, err := os.Stat(filepath.Join(dir, "requests", oh)); err == nil {
		t.Fatalf("request for a person who is ready")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "requests"))
	for _, e := range entries {
		if e.Name() == oh || strings.ContainsAny(e.Name(), "/.") {
			t.Fatalf("bad request file %q", e.Name())
		}
	}

	// ---- addresses handed out ------------------------------------------------
	r := a.at(t, "GET", apex, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.body), `"https://`+shop+`/"`) || strings.Contains(string(r.body), person+"/shop/") {
		t.Fatalf("site_url: %s", r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, map[string]string{"X-API-Key": oscar.key}); !strings.Contains(string(r.body), "https://"+oscarPerson+"/shop/") {
		t.Fatalf("not-ready site_url: %s", r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/me", nil, okey); r.json(t)["public_page"] != "https://"+person+"/" {
		t.Fatalf("public_page: %s", r.body)
	}

	// ---- serving at the root ---------------------------------------------------
	if r := a.at(t, "GET", shop, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" || r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("site host: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, "/sub", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/sub/" {
		t.Fatalf("dir without slash: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", shop, "/sub/", nil, nil); r.status != 200 || string(r.body) != "sub" {
		t.Fatalf("dir: %d %s", r.status, r.body)
	}
	for _, p := range []string{"/nope.html", "/../../etc/passwd", "/internal/site-redirect/" + oh + "/blog/", "/.env"} {
		if r := a.at(t, "GET", shop, p, nil, nil); r.status != 404 {
			t.Errorf("GET %s: %d", p, r.status)
		}
	}
	if r := a.at(t, "POST", shop, "/", nil, nil); r.status != http.StatusMethodNotAllowed {
		t.Errorf("POST page: %d", r.status)
	}
	if r := a.at(t, "GET", "nope."+person, "/", nil, nil); r.status != 404 {
		t.Errorf("unknown site: %d", r.status)
	}
	if r := a.at(t, "GET", "shop.nobody-here."+pcSiteDomain, "/", nil, nil); r.status != 404 {
		t.Errorf("unknown person: %d", r.status)
	}
	// Root-absolute links written for older addresses.
	if r := a.at(t, "GET", shop, "/"+oh+"/shop/sub/?x=1", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/sub/?x=1" {
		t.Fatalf("content-host prefix: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", shop, "/shop/sub/", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/sub/" {
		t.Fatalf("person-host prefix: %d %q", r.status, r.header.Get("Location"))
	}
	for _, p := range []string{"//evil.example/x", "/shop//evil.example/x", "/" + oh + "/shop//evil.example"} {
		if loc := a.at(t, "GET", shop, p, nil, nil).header.Get("Location"); strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
			t.Fatalf("protocol-relative redirect for %s: %q", p, loc)
		}
	}

	// ---- old addresses redirect, path and query kept -----------------------------
	if r := a.at(t, "GET", person, "/shop/sub/?q=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+shop+"/sub/?q=1" {
		t.Fatalf("person-path redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", person, "/shop", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/shop/" {
		t.Fatalf("person-path no slash: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+shop+"/sub/?q=1" {
		t.Fatalf("legacy redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh, nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+person+"/" {
		t.Fatalf("legacy person page: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", person, "/", nil, nil); r.status != 200 {
		t.Fatalf("person page: %d", r.status)
	}

	// ---- a person without a certificate keeps the person-path form ----------------
	if r := a.at(t, "GET", oscarPerson, "/shop/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("fallback person path: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+sh+"/shop/x?y=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+oscarPerson+"/shop/x?y=1" {
		t.Fatalf("fallback legacy redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", oscarShop, "/x?y=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+oscarPerson+"/shop/x?y=1" {
		t.Fatalf("not-ready site host: %d %q", r.status, r.header.Get("Location"))
	}

	// ---- the API on a site host answers for that one site ---------------------------
	if r := a.at(t, "GET", shop, "/v1/sites/shop/state", nil, map[string]string{"Origin": "https://" + shop}); r.status != 200 {
		t.Fatalf("own state: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, "/v1/u/"+oh+"/sites/shop/state", nil, map[string]string{"Origin": "https://" + shop}); r.status != 200 {
		t.Fatalf("own state by handle: %d %s", r.status, r.body)
	}
	for _, p := range []string{"/v1/sites/blog/state", "/v1/u/" + oh + "/sites/blog/state", "/v1/u/" + sh + "/sites/shop/state"} {
		if r := a.at(t, "GET", shop, p, nil, map[string]string{"Origin": "https://" + shop}); r.status == 200 {
			t.Fatalf("GET %s on shop's host: %d %s", p, r.status, r.body)
		}
	}
	// A sibling site's page is not an allowed origin.
	if r := a.at(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/state", nil, map[string]string{"Origin": "https://" + blog}); r.status != http.StatusForbidden {
		t.Fatalf("sibling origin: %d", r.status)
	}
	if r := a.at(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/state", nil, map[string]string{"Origin": "https://" + shop}); r.status != 200 {
		t.Fatalf("own origin to apex: %d %s", r.status, r.body)
	}

	// ---- sign-in is per site ------------------------------------------------------
	patch := map[string]any{"ops": []map[string]any{{"op": "set", "path": "n", "value": 1}}}
	if r := a.at(t, "PATCH", shop, "/v1/sites/shop/state", patch, browser(shop, "")); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous save: %d %s", r.status, r.body)
	}
	vic := a.newPerson(t, "vic")
	shopCookie := a.session(t, vic, a.siteID(t, olive, "shop"), shop)
	blogCookie := a.session(t, vic, a.siteID(t, olive, "blog"), blog)
	personCookie := a.session(t, vic, a.siteID(t, olive, "blog"), person)
	if r := a.at(t, "PATCH", shop, "/v1/sites/shop/state", patch, browser(shop, shopCookie)); r.status != 200 {
		t.Fatalf("signed-in save: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, "/v1/sites/shop/me", nil, browser(shop, shopCookie)); r.json(t)["signed_in"] != true {
		t.Fatalf("me: %s", r.body)
	}
	// Another site's sign-in, on its host or presented here, does not count.
	if r := a.at(t, "PATCH", shop, "/v1/sites/shop/state", patch, browser(shop, blogCookie)); r.status == 200 {
		t.Fatalf("blog session on shop host: %d", r.status)
	}
	// A person-host sign-in no longer covers a site on its own host.
	if r := a.at(t, "PATCH", person, "/v1/sites/shop/state", patch, browser(person, personCookie)); r.status == 200 {
		t.Fatalf("person-host session for a site-host site: %d", r.status)
	}
	// Cross-origin, or the plain cookie a sibling can plant: nothing.
	if r := a.at(t, "PATCH", shop, "/v1/sites/shop/state", patch, browser(shop, shopCookie, "Origin", "https://"+blog, "Sec-Fetch-Site", "same-site")); r.status == 200 {
		t.Fatalf("sibling-origin save: %d", r.status)
	}
	plain := browser(shop, "")
	plain["Cookie"] = visitorCookieHTTP + "=" + shopCookie
	if r := a.at(t, "PATCH", shop, "/v1/sites/shop/state", patch, plain); r.status == 200 {
		t.Fatalf("plain cookie save: %d", r.status)
	}
	// Email sign-in only from a page on this very host.
	if r := a.at(t, "POST", shop, "/v1/sites/shop/visitor/auth", map[string]string{"email": "x@example.com"}, browser(shop, "", "Origin", "https://"+blog, "Sec-Fetch-Site", "same-site")); r.status != http.StatusForbidden {
		t.Fatalf("cross-site email sign-in: %d %s", r.status, r.body)
	}
	// The key still saves.
	if r := a.at(t, "PATCH", shop, "/v1/sites/shop/state", patch, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + shop}); r.status != 200 {
		t.Fatalf("key save: %d %s", r.status, r.body)
	}

	// ---- private lists live on the site host -----------------------------------------
	if r := a.at(t, "PUT", apex, "/v1/sites/shop/collections/orders/privacy", map[string]bool{"private": true}, okey); r.status != 200 || r.json(t)["domain"] != shop {
		t.Fatalf("private: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", shop, "/v1/sites/shop/collections/orders", map[string]any{"x": 1}, browser(shop, shopCookie)); r.status != http.StatusCreated {
		t.Fatalf("private submit: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", person, "/v1/sites/shop/collections/orders", map[string]any{"x": 1}, browser(person, personCookie)); r.status == http.StatusCreated {
		t.Fatalf("private submit on person host: %d", r.status)
	}
	if r := a.at(t, "GET", shop, "/v1/sites/shop/collections/orders", nil, browser(shop, shopCookie)); r.status != 404 {
		t.Fatalf("visitor read of private list: %d", r.status)
	}
	ownerCookie := a.session(t, olive, a.siteID(t, olive, "shop"), shop)
	if r := a.at(t, "GET", shop, "/v1/sites/shop/collections/orders", nil, browser(shop, ownerCookie)); r.status != 200 || len(itemsOf(t, r)) != 1 {
		t.Fatalf("owner read: %d %s", r.status, r.body)
	}
	ownerOnBlog := a.session(t, olive, a.siteID(t, olive, "blog"), blog)
	if r := a.at(t, "GET", blog, "/v1/sites/shop/collections/orders", nil, browser(blog, ownerOnBlog)); r.status == 200 {
		t.Fatalf("owner read from a sibling site: %d", r.status)
	}

	// ---- sign-in return_to --------------------------------------------------------------
	oa := &OAuthHandler{database: a.database, personSite: a.sites.PersonReturnSite}
	oa.cfg.PublicBaseURL = "https://simple-host.test"
	oa.cfg.SiteDomain = pcSiteDomain
	oa.cfg.ContentHost = pcContentHost
	if _, siteID, h, purpose, err := oa.sanitizeReturnTo(ctx, "https://"+shop+"/page.html"); err != nil || h != shop || purpose != "site" || siteID.String != a.siteID(t, olive, "shop") {
		t.Fatalf("return_to on site host: %v %q %q %v", err, h, purpose, siteID)
	}
	for _, bad := range []string{"https://nope." + person + "/", "https://" + oscarShop + "/", "https://" + person + "/shop/", "https://x." + shop + "/"} {
		if _, _, _, _, err := oa.sanitizeReturnTo(ctx, bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}
	// A not-ready person still signs in on the person host.
	if _, _, h, _, err := oa.sanitizeReturnTo(ctx, "https://"+oscarPerson+"/shop/"); err != nil || h != oscarPerson {
		t.Fatalf("fallback return_to: %v %q", err, h)
	}

	// ---- a site with its own domain lives there -------------------------------------------
	dom := "olive-blog." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/blog/domain", map[string]string{"domain": dom}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", blog, "/a/b?q=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+dom+"/a/b?q=1" {
		t.Fatalf("domain redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "PATCH", blog, "/v1/sites/blog/state", patch, browser(blog, blogCookie)); r.status != http.StatusUnauthorized || r.json(t)["code"] != "use_custom_domain" {
		t.Fatalf("save for domain site on its site host: %d %s", r.status, r.body)
	}
	if _, _, _, _, err := oa.sanitizeReturnTo(ctx, "https://"+blog+"/"); err == nil {
		t.Fatalf("return_to on a domain site's site host accepted")
	}

	// ---- serve mode: answers, but hands out the person-path form ------------------------------
	a.sites.SetSiteHosts("serve", dir)
	if r := a.at(t, "GET", shop, "/", nil, nil); r.status != 200 {
		t.Fatalf("serve: site host: %d", r.status)
	}
	if r := a.at(t, "GET", person, "/shop/", nil, nil); r.status != 200 {
		t.Fatalf("serve: person path: %d", r.status)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.body), "https://"+person+"/shop/") {
		t.Fatalf("serve: site_url: %s", r.body)
	}
	a.sites.SetSiteHosts("canonical", dir)

	// ---- an old handle kept as an alias follows the account -----------------------------------
	uid, _ := a.userID(t, olive)
	if _, err := db.RenameHandle(ctx, a.database, uid, oh+"-new"); err != nil {
		t.Fatal(err)
	}
	// The new handle has no certificate yet: the working person-path address.
	if r := a.at(t, "GET", shop, "/x?y=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+oh+"-new."+pcSiteDomain+"/shop/x?y=1" {
		t.Fatalf("alias redirect: %d %q", r.status, r.header.Get("Location"))
	}
	markReady(t, dir, oh+"-new")
	if r := a.at(t, "GET", shop, "/x?y=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://shop."+oh+"-new."+pcSiteDomain+"/x?y=1" {
		t.Fatalf("alias redirect when ready: %d %q", r.status, r.header.Get("Location"))
	}
}
