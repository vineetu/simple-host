package sitestest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/server/servertest"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

func TestSiteHostsOffLeavesPersonHosts(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "off")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, dir, oh)
	if r := a.At(t, "GET", "shop."+oh+"."+servertest.PcSiteDomain, "/", nil, nil); r.Status == 200 && string(r.Body) == "<h1>shop</h1>" {
		t.Fatalf("off: site host served the site")
	}
	if r := a.At(t, "GET", oh+"."+servertest.PcSiteDomain, "/shop/", nil, nil); r.Status != 200 {
		t.Fatalf("off: person host: %d", r.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", oh)); err == nil {
		t.Fatalf("off: certificate requested")
	}
}

func TestSiteHostsCanonical(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "shop")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	_, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	servertest.MarkReady(t, dir, oh)
	apex := servertest.PcSiteDomain
	person := oh + "." + servertest.PcSiteDomain
	shop := "shop." + person
	blog := "blog." + person
	oscarPerson := sh + "." + servertest.PcSiteDomain
	oscarShop := "shop." + oscarPerson
	okey := map[string]string{"X-API-Key": olive.Key}
	ctx := context.Background()

	// ---- certificate requests: only people without one ----------------------
	if _, err := os.Stat(filepath.Join(dir, "requests", sh)); err != nil {
		t.Fatalf("no request for %s: %v", sh, err)
	}
	// The issuer removes a request once served; a ready person is never asked for again.
	if err := os.Remove(filepath.Join(dir, "requests", oh)); err != nil {
		t.Fatal(err)
	}
	a.Sites.RequestSiteCertsForAll(ctx) // idempotent back-fill
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
	r := a.At(t, "GET", apex, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.Body), `"https://`+shop+`/"`) || strings.Contains(string(r.Body), person+"/shop/") {
		t.Fatalf("site_url: %s", r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, map[string]string{"X-API-Key": oscar.Key}); !strings.Contains(string(r.Body), "https://"+oscarPerson+"/shop/") {
		t.Fatalf("not-ready site_url: %s", r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me", nil, okey); r.JSON(t)["public_page"] != "https://"+person+"/" {
		t.Fatalf("public_page: %s", r.Body)
	}

	// ---- serving at the root ---------------------------------------------------
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("site host: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/sub", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/sub/" || r.Header.Get("Cache-Control") != publicPageCache {
		t.Fatalf("dir without slash: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", shop, "/sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("dir: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/nope.html", "/../../etc/passwd", "/internal/site-redirect/" + oh + "/blog/", "/.env"} {
		if r := a.At(t, "GET", shop, p, nil, nil); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := a.At(t, "POST", shop, "/", nil, nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST page: %d", r.Status)
	}
	if r := a.At(t, "GET", "nope."+person, "/", nil, nil); r.Status != 404 {
		t.Errorf("unknown site: %d", r.Status)
	}
	if r := a.At(t, "GET", "shop.nobody-here."+servertest.PcSiteDomain, "/", nil, nil); r.Status != 404 {
		t.Errorf("unknown person: %d", r.Status)
	}
	// Root-absolute links written for older addresses.
	if r := a.At(t, "GET", shop, "/"+oh+"/shop/sub/?x=1", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/sub/?x=1" {
		t.Fatalf("content-host prefix: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", shop, "/shop/sub/", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/sub/" {
		t.Fatalf("person-host prefix: %d %q", r.Status, r.Header.Get("Location"))
	}
	for _, p := range []string{"//evil.example/x", "/shop//evil.example/x", "/" + oh + "/shop//evil.example"} {
		if loc := a.At(t, "GET", shop, p, nil, nil).Header.Get("Location"); strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, "/\\") {
			t.Fatalf("protocol-relative redirect for %s: %q", p, loc)
		}
	}

	// ---- old addresses redirect, path and query kept -----------------------------
	if r := a.At(t, "GET", person, "/shop/sub/?q=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+shop+"/sub/?q=1" {
		t.Fatalf("person-path redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", person, "/shop", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/shop/" {
		t.Fatalf("person-path no slash: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+shop+"/sub/?q=1" {
		t.Fatalf("legacy redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh, nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+person+"/" {
		t.Fatalf("legacy person page: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", person, "/", nil, nil); r.Status != 200 {
		t.Fatalf("person page: %d", r.Status)
	}

	// ---- a person without a certificate keeps the person-path form ----------------
	if r := a.At(t, "GET", oscarPerson, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("fallback person path: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+sh+"/shop/x?y=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+oscarPerson+"/shop/x?y=1" {
		t.Fatalf("fallback legacy redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", oscarShop, "/x?y=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+oscarPerson+"/shop/x?y=1" {
		t.Fatalf("not-ready site host: %d %q", r.Status, r.Header.Get("Location"))
	}

	// ---- the API on a site host answers for that one site ---------------------------
	if r := a.At(t, "GET", shop, "/v1/sites/shop/me", nil, map[string]string{"Origin": "https://" + shop}); r.Status != 200 {
		t.Fatalf("own site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + shop}); r.Status != 200 {
		t.Fatalf("own site by handle: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/v1/sites/blog/me", "/v1/u/" + oh + "/sites/blog/me", "/v1/u/" + sh + "/sites/shop/me"} {
		if r := a.At(t, "GET", shop, p, nil, map[string]string{"Origin": "https://" + shop}); r.Status == 200 {
			t.Fatalf("GET %s on shop's host: %d %s", p, r.Status, r.Body)
		}
	}
	// A sibling site's page is not an allowed origin.
	if r := a.At(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + blog}); r.Status != http.StatusForbidden {
		t.Fatalf("sibling origin: %d", r.Status)
	}
	if r := a.At(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + shop}); r.Status != 200 {
		t.Fatalf("own origin to apex: %d %s", r.Status, r.Body)
	}

	// ---- sign-in is per site ------------------------------------------------------
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/resources/notes", servertest.StorageRules("kv", servertest.RulesBoard), okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	save := "/v1/sites/shop/storage/kv/notes/keys/n"
	value := map[string]any{"value": 1}
	if r := a.At(t, "PUT", shop, save, value, servertest.Browser(shop, "")); r.Status/100 == 2 {
		t.Fatalf("anonymous save: %d %s", r.Status, r.Body)
	}
	vic := a.NewPerson(t, "vic")
	shopCookie := a.Session(t, vic, a.SiteID(t, olive, "shop"), shop)
	blogCookie := a.Session(t, vic, a.SiteID(t, olive, "blog"), blog)
	personCookie := a.Session(t, vic, a.SiteID(t, olive, "blog"), person)
	if r := a.At(t, "PUT", shop, save, value, servertest.Browser(shop, shopCookie)); r.Status != 200 {
		t.Fatalf("signed-in save: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/v1/sites/shop/me", nil, servertest.Browser(shop, shopCookie)); r.JSON(t)["signed_in"] != true {
		t.Fatalf("me: %s", r.Body)
	}
	// Another site's sign-in, on its host or presented here, does not count.
	if r := a.At(t, "PUT", shop, save, value, servertest.Browser(shop, blogCookie)); r.Status/100 == 2 {
		t.Fatalf("blog session on shop host: %d", r.Status)
	}
	// A person-host sign-in no longer covers a site on its own host.
	if r := a.At(t, "PUT", person, save, value, servertest.Browser(person, personCookie)); r.Status/100 == 2 {
		t.Fatalf("person-host session for a site-host site: %d", r.Status)
	}
	// Cross-origin, or the plain cookie a sibling can plant: nothing.
	if r := a.At(t, "PUT", shop, save, value, servertest.Browser(shop, shopCookie, "Origin", "https://"+blog, "Sec-Fetch-Site", "same-site")); r.Status/100 == 2 {
		t.Fatalf("sibling-origin save: %d", r.Status)
	}
	plain := servertest.Browser(shop, "")
	plain["Cookie"] = accesshttp.VisitorCookieHTTP + "=" + shopCookie
	if r := a.At(t, "PUT", shop, save, value, plain); r.Status/100 == 2 {
		t.Fatalf("plain cookie save: %d", r.Status)
	}
	// Email sign-in only from a page on this very host.
	if r := a.At(t, "POST", shop, "/v1/sites/shop/visitor/auth", map[string]string{"email": "x@example.com"}, servertest.Browser(shop, "", "Origin", "https://"+blog, "Sec-Fetch-Site", "same-site")); r.Status != http.StatusForbidden {
		t.Fatalf("cross-site email sign-in: %d %s", r.Status, r.Body)
	}
	// The key still saves.
	if r := a.At(t, "PUT", shop, save, value, map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + shop}); r.Status != 200 {
		t.Fatalf("key save: %d %s", r.Status, r.Body)
	}

	// ---- sign-in return_to --------------------------------------------------------------
	oa := &accesshttp.OAuthHandler{Database: a.Database, Accounts: accountapp.New(a.Database), Limits: a.Sites.Limits, PersonSite: a.Sites.Hosts.ReturnSite}
	oa.Cfg.PublicBaseURL = "https://simple-host.test"
	oa.Cfg.SiteDomain = servertest.PcSiteDomain
	oa.Cfg.ContentHost = servertest.PcContentHost
	if _, siteID, h, purpose, err := oa.SanitizeReturnTo(ctx, "https://"+shop+"/page.html"); err != nil || h != shop || purpose != "site" || siteID.String != a.SiteID(t, olive, "shop") {
		t.Fatalf("return_to on site host: %v %q %q %v", err, h, purpose, siteID)
	}
	for _, bad := range []string{"https://nope." + person + "/", "https://" + oscarShop + "/", "https://" + person + "/shop/", "https://x." + shop + "/"} {
		if _, _, _, _, err := oa.SanitizeReturnTo(ctx, bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}
	// A not-ready person still signs in on the person host.
	if _, _, h, _, err := oa.SanitizeReturnTo(ctx, "https://"+oscarPerson+"/shop/"); err != nil || h != oscarPerson {
		t.Fatalf("fallback return_to: %v %q", err, h)
	}

	// ---- a site with its own domain lives there -------------------------------------------
	dom := "olive-blog." + servertest.PcSiteDomain
	if r := a.At(t, "POST", apex, "/v1/sites/blog/domain", map[string]string{"domain": dom}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", blog, "/a/b?q=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+dom+"/a/b?q=1" {
		t.Fatalf("domain redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", blog, "/v1/sites/blog/me", nil, servertest.Browser(blog, blogCookie)); r.JSON(t)["code"] != "use_custom_domain" {
		t.Fatalf("sign-in for domain site on its site host: %d %s", r.Status, r.Body)
	}
	if _, _, _, _, err := oa.SanitizeReturnTo(ctx, "https://"+blog+"/"); err == nil {
		t.Fatalf("return_to on a domain site's site host accepted")
	}

	// ---- serve mode: answers, but hands out the person-path form ------------------------------
	a.Sites.Hosts.SetSiteHosts("serve", dir)
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != 200 {
		t.Fatalf("serve: site host: %d", r.Status)
	}
	if r := a.At(t, "GET", person, "/shop/", nil, nil); r.Status != 200 {
		t.Fatalf("serve: person path: %d", r.Status)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.Body), "https://"+person+"/shop/") {
		t.Fatalf("serve: site_url: %s", r.Body)
	}
	a.Sites.Hosts.SetSiteHosts("canonical", dir)

	// ---- an old handle kept as an alias follows the account -----------------------------------
	uid, _ := a.UserID(t, olive)
	if _, err := accountsrepo.RenameHandle(ctx, a.Database, uid, oh+"-new"); err != nil {
		t.Fatal(err)
	}
	// The new handle has no certificate yet: the working person-path address.
	if r := a.At(t, "GET", shop, "/x?y=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+oh+"-new."+servertest.PcSiteDomain+"/shop/x?y=1" {
		t.Fatalf("alias redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	servertest.MarkReady(t, dir, oh+"-new")
	if r := a.At(t, "GET", shop, "/x?y=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://shop."+oh+"-new."+servertest.PcSiteDomain+"/x?y=1" {
		t.Fatalf("alias redirect when ready: %d %q", r.Status, r.Header.Get("Location"))
	}
}
