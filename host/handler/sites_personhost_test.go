package handler

import (
	"context"
	accountapp "github.com/vsriram/simple-host/host/access"
	"net/http"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
)

func newPersonApp(t *testing.T, mode string, eds ...*edition.Edition) *privateApp {
	t.Helper()
	a := newPrivateApp(t, eds...)
	a.sites.SetPersonHosts(mode)
	accountsrepo.SetPlatformDomain(pcSiteDomain)
	t.Cleanup(func() { accountsrepo.SetPlatformDomain("") })
	return a
}

func TestPersonHostsOffKeepsPathModel(t *testing.T) {
	a := newPersonApp(t, "off")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, handle := a.userID(t, olive)
	host := handle + "." + pcSiteDomain
	// Off: the name is a retired per-name host (legacy 301), never served.
	if r := a.at(t, "GET", host, "/shop/", nil, nil); r.status != http.StatusMovedPermanently {
		t.Fatalf("off: person host answered %d", r.status)
	}
	r := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.key})
	if !strings.Contains(string(r.body), "https://"+pcContentHost+"/"+handle+"/shop/") {
		t.Fatalf("off: site_url: %s", r.body)
	}
}

func TestPersonHostsServeAndCanonical(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, oscar, "shop") // older same-named site: the legacy name lookup would pick it
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	_, oh := a.userID(t, olive)
	_, sh := a.userID(t, oscar)
	host := oh + "." + pcSiteDomain
	oscarHost := sh + "." + pcSiteDomain
	apex := "simple-host.test"
	okey := map[string]string{"X-API-Key": olive.key}
	if _, err := a.database.Exec(`UPDATE sites SET visibility = 'public' WHERE id = $1`, a.siteID(t, olive, "blog")); err != nil {
		t.Fatal(err)
	}

	// ---- serving ---------------------------------------------------------------
	if r := a.at(t, "GET", host, "/shop/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("serve site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/shop", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/shop/" {
		t.Fatalf("site without slash: %d %s", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", host, "/shop/sub", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/shop/sub/" || r.header.Get("Cache-Control") != publicPageCache {
		t.Fatalf("dir without slash: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", host, "/shop/sub/", nil, nil); r.status != 200 || string(r.body) != "sub" {
		t.Fatalf("dir: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/"+oh+"/shop/sub/?x=1", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/shop/sub/?x=1" {
		t.Fatalf("old path shim: %d %q", r.status, r.header.Get("Location"))
	}
	for _, p := range []string{"/nope/", "/shop/../../../etc/passwd", "/shop/nope.html", "/internal/tls-ask?domain=x", "/mcp", "/.env"} {
		if r := a.at(t, "GET", host, p, nil, nil); r.status != 404 {
			t.Errorf("GET %s: %d", p, r.status)
		}
	}
	if r := a.at(t, "POST", host, "/shop/", nil, nil); r.status != http.StatusMethodNotAllowed {
		t.Errorf("POST page: %d", r.status)
	}
	// The person's page lists public sites only.
	r := a.at(t, "GET", host, "/", nil, nil)
	if r.status != 200 || !strings.Contains(string(r.body), "/blog/") || strings.Contains(string(r.body), "/shop/\"") {
		t.Fatalf("index: %d", r.status)
	}
	// The old content-host address, once nginx hands it over: served as-is
	// until canonical.
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/", nil, nil); r.status != 200 || string(r.body) != "sub" {
		t.Fatalf("content-host serve: %d %s", r.status, r.body)
	}
	// The directory redirect names the public path, whatever headers say.
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh+"/shop/sub", nil, map[string]string{"X-Original-URI": "//evil.example/x"}); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/"+oh+"/shop/sub/" {
		t.Fatalf("content-host dir redirect: %d %q", r.status, r.header.Get("Location"))
	}
	for _, p := range []string{"//evil.example/shop/sub", "/" + oh + "//evil.example/x", "/shop//evil.example"} {
		if r := a.at(t, "GET", host, p, nil, nil); strings.HasPrefix(r.header.Get("Location"), "//") || strings.HasPrefix(r.header.Get("Location"), "/\\") {
			t.Fatalf("protocol-relative redirect for %s: %q", p, r.header.Get("Location"))
		}
	}
	if r := a.at(t, "GET", host, "//evil.example/shop/sub", nil, nil); strings.HasPrefix(r.header.Get("Location"), "//") {
		t.Fatalf("protocol-relative redirect: %q", r.header.Get("Location"))
	}
	if r := a.at(t, "GET", pcContentHost, "/%69nternal/site-redirect/"+strings.ToUpper(oh)+"/shop/sub", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "/"+oh+"/shop/sub/" {
		t.Fatalf("encoded route dir redirect: %d %q", r.status, r.header.Get("Location"))
	}
	// Serve mode: addresses handed out are still the path URL.
	if r := a.at(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.body), "https://"+pcContentHost+"/"+oh+"/shop/") {
		t.Fatalf("serve: site_url: %s", r.body)
	}

	// ---- API on a person host is bound to that person -----------------------------
	origin := map[string]string{"Origin": "https://" + host}
	if r := a.at(t, "GET", host, "/v1/sites/shop/me", nil, origin); r.status != 200 {
		t.Fatalf("own site: %d %s", r.status, r.body)
	}
	// Another person's site by handle: 404/403, never served.
	if r := a.at(t, "GET", host, "/v1/u/"+sh+"/sites/shop/me", nil, origin); r.status == 200 {
		t.Fatalf("other person's site on this host: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", oscarHost, "/v1/sites/blog/me", nil, map[string]string{"Origin": "https://" + oscarHost}); r.status == 200 {
		t.Fatalf("olive's blog on oscar's host: %d", r.status)
	}
	// A page on olive's host may call the apex API for her sites, not oscar's.
	if r := a.at(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/me", nil, origin); r.status != 200 {
		t.Fatalf("person origin to apex: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/u/"+sh+"/sites/shop/me", nil, origin); r.status != http.StatusForbidden {
		t.Fatalf("person origin to someone else's site: %d %s", r.status, r.body)
	}

	// ---- saves on a person host need a signed-in visitor ------------------------------
	if r := a.at(t, "PUT", apex, "/v1/sites/shop/storage/resources/notes", storageRules("kv", rulesBoard), okey); r.status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.status, r.body)
	}
	save := "/v1/sites/shop/storage/kv/notes/keys/n"
	value := map[string]any{"value": 1}
	if r := a.at(t, "PUT", host, save, value, browser(host, "")); r.status/100 == 2 {
		t.Fatalf("anonymous save on person host: %d %s", r.status, r.body)
	}
	vic := a.newPerson(t, "vic")
	cookie := a.session(t, vic, a.siteID(t, olive, "blog"), host) // signed in on blog
	// Sessions are always bound to one site, including the legacy person path.
	if r := a.at(t, "PUT", host, save, value, browser(host, cookie)); r.status/100 == 2 {
		t.Fatal("blog session accepted on shop")
	}
	cookie = a.session(t, vic, a.siteID(t, olive, "shop"), host)
	if r := a.at(t, "PUT", host, save, value, browser(host, cookie)); r.status != 200 {
		t.Fatalf("signed-in save: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/v1/sites/shop/me", nil, browser(host, cookie)); r.json(t)["signed_in"] != true {
		t.Fatalf("me: %s", r.body)
	}
	// Cross-origin (a sibling host) gets nothing from the cookie.
	if r := a.at(t, "PUT", host, save, value, browser(host, cookie, "Origin", "https://"+oscarHost, "Sec-Fetch-Site", "same-site")); r.status/100 == 2 {
		t.Fatalf("sibling-origin save: %d", r.status)
	}
	// The session is bound to its host.
	if r := a.at(t, "PUT", oscarHost, save, value, browser(oscarHost, cookie)); r.status/100 == 2 {
		t.Fatalf("session on another host: %d", r.status)
	}
	// The owner's key saves; another account's key does not.
	if r := a.at(t, "PUT", host, save, value, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + host}); r.status != 200 {
		t.Fatalf("owner key save: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", host, save, value, map[string]string{"X-API-Key": oscar.key, "Origin": "https://" + host}); r.status/100 == 2 {
		t.Fatalf("other account's key save: %d %s", r.status, r.body)
	}

	// ---- sign-in return_to on a person host ---------------------------------------------
	oa := &OAuthHandler{database: a.database, accounts: accountapp.New(a.database), limitSet: a.sites.limitSet, personSite: a.sites.PersonReturnSite}
	oa.cfg.PublicBaseURL = "https://simple-host.test"
	oa.cfg.SiteDomain = pcSiteDomain
	oa.cfg.ContentHost = pcContentHost
	if _, siteID, h, purpose, err := oa.sanitizeReturnTo(context.Background(), "https://"+host+"/shop/page.html"); err != nil || h != host || purpose != "site" || siteID.String != a.siteID(t, olive, "shop") {
		t.Fatalf("return_to on person host: %v %q %q %v", err, h, purpose, siteID)
	}
	for _, bad := range []string{"https://" + host + "/", "https://" + host + "/nope/", "https://" + oscarHost + "/blog/"} {
		if _, _, _, _, err := oa.sanitizeReturnTo(context.Background(), bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}

	// ---- a site with its own domain lives there ---------------------------------------------
	dom := "olive-blog." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/blog/domain", map[string]string{"domain": dom}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/blog/a/b?q=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+dom+"/a/b?q=1" {
		t.Fatalf("domain redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", host, "/v1/sites/blog/me", nil, browser(host, cookie)); r.json(t)["code"] != "use_custom_domain" {
		t.Fatalf("sign-in for domain site on person host: %d %s", r.status, r.body)
	}

	// ---- namespace -----------------------------------------------------------------------
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": sh + "." + pcSiteDomain}, okey); r.status != http.StatusConflict {
		t.Fatalf("claim someone's handle: %d %s", r.status, r.body)
	}
	fresh := a.newPerson(t, "fresh")
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": "olive-blog"}, map[string]string{"X-API-Key": fresh.key}); r.status != http.StatusConflict || r.json(t)["code"] != "handle_taken" || !strings.Contains(string(r.body), "That address is taken: olive-blog."+pcSiteDomain+" is already in use. Try another.") {
		t.Fatalf("handle on a claimed name: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": "sites"}, map[string]string{"X-API-Key": fresh.key}); r.status != http.StatusConflict || r.json(t)["code"] != "handle_reserved" {
		t.Fatalf("reserved handle: %d %s", r.status, r.body)
	}
	// Another account's live site name: shop.<domain> still redirects to it
	// (LegacyHostRedirect), so it is taken as a handle; the check says so too.
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": "shop"}, map[string]string{"X-API-Key": fresh.key}); r.status != http.StatusConflict || r.json(t)["code"] != "handle_taken" {
		t.Fatalf("handle on another account's site name: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/handles/check?handle=shop", nil, nil); r.json(t)["available"] != false || r.json(t)["address"] != "shop."+pcSiteDomain {
		t.Fatalf("check on a site name: %d %s", r.status, r.body)
	}

	// ---- canonical: every address handed out is the person address -----------------------
	a.sites.SetPersonHosts("canonical")
	r = a.at(t, "GET", apex, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.body), "https://"+host+"/shop/") {
		t.Fatalf("canonical site_url: %s", r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/me", nil, okey); r.json(t)["public_page"] != "https://"+host+"/" {
		t.Fatalf("public_page: %s", r.body)
	}
	if r := a.at(t, "GET", pcContentHost, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + pcContentHost}); r.json(t)["address"] != "https://"+host+"/shop/" {
		t.Fatalf("content-host me address: %s", r.body)
	}

	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+host+"/shop/sub/?q=1" {
		t.Fatalf("content-host redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh, nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+host+"/" {
		t.Fatalf("content-host person page: %d %q", r.status, r.header.Get("Location"))
	}

	// ---- an old handle kept as an alias follows the account ------------------------------
	uid, _ := a.userID(t, olive)
	if _, err := accountsrepo.RenameHandle(context.Background(), a.database, uid, oh+"-new"); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", host, "/shop/x?y=1", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "https://"+oh+"-new."+pcSiteDomain+"/shop/x?y=1" {
		t.Fatalf("alias redirect: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + pcContentHost}); r.status != 200 {
		t.Fatalf("alias API: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh}, map[string]string{"X-API-Key": fresh.key}); r.status != http.StatusConflict {
		t.Fatalf("taking someone's alias: %d %s", r.status, r.body)
	}
}
