package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/server/servertest"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

func TestPersonHostsOffKeepsPathModel(t *testing.T) {
	a := servertest.NewPersonApp(t, "off")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	_, handle := a.UserID(t, olive)
	host := handle + "." + servertest.PcSiteDomain
	// Off: the name is a retired per-name host (legacy 301), never served.
	if r := a.At(t, "GET", host, "/shop/", nil, nil); r.Status != http.StatusMovedPermanently {
		t.Fatalf("off: person host answered %d", r.Status)
	}
	r := a.At(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.Key})
	if !strings.Contains(string(r.Body), "https://"+servertest.PcContentHost+"/"+handle+"/shop/") {
		t.Fatalf("off: site_url: %s", r.Body)
	}
}

func TestPersonHostsServeAndCanonical(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "shop") // older same-named site: the legacy name lookup would pick it
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	_, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	host := oh + "." + servertest.PcSiteDomain
	oscarHost := sh + "." + servertest.PcSiteDomain
	apex := "simple-host.test"
	okey := map[string]string{"X-API-Key": olive.Key}
	if _, err := a.Database.Exec(`UPDATE sites SET visibility = 'public' WHERE id = $1`, a.SiteID(t, olive, "blog")); err != nil {
		t.Fatal(err)
	}

	// ---- serving ---------------------------------------------------------------
	if r := a.At(t, "GET", host, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("serve site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/shop", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/shop/" {
		t.Fatalf("site without slash: %d %s", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", host, "/shop/sub", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/shop/sub/" || r.Header.Get("Cache-Control") != publicPageCache {
		t.Fatalf("dir without slash: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", host, "/shop/sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("dir: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/"+oh+"/shop/sub/?x=1", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/shop/sub/?x=1" {
		t.Fatalf("old path shim: %d %q", r.Status, r.Header.Get("Location"))
	}
	for _, p := range []string{"/nope/", "/shop/../../../etc/passwd", "/shop/nope.html", "/internal/tls-ask?domain=x", "/mcp", "/.env"} {
		if r := a.At(t, "GET", host, p, nil, nil); r.Status != 404 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := a.At(t, "POST", host, "/shop/", nil, nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST page: %d", r.Status)
	}
	// The person's page lists public sites only.
	r := a.At(t, "GET", host, "/", nil, nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), "/blog/") || strings.Contains(string(r.Body), "/shop/\"") {
		t.Fatalf("index: %d", r.Status)
	}
	// The old content-host address, once nginx hands it over: served as-is
	// until canonical.
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("content-host serve: %d %s", r.Status, r.Body)
	}
	// The directory redirect names the public path, whatever headers say.
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh+"/shop/sub", nil, map[string]string{"X-Original-URI": "//evil.example/x"}); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/"+oh+"/shop/sub/" {
		t.Fatalf("content-host dir redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	for _, p := range []string{"//evil.example/shop/sub", "/" + oh + "//evil.example/x", "/shop//evil.example"} {
		if r := a.At(t, "GET", host, p, nil, nil); strings.HasPrefix(r.Header.Get("Location"), "//") || strings.HasPrefix(r.Header.Get("Location"), "/\\") {
			t.Fatalf("protocol-relative redirect for %s: %q", p, r.Header.Get("Location"))
		}
	}
	if r := a.At(t, "GET", host, "//evil.example/shop/sub", nil, nil); strings.HasPrefix(r.Header.Get("Location"), "//") {
		t.Fatalf("protocol-relative redirect: %q", r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", servertest.PcContentHost, "/%69nternal/site-redirect/"+strings.ToUpper(oh)+"/shop/sub", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "/"+oh+"/shop/sub/" {
		t.Fatalf("encoded route dir redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	// Serve mode: addresses handed out are still the path URL.
	if r := a.At(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.Body), "https://"+servertest.PcContentHost+"/"+oh+"/shop/") {
		t.Fatalf("serve: site_url: %s", r.Body)
	}

	// ---- API on a person host is bound to that person -----------------------------
	origin := map[string]string{"Origin": "https://" + host}
	if r := a.At(t, "GET", host, "/v1/sites/shop/me", nil, origin); r.Status != 200 {
		t.Fatalf("own site: %d %s", r.Status, r.Body)
	}
	// Another person's site by handle: 404/403, never served.
	if r := a.At(t, "GET", host, "/v1/u/"+sh+"/sites/shop/me", nil, origin); r.Status == 200 {
		t.Fatalf("other person's site on this host: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", oscarHost, "/v1/sites/blog/me", nil, map[string]string{"Origin": "https://" + oscarHost}); r.Status == 200 {
		t.Fatalf("olive's blog on oscar's host: %d", r.Status)
	}
	// A page on olive's host may call the apex API for her sites, not oscar's.
	if r := a.At(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/me", nil, origin); r.Status != 200 {
		t.Fatalf("person origin to apex: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/u/"+sh+"/sites/shop/me", nil, origin); r.Status != http.StatusForbidden {
		t.Fatalf("person origin to someone else's site: %d %s", r.Status, r.Body)
	}

	// ---- saves on a person host need a signed-in visitor ------------------------------
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/resources/notes", servertest.StorageRules("kv", servertest.RulesBoard), okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	save := "/v1/sites/shop/storage/kv/notes/keys/n"
	value := map[string]any{"value": 1}
	if r := a.At(t, "PUT", host, save, value, servertest.Browser(host, "")); r.Status/100 == 2 {
		t.Fatalf("anonymous save on person host: %d %s", r.Status, r.Body)
	}
	vic := a.NewPerson(t, "vic")
	cookie := a.Session(t, vic, a.SiteID(t, olive, "blog"), host) // signed in on blog
	// Sessions are always bound to one site, including the legacy person path.
	if r := a.At(t, "PUT", host, save, value, servertest.Browser(host, cookie)); r.Status/100 == 2 {
		t.Fatal("blog session accepted on shop")
	}
	cookie = a.Session(t, vic, a.SiteID(t, olive, "shop"), host)
	if r := a.At(t, "PUT", host, save, value, servertest.Browser(host, cookie)); r.Status != 200 {
		t.Fatalf("signed-in save: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/me", nil, servertest.Browser(host, cookie)); r.JSON(t)["signed_in"] != true {
		t.Fatalf("me: %s", r.Body)
	}
	// Cross-origin (a sibling host) gets nothing from the cookie.
	if r := a.At(t, "PUT", host, save, value, servertest.Browser(host, cookie, "Origin", "https://"+oscarHost, "Sec-Fetch-Site", "same-site")); r.Status/100 == 2 {
		t.Fatalf("sibling-origin save: %d", r.Status)
	}
	// The session is bound to its host.
	if r := a.At(t, "PUT", oscarHost, save, value, servertest.Browser(oscarHost, cookie)); r.Status/100 == 2 {
		t.Fatalf("session on another host: %d", r.Status)
	}
	// The owner's key saves; another account's key does not.
	if r := a.At(t, "PUT", host, save, value, map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + host}); r.Status != 200 {
		t.Fatalf("owner key save: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", host, save, value, map[string]string{"X-API-Key": oscar.Key, "Origin": "https://" + host}); r.Status/100 == 2 {
		t.Fatalf("other account's key save: %d %s", r.Status, r.Body)
	}

	// ---- sign-in return_to on a person host ---------------------------------------------
	oa := &accesshttp.OAuthHandler{Database: a.Database, Accounts: accountapp.New(a.Database), Limits: a.Sites.Limits, PersonSite: a.Sites.Hosts.ReturnSite}
	oa.Cfg.PublicBaseURL = "https://simple-host.test"
	oa.Cfg.SiteDomain = servertest.PcSiteDomain
	oa.Cfg.ContentHost = servertest.PcContentHost
	if _, siteID, h, purpose, err := oa.SanitizeReturnTo(context.Background(), "https://"+host+"/shop/page.html"); err != nil || h != host || purpose != "site" || siteID.String != a.SiteID(t, olive, "shop") {
		t.Fatalf("return_to on person host: %v %q %q %v", err, h, purpose, siteID)
	}
	for _, bad := range []string{"https://" + host + "/", "https://" + host + "/nope/", "https://" + oscarHost + "/blog/"} {
		if _, _, _, _, err := oa.SanitizeReturnTo(context.Background(), bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}

	// ---- a site with its own domain lives there ---------------------------------------------
	dom := "olive-blog." + servertest.PcSiteDomain
	if r := a.At(t, "POST", apex, "/v1/sites/blog/domain", map[string]string{"domain": dom}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/blog/a/b?q=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+dom+"/a/b?q=1" {
		t.Fatalf("domain redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", host, "/v1/sites/blog/me", nil, servertest.Browser(host, cookie)); r.JSON(t)["code"] != "use_custom_domain" {
		t.Fatalf("sign-in for domain site on person host: %d %s", r.Status, r.Body)
	}

	// ---- namespace -----------------------------------------------------------------------
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": sh + "." + servertest.PcSiteDomain}, okey); r.Status != http.StatusConflict {
		t.Fatalf("claim someone's handle: %d %s", r.Status, r.Body)
	}
	fresh := a.NewPerson(t, "fresh")
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": "olive-blog"}, map[string]string{"X-API-Key": fresh.Key}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "handle_taken" || !strings.Contains(string(r.Body), "That address is taken: olive-blog."+servertest.PcSiteDomain+" is already in use. Try another.") {
		t.Fatalf("handle on a claimed name: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": "sites"}, map[string]string{"X-API-Key": fresh.Key}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "handle_reserved" {
		t.Fatalf("reserved handle: %d %s", r.Status, r.Body)
	}
	// Another account's live site name: shop.<domain> still redirects to it
	// (LegacyHostRedirect), so it is taken as a handle; the check says so too.
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": "shop"}, map[string]string{"X-API-Key": fresh.Key}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "handle_taken" {
		t.Fatalf("handle on another account's site name: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/handles/check?handle=shop", nil, nil); r.JSON(t)["available"] != false || r.JSON(t)["address"] != "shop."+servertest.PcSiteDomain {
		t.Fatalf("check on a site name: %d %s", r.Status, r.Body)
	}

	// ---- canonical: every address handed out is the person address -----------------------
	a.Sites.Hosts.SetPersonHosts("canonical")
	r = a.At(t, "GET", apex, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.Body), "https://"+host+"/shop/") {
		t.Fatalf("canonical site_url: %s", r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me", nil, okey); r.JSON(t)["public_page"] != "https://"+host+"/" {
		t.Fatalf("public_page: %s", r.Body)
	}
	if r := a.At(t, "GET", servertest.PcContentHost, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + servertest.PcContentHost}); r.JSON(t)["address"] != "https://"+host+"/shop/" {
		t.Fatalf("content-host me address: %s", r.Body)
	}

	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+host+"/shop/sub/?q=1" {
		t.Fatalf("content-host redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh, nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+host+"/" {
		t.Fatalf("content-host person page: %d %q", r.Status, r.Header.Get("Location"))
	}

	// ---- an old handle kept as an alias follows the account ------------------------------
	uid, _ := a.UserID(t, olive)
	if _, err := accountsrepo.RenameHandle(context.Background(), a.Database, uid, oh+"-new"); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", host, "/shop/x?y=1", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "https://"+oh+"-new."+servertest.PcSiteDomain+"/shop/x?y=1" {
		t.Fatalf("alias redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/me", nil, map[string]string{"Origin": "https://" + servertest.PcContentHost}); r.Status != 200 {
		t.Fatalf("alias API: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh}, map[string]string{"X-API-Key": fresh.Key}); r.Status != http.StatusConflict {
		t.Fatalf("taking someone's alias: %d %s", r.Status, r.Body)
	}
}
