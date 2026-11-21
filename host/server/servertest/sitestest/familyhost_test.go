package sitestest

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/config"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// Address families (familyhost.go, familyapi.go). Needs DB_DSN.

const famIP = "203.0.113.7"

func stubFamilyDNS(t *testing.T) *servertest.FamilyDNS {
	t.Helper()
	d := &servertest.FamilyDNS{Txt: map[string]string{}, Away: map[string]string{}}
	oldTXT, oldHost := sites.LookupTXT, sites.LookupHost
	t.Cleanup(func() { sites.LookupTXT, sites.LookupHost = oldTXT, oldHost })
	sites.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := d.Txt[name]; ok {
			return []string{v}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	sites.LookupHost = func(_ context.Context, name string) ([]string, error) {
		if ip, ok := d.Away[name]; ok {
			return []string{ip}, nil
		}
		if _, suffix, ok := strings.Cut(name, "."); ok {
			if ip, ok := d.Away["*."+suffix]; ok {
				return []string{ip}, nil
			}
		}
		return []string{famIP}, nil
	}
	return d
}

// newFamilyApp is a site-hosts app with address families on.
func newFamilyApp(t *testing.T) (*servertest.PrivateApp, string, string, *servertest.FamilyDNS) {
	t.Helper()
	a, siteDir := servertest.NewSiteApp(t, "canonical")
	famDir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(famDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.SetAddressFamilies(famDir)
	a.Sites.Hosts.SetPlatformZones(servertest.PcSiteDomain, "simple-hack.test")
	clearTestFamilies(t, a)
	return a, siteDir, famDir, stubFamilyDNS(t)
}

// clearTestFamilies forgets families and custom domains of earlier runs
// under the reserved .test TLD (the test database is shared across runs, and
// a verified family is exclusive).
func clearTestFamilies(t *testing.T, a *servertest.PrivateApp) {
	t.Helper()
	if _, err := a.Database.Exec(`DELETE FROM address_families WHERE suffix LIKE '%.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL, previous_domain = NULL
		WHERE custom_domain LIKE '%.fam.test' OR custom_domain LIKE '%.block.test' OR custom_domain LIKE '%.proven.test'`); err != nil {
		t.Fatal(err)
	}
}

func TestFamilyServing(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	for _, s := range []string{"meera", "voucher-meera", "draft-guide", "shop"} {
		a.Deploy(t, olive, s)
	}
	a.Deploy(t, oscar, "zed")
	uid, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	okey := map[string]string{"X-API-Key": olive.Key}
	ctx := context.Background()

	// Before anything is live, nothing answers under the family.
	if r := a.At(t, "GET", "meera.quotes.fam.test", "/", nil, nil); r.Status == 200 {
		t.Fatalf("pending family served: %d %s", r.Status, r.Body)
	}
	a.LiveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 1)
	a.LiveFamily(t, famDir, dns, olive, "voucher.fam.test", "voucher-", true, 0)

	get := func(host, path string) servertest.Resp { return a.At(t, "GET", host, path, nil, nil) }
	for host, want := range map[string]string{
		"meera.quotes.fam.test":         "<h1>meera</h1>",
		"voucher-meera.quotes.fam.test": "<h1>voucher-meera</h1>",
		"draft-guide.quotes.fam.test":   "<h1>draft-guide</h1>",
		"meera.voucher.fam.test":        "<h1>voucher-meera</h1>",
	} {
		if r := get(host, "/"); r.Status != 200 || string(r.Body) != want {
			t.Errorf("%s: %d %q, want %q", host, r.Status, r.Body, want)
		}
	}
	if r := get("meera.quotes.fam.test", "/sub/"); r.Status != 200 || string(r.Body) != "sub" {
		t.Errorf("subdirectory: %d %s", r.Status, r.Body)
	}
	// Never another account's site, never a reserved or odd label.
	for _, host := range []string{"zed.quotes.fam.test", "www.quotes.fam.test", "xn--mera-1qa.quotes.fam.test", "nope.quotes.fam.test", "shop.voucher.fam.test", "-x.quotes.fam.test"} {
		if r := get(host, "/"); r.Status == 200 {
			t.Errorf("%s served: %s", host, r.Body)
		}
	}
	for _, p := range []string{"/../../etc/passwd", "/.env", "/sub/../../x"} {
		if r := get("meera.quotes.fam.test", p); r.Status == 200 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}
	if r := a.At(t, "POST", "meera.quotes.fam.test", "/", "x", nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST a file: %d", r.Status)
	}

	// The main address: the most specific canonical family (the voucher-
	// prefix beats none), handed out in the API and redirected to.
	r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.Body), `"family_address":"https://meera.voucher.fam.test/"`) ||
		!strings.Contains(string(r.Body), `"https://voucher-meera.quotes.fam.test/"`) {
		t.Fatalf("family addresses in the site list: %s", r.Body)
	}
	siteHost := "voucher-meera." + oh + "." + servertest.PcSiteDomain
	if r := get(siteHost, "/sub/?x=1"); r.Status != http.StatusFound || r.Header.Get("Location") != "https://meera.voucher.fam.test/sub/?x=1" {
		t.Fatalf("site host -> family: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := get("meera."+oh+"."+servertest.PcSiteDomain, "/"); r.Status != http.StatusFound || r.Header.Get("Location") != "https://meera.quotes.fam.test/" {
		t.Fatalf("site host -> quotes family: %d %q", r.Status, r.Header.Get("Location"))
	}
	if m := a.RedirectMarker(t, olive, "meera"); m != "meera.quotes.fam.test" {
		t.Fatalf("content-host marker = %q", m)
	}
	// Every family address keeps working, the main one or not.
	if r := get("voucher-meera.quotes.fam.test", "/"); r.Status != 200 {
		t.Fatalf("second family address: %d", r.Status)
	}

	// canonical off: the site host is the address again.
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"canonical": false}, okey); r.Status != 200 {
		t.Fatalf("patch: %d %s", r.Status, r.Body)
	}
	if r := get("meera."+oh+"."+servertest.PcSiteDomain, "/"); r.Status != 200 {
		t.Fatalf("canonical off: site host %d %q", r.Status, r.Header.Get("Location"))
	}
	if m := a.RedirectMarker(t, olive, "meera"); m != "" {
		t.Fatalf("canonical off: marker %q", m)
	}
	if r := get("meera.quotes.fam.test", "/"); r.Status != 200 {
		t.Fatalf("canonical off: family address %d", r.Status)
	}
	a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"canonical": true}, okey)

	// A prefix change waits for the issuer to serve it before it counts.
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": "draft-"}, okey); r.Status != 200 || r.JSON(t)["live"] != false {
		t.Fatalf("prefix change live before the issuer: %d %s", r.Status, r.Body)
	}
	if r := get("guide.quotes.fam.test", "/"); r.Status == 200 {
		t.Fatal("new prefix served before the issuer re-rendered")
	}
	servertest.MarkFamilyReady(t, famDir, "quotes.fam.test", "draft-")
	a.Sites.Hosts.RefreshFamilies(ctx)
	if r := get("guide.quotes.fam.test", "/"); r.Status != 200 || string(r.Body) != "<h1>draft-guide</h1>" {
		t.Fatalf("prefix draft-: %d %s", r.Status, r.Body)
	}
	a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": ""}, okey)
	servertest.MarkFamilyReady(t, famDir, "quotes.fam.test", "")
	a.Sites.Hosts.RefreshFamilies(ctx)

	// Rename: the old label redirects to the new one; delete: gone; restore: back.
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/meera", map[string]string{"name": "meera2"}, okey); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if r := get("meera.quotes.fam.test", "/x?y=1"); r.Status != http.StatusFound || r.Header.Get("Location") != "https://meera2.quotes.fam.test/x?y=1" {
		t.Fatalf("renamed: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := get("meera2.quotes.fam.test", "/"); r.Status != 200 {
		t.Fatalf("new name: %d", r.Status)
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/draft-guide", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := get("draft-guide.quotes.fam.test", "/"); r.Status != http.StatusNotFound {
		t.Fatalf("deleted site: %d", r.Status)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/draft-guide/restore", nil, okey); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := get("draft-guide.quotes.fam.test", "/"); r.Status != 200 {
		t.Fatalf("restored site: %d", r.Status)
	}
	// A site created later answers at once.
	a.Deploy(t, olive, "later")
	if r := get("later.quotes.fam.test", "/"); r.Status != 200 {
		t.Fatalf("new site: %d", r.Status)
	}
	if m := a.RedirectMarker(t, olive, "later"); m != "later.quotes.fam.test" {
		t.Fatalf("new site marker = %q", m)
	}

	// Offline and take-down on the family address.
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/shop", map[string]bool{"offline": true}, okey); r.Status != 200 {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	if r := get("shop.quotes.fam.test", "/"); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("offline on family: %d", r.Status)
	}
	a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/shop", map[string]bool{"offline": false}, okey)

	// A domain of its own wins: the family addresses redirect there, and
	// the marker tells nginx.
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": "shop-" + oh + "." + servertest.PcSiteDomain}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := get("shop.quotes.fam.test", "/a?b=1"); r.Status != http.StatusFound || r.Header.Get("Location") != "https://shop-"+oh+"."+servertest.PcSiteDomain+"/a?b=1" {
		t.Fatalf("family -> own domain: %d %q", r.Status, r.Header.Get("Location"))
	}
	if b, err := os.ReadFile(filepath.Join(a.Sites.Disk.SiteDir(uid, "shop"), "lives-elsewhere")); err != nil || strings.TrimSpace(string(b)) != "shop-"+oh+"."+servertest.PcSiteDomain {
		t.Fatalf("lives-elsewhere marker: %q %v", b, err)
	}
	if r := a.At(t, "GET", "shop.quotes.fam.test", "/internal/family/a", nil, nil); r.Status != http.StatusFound {
		// (nginx reaches this only through its own rewrite; the app answers it the same)
		t.Fatalf("/internal/family: %d", r.Status)
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/shop/domain", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("drop domain: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(a.Sites.Disk.SiteDir(uid, "shop"), "lives-elsewhere")); !os.IsNotExist(err) {
		t.Fatalf("lives-elsewhere marker stayed: %v", err)
	}

	// Oscar cannot take any of it; the family is exclusive once verified.
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.quotes.fam.test"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "domain_taken" {
		t.Fatalf("oscar on olive's family: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.x.quotes.fam.test"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
		t.Fatalf("oscar under olive's family: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.fam.test"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
		t.Fatalf("oscar over olive's family: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/zed/domain", map[string]string{"domain": "zed.quotes.fam.test"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "domain_taken" {
		t.Fatalf("oscar's custom domain under olive's family: %d %s", r.Status, r.Body)
	}
	// Olive may put a custom domain of her own under her family: the exact
	// name wins.
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": "special.quotes.fam.test"}, okey); r.Status != 200 {
		t.Fatalf("own custom domain under own family: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_verified_at = now(), domain_status = 'active' WHERE custom_domain = 'special.quotes.fam.test'`); err != nil {
		t.Fatal(err)
	}
	if m, ok, _ := a.Sites.Hosts.FamilySiteForHost(ctx, "special.quotes.fam.test"); ok {
		t.Fatalf("custom domain resolved as family site %s", m.Site.Name)
	}

	// Disconnect: nothing answers there any more, the site host is back.
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/me/address-families/*.voucher.fam.test", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	if r := get("meera.voucher.fam.test", "/"); r.Status == 200 {
		t.Fatal("disconnected family still serves")
	}
	if a.Sites.Disk.FamilyLinkTarget("voucher.fam.test") != "" {
		t.Fatal("link left behind")
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "voucher.fam.test")); !os.IsNotExist(err) {
		t.Fatal("issuer request left behind")
	}
	if r := get(siteHost, "/"); r.Status != http.StatusFound || r.Header.Get("Location") != "https://voucher-meera.quotes.fam.test/" {
		t.Fatalf("after disconnect the next family is main: %d %q", r.Status, r.Header.Get("Location"))
	}
}

// The site API on a family address: the host names the site, the origin
// checks accept its own family addresses only, and saves need the page's
// own address.
func TestFamilyAPIAndOrigins(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "voucher-meera")
	a.Deploy(t, olive, "meera")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	a.LiveFamily(t, famDir, dns, olive, "voucher.fam.test", "voucher-", true, 0)
	vm := a.SiteID(t, olive, "voucher-meera")
	host := "meera.voucher.fam.test"

	// auth.js names the site by the first label: meera means voucher-meera here.
	for _, name := range []string{"meera", "voucher-meera"} {
		if id, err := a.Access.ResolveSiteIDScoped(httptestRequest(host, "/v1/sites/"+name+"/me"), name); err != nil || id != vm {
			t.Errorf("resolve %s on %s: %q %v", name, host, id, err)
		}
	}
	if _, err := a.Access.ResolveSiteIDScoped(httptestRequest(host, "/v1/sites/shop/me"), "shop"); err == nil {
		t.Error("another site resolved on a family address")
	}
	// A page on the family address may save; a page on a sibling may not.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/voucher-meera/storage/resources/notes", servertest.StorageRules("kv", servertest.RulesBoard), map[string]string{"X-API-Key": olive.Key}); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	save := "/v1/sites/meera/storage/kv/notes/keys/n"
	sess := a.Session(t, olive, vm, host)
	if r := a.At(t, "PUT", host, save, map[string]any{"value": 1}, servertest.Browser(host, sess)); r.Status != 200 {
		t.Fatalf("save on the family address: %d %s", r.Status, r.Body)
	}
	sib := "other.voucher.fam.test"
	if r := a.At(t, "PUT", host, save, map[string]any{"value": 2}, servertest.Browser(host, sess, "Origin", "https://"+sib, "Sec-Fetch-Site", "same-site")); r.Status != http.StatusForbidden {
		t.Fatalf("sibling origin saved: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", host, save, map[string]any{"value": 2}, servertest.Browser(host, sess, "Origin", "http://"+host)); r.Status != http.StatusForbidden {
		t.Fatalf("http origin saved: %d %s", r.Status, r.Body)
	}
	// Email sign-in: only from a page on this very host.
	r := a.At(t, "POST", host, "/v1/sites/meera/visitor/auth", map[string]string{"email": "v@example.com"},
		map[string]string{"Origin": "https://" + sib, "Sec-Fetch-Site": "same-site", "X-SH-CSRF": "1"})
	if r.Status != http.StatusForbidden {
		t.Fatalf("email sign-in from a sibling: %d %s", r.Status, r.Body)
	}
	// The visitor cookie counts only as __Host- and same-origin here.
	if got := accesshttp.StrictVisitorCookie(httptestRequestHeaders(host, map[string]string{"Cookie": accesshttp.VisitorCookieHTTP + "=" + sess, "Sec-Fetch-Site": "same-origin", "X-Forwarded-Proto": "https"})); got != "" {
		t.Fatalf("plain cookie read on a family host: %q", got)
	}
}

func httptestRequest(host, path string) *http.Request {
	return httptestRequestHeaders(host, nil)
}

func httptestRequestHeaders(host string, h map[string]string) *http.Request {
	r, _ := http.NewRequest("GET", "https://"+host+"/", nil)
	r.Host = host
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return r
}

// Visitor sign-in (Google) returns to a family address when it points here.
func TestFamilyVisitorSignIn(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	a.WithOAuth(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	a.LiveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	host := "meera.quotes.fam.test"
	returnTo := "https://" + host + "/page.html"
	state, nonce := a.SignInStart(t, host, returnTo)
	loc := a.SignInCallback(t, state, "visitor", host)
	r := a.At(t, "GET", host, loc.RequestURI(), nil, map[string]string{"Cookie": accesshttp.VisitorNonceCookieHost + "=" + nonce, "X-Forwarded-For": "198.51.100.9"})
	if r.Status != http.StatusFound || r.Header.Get("Location") != returnTo {
		t.Fatalf("establish: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	// DNS moved away: no sign-in returns there.
	dns.Away[host] = "192.0.2.50"
	if r := a.At(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil); r.Status == http.StatusFound {
		t.Fatalf("sign-in started for a host that points elsewhere: %d", r.Status)
	}
	delete(dns.Away, host)
	// Nor to another account's family-shaped name or a missing site.
	if r := a.At(t, "GET", "nope.quotes.fam.test", "/v1/visitor/oauth/google?return_to="+url.QueryEscape("https://nope.quotes.fam.test/"), nil, nil); r.Status == http.StatusFound {
		t.Fatalf("sign-in for a missing site: %d", r.Status)
	}
}

// Suffix and label rules, the certificate modes, the cap, and the custom
// domain side (simple-hack.app and *.x refused there).
func TestFamilyBindRules(t *testing.T) {
	a, _, _, _ := newFamilyApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	okey := map[string]string{"X-API-Key": olive.Key}
	post := func(body map[string]any) servertest.Resp {
		return a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", body, okey)
	}
	for _, bad := range []string{"*." + servertest.PcSiteDomain, "x." + servertest.PcSiteDomain, "*.sites." + servertest.PcSiteDomain, "*.simple-hack.test", "*.a.simple-hack.test", "*.co.uk", "*.com", "*.bad_name.com", "*.xn--80ak6aa92e.com", "localhost", ""} {
		if r := post(map[string]any{"suffix": bad}); r.Status != http.StatusBadRequest {
			t.Errorf("%q: %d %s", bad, r.Status, r.Body)
		}
	}
	if r := post(map[string]any{"suffix": "*.brand.test", "cert_mode": "per_host"}); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "cert_mode_unavailable" {
		t.Errorf("per_host: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"-x", "a_b", strings.Repeat("a", 41), "a.b"} {
		if r := post(map[string]any{"suffix": "*.brand.test", "site_prefix": p}); r.Status != http.StatusBadRequest {
			t.Errorf("prefix %q: %d", p, r.Status)
		}
	}
	r := post(map[string]any{"suffix": "https://*.Brand.test/", "site_prefix": "voucher-"})
	if r.Status != http.StatusCreated || r.JSON(t)["family"] != "*.brand.test" || r.JSON(t)["status"] != "pending" {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	j := r.JSON(t)
	if dnsRec, _ := j["dns"].(map[string]any); dnsRec["host"] != "*.brand.test" || dnsRec["value"] != "cname."+servertest.PcSiteDomain {
		t.Errorf("dns: %v", j["dns"])
	}
	if txt, _ := j["dns_txt"].(map[string]any); txt["host"] != "_simple-host.brand.test" || !strings.HasPrefix(txt["value"].(string), "sh-") {
		t.Errorf("txt: %v", j["dns_txt"])
	}
	if cert, _ := j["certificate"].(map[string]any); cert["status"] != "waiting_for_operator" {
		t.Errorf("certificate: %v", j["certificate"])
	}
	if r := post(map[string]any{"suffix": "brand.test"}); r.Status != 200 {
		t.Errorf("again: %d %s", r.Status, r.Body)
	}
	func() {
		old := a.Sites.Limits
		l := old
		l.FamiliesPerAccount = 1
		a.Sites.Limits = l
		defer func() { a.Sites.Limits = old }()
		if r := post(map[string]any{"suffix": "*.other.test"}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "address_family_limit" {
			t.Errorf("cap: %d %s", r.Status, r.Body)
		}
	}()
	// The custom-domain side.
	for _, d := range []string{"simple-hack.test", "x.simple-hack.test"} {
		if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": d}, okey); r.Status != http.StatusBadRequest {
			t.Errorf("custom domain %s: %d %s", d, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": "*.shop.test"}, okey); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "use_address_family" {
		t.Errorf("*.x as a site domain: %d %s", r.Status, r.Body)
	}
	// Families off: refused with a clear code.
	a.Sites.SetAddressFamilies("")
	if r := post(map[string]any{"suffix": "*.late.test"}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "address_families_unavailable" {
		t.Errorf("off: %d %s", r.Status, r.Body)
	}
}

// Verification, the first account to prove a name winning it, failing
// checks, the warning and the Release; the proof exemption.
func TestFamilyLifecycle(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "meera")
	a.Deploy(t, oscar, "zed")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	ctx := context.Background()
	ours := a.Sites.Hosts.ServerAddrs(ctx)

	mk := func(p servertest.Person, suffix string) model.AddressFamily {
		r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": suffix}, map[string]string{"X-API-Key": p.Key})
		if r.Status != http.StatusCreated {
			t.Fatalf("connect %s: %d %s", suffix, r.Status, r.Body)
		}
		f, _ := sitesrepo.GetFamilyByID(ctx, a.Database, r.JSON(t)["id"].(string))
		return f
	}
	fo := mk(olive, "*.race.test")
	fs := mk(oscar, "*.race.test") // both may wait on it
	// No TXT yet: pending with the reason.
	a.Sites.CheckFamily(ctx, fo, ours)
	fo, _ = sitesrepo.GetFamilyByID(ctx, a.Database, fo.ID)
	if fo.VerifiedAt.Valid || !strings.Contains(fo.LastError, "_simple-host.race.test") {
		t.Fatalf("no TXT: %+v", fo)
	}
	// The wildcard pointing elsewhere is not enough either.
	dns.Txt["_simple-host.race.test"] = fo.Token
	dns.Away["*.race.test"] = "192.0.2.50"
	a.Sites.CheckFamily(ctx, fo, ours)
	fo, _ = sitesrepo.GetFamilyByID(ctx, a.Database, fo.ID)
	if fo.VerifiedAt.Valid || !strings.Contains(fo.LastError, "not this server") {
		t.Fatalf("wildcard elsewhere: %+v", fo)
	}
	delete(dns.Away, "*.race.test")
	a.Sites.CheckFamily(ctx, fo, ours)
	fo, _ = sitesrepo.GetFamilyByID(ctx, a.Database, fo.ID)
	if !fo.VerifiedAt.Valid || fo.Status != "active" {
		t.Fatalf("verified: %+v", fo)
	}
	if _, err := sitesrepo.GetFamilyByID(ctx, a.Database, fs.ID); err == nil {
		t.Fatal("the other account's pending family was not let go")
	}

	// Failing: the clock starts; past the lapse it is let go.
	delete(dns.Txt, "_simple-host.race.test")
	a.Sites.CheckFamily(ctx, fo, ours)
	fo, _ = sitesrepo.GetFamilyByID(ctx, a.Database, fo.ID)
	if fo.Status != "failing" || !fo.FailingSince.Valid || !fo.VerifiedAt.Valid {
		t.Fatalf("failing: %+v", fo)
	}
	if _, err := a.Database.Exec(`UPDATE address_families SET failing_since = now() - interval '100 hours' WHERE id = $1`, fo.ID); err != nil {
		t.Fatal(err)
	}
	fo, _ = sitesrepo.GetFamilyByID(ctx, a.Database, fo.ID)
	a.Sites.CheckFamily(ctx, fo, ours)
	if _, err := sitesrepo.GetFamilyByID(ctx, a.Database, fo.ID); err == nil {
		t.Fatal("lapsed family kept")
	}
	if a.Sites.Disk.FamilyLinkTarget("race.test") != "" {
		t.Fatal("lapsed family's link kept")
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "race.test")); !os.IsNotExist(err) {
		t.Fatal("lapsed family's request kept")
	}

	// Proof exempt (admin): no TXT needed, the wildcard still is.
	r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/admin/users/"+a.SiteIDOwner(t, olive)+"/address-families",
		map[string]any{"suffix": "*.exempt.test", "proof_exempt": true, "cert_name": "exempt.test", "canonical": false},
		map[string]string{"X-API-Key": a.Admin})
	if r.Status != http.StatusCreated || r.JSON(t)["proof_exempt"] != true || r.JSON(t)["dns_txt"] != nil {
		t.Fatalf("admin create: %d %s", r.Status, r.Body)
	}
	fe, _ := sitesrepo.GetFamilyByID(ctx, a.Database, r.JSON(t)["id"].(string))
	a.Sites.CheckFamily(ctx, fe, ours)
	fe, _ = sitesrepo.GetFamilyByID(ctx, a.Database, fe.ID)
	if !fe.VerifiedAt.Valid {
		t.Fatalf("exempt family not verified: %+v", fe)
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "exempt.test")); err != nil {
		t.Fatalf("exempt family's request: %v", err)
	}
	// Owners cannot set the exemption or the certificate themselves.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/admin/address-families/"+fe.ID+"/proof-exempt", map[string]bool{"proof_exempt": false}, map[string]string{"X-API-Key": olive.Key}); r.Status == 200 {
		t.Fatalf("owner changed the proof exemption: %d", r.Status)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/admin/address-families/"+fe.ID+"/cert-mode", map[string]string{"cert_mode": "per_host"}, map[string]string{"X-API-Key": a.Admin}); r.Status != http.StatusBadRequest {
		t.Fatalf("admin per_host: %d %s", r.Status, r.Body)
	}
	// Never proved: dropped after ADDRESS_FAMILY_UNPROVEN_HOURS.
	fp := mk(oscar, "*.late.test")
	if _, err := a.Database.Exec(`UPDATE address_families SET bound_at = now() - interval '30 hours' WHERE id = $1`, fp.ID); err != nil {
		t.Fatal(err)
	}
	a.Sites.CheckFamilies(ctx)
	if _, err := sitesrepo.GetFamilyByID(ctx, a.Database, fp.ID); err == nil {
		t.Fatal("unproven family kept")
	}
	// The admin list shows every family with its owner.
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/admin/address-families", nil, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 || !strings.Contains(string(r.Body), "*.exempt.test") || !strings.Contains(string(r.Body), olive.Email) {
		t.Fatalf("admin list: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/admin/address-families", nil, map[string]string{"X-API-Key": olive.Key}); r.Status == 200 {
		t.Fatal("owner read the admin list")
	}
	_ = time.Now
}

// The passcode gate covers family addresses: the Go path, the nginx marker
// rewrite (/internal/passcode on the family host) and the unlock there.
func TestFamilyPasscode(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	if err := a.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &a.Sites.Limits, &a.Access.Limits, &a.Data.Limits)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	a.LiveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	host := "meera.quotes.fam.test"
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/meera/lock", map[string]string{"passcode": "Trip-2026-x"}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/", "/sub/", "/nope"} {
		if r := a.At(t, "GET", host, p, nil, nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "<h1>meera</h1>") {
			t.Errorf("family %s: %d", p, r.Status)
		}
		if r := a.At(t, "GET", host, "/internal/passcode"+p, nil, nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "<h1>meera</h1>") {
			t.Errorf("marker rewrite %s: %d", p, r.Status)
		}
	}
	// Through nginx's rewrite the form sends the visitor back to the page
	// they asked for, never /internal/passcode/... (a 404 from outside).
	if r := a.At(t, "GET", host, "/internal/passcode/sub/?day=2", nil, nil); r.Status != http.StatusUnauthorized || !strings.Contains(string(r.Body), `name="next" value="/sub/?day=2"`) || strings.Contains(string(r.Body), "/internal/") {
		t.Fatalf("family gate next through the rewrite: %d %s", r.Status, r.Body)
	}
	form := url.Values{"passcode": {"Trip-2026-x"}, "next": {"/internal/passcode/sub/"}}.Encode()
	r := a.At(t, "POST", host, "/v1/site-unlock", form, map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"})
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/sub/" {
		t.Fatalf("unlock: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	var cookie string
	for _, c := range r.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "__Host-sh_pass_") {
			cookie = strings.SplitN(c, ";", 2)[0]
		}
	}
	if cookie == "" {
		t.Fatalf("no unlock cookie: %v", r.Header.Values("Set-Cookie"))
	}
	if r := a.At(t, "GET", host, "/sub/", nil, map[string]string{"Cookie": cookie}); r.Status != 200 || string(r.Body) != "sub" {
		t.Fatalf("unlocked: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/internal/passcode/sub/", nil, map[string]string{"Cookie": cookie}); r.Status != 200 {
		t.Fatalf("unlocked via marker: %d", r.Status)
	}
	// The cookie is this host's only.
	a.Deploy(t, olive, "other")
	if r := a.At(t, "GET", "meera."+oh+"."+servertest.PcSiteDomain, "/", nil, map[string]string{"Cookie": cookie}); r.Status == 200 {
		t.Fatal("family unlock opened the site host")
	}
}

// Everything else that follows a site's addresses.
func TestFamilyElsewhere(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	uid, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	a.LiveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	ctx := context.Background()

	// The report form counts a family address as hosted here.
	rh := a.Sites.ReportHandler().(*siteshttp.ReportHandler)
	if ok, err := rh.Hosted(ctx, "meera.quotes.fam.test"); !ok || err != nil {
		t.Errorf("report: family address not hosted: %v %v", ok, err)
	}
	if ok, _ := rh.Hosted(ctx, "nope.quotes.fam.test"); ok {
		t.Error("report: missing site counted as hosted")
	}
	// The data download names the family.
	r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/me/export.zip", nil, map[string]string{"X-API-Key": olive.Key})
	found := false
	for _, body := range servertest.ZipFiles(t, r.Body) {
		found = found || strings.Contains(body, "*.quotes.fam.test")
	}
	if r.Status != 200 || !found {
		t.Fatalf("export: %d", r.Status)
	}
	// Deleting the account removes the link and the issuer request.
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/me", map[string]string{"confirm": oh}, map[string]string{"X-API-Key": olive.Key}); r.Status/100 != 2 {
		t.Fatalf("delete account: %d %s", r.Status, r.Body)
	}
	if a.Sites.Disk.FamilyLinkTarget("quotes.fam.test") != "" {
		t.Fatal("erased account's family link kept")
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "quotes.fam.test")); !os.IsNotExist(err) {
		t.Fatal("erased account's issuer request kept")
	}
	_ = uid
}

// While the base moves (SITE_BASE_MOVE=redirect), an old site host whose
// site lives at a family address goes there in one hop.
func TestFamilyBaseMoveOneHop(t *testing.T) {
	a, siteDir, baseDir := newBaseApp(t, "redirect")
	clearTestFamilies(t, a)
	famDir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(famDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.SetAddressFamilies(famDir)
	dns := stubFamilyDNS(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	a.Deploy(t, olive, "plain")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	servertest.MarkReady(t, baseDir, oh)
	a.LiveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": "m"}, map[string]string{"X-API-Key": olive.Key})
	servertest.MarkFamilyReady(t, famDir, "quotes.fam.test", "m")
	a.Sites.Hosts.RefreshFamilies(context.Background())
	old := "meera." + oh + "." + servertest.PcSiteDomain
	if r := a.At(t, "GET", old, "/p?q=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://eera.quotes.fam.test/p?q=1" {
		t.Fatalf("old site host: %d %q", r.Status, r.Header.Get("Location"))
	}
	// A site without one moves to the new base as before.
	if r := a.At(t, "GET", "plain."+oh+"."+servertest.PcSiteDomain, "/", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://plain."+oh+"."+sbBase+"/" {
		t.Fatalf("plain site: %d %q", r.Status, r.Header.Get("Location"))
	}
}

// Idle cleanup keeps a site whose main address is a verified family of its
// account (IDLE_EXEMPT_FAMILY_SITES), and not one outside the family.
func TestFamilyIdleExempt(t *testing.T) {
	a, _, _, _ := newFamilyApp(t)
	ctx := context.Background()
	a.Sites.SetIdleCleanup(true)
	a.Sites.SetIdleExempt(nil, "")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "voucher-meera")
	a.Deploy(t, olive, "quiet")
	uid, _ := a.UserID(t, olive)
	for _, s := range []string{"voucher-meera", "quiet"} {
		id := a.SiteID(t, olive, s)
		if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET created_at = now() - interval '130 days', updated_at = now() - interval '130 days' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Database.ExecContext(ctx, `UPDATE versions SET created_at = now() - interval '130 days' WHERE site_id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Database.ExecContext(ctx, `INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.ExecContext(ctx, `INSERT INTO address_families (user_id, suffix, site_prefix, verified_at, status) VALUES ($1, $2, 'voucher-', now(), 'active')`, uid, "idle-"+strings.ToLower(uid[:8])+".test"); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/admin/idle-sites", nil, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 200 || strings.Contains(string(r.Body), `"site":"voucher-meera"`) || !strings.Contains(string(r.Body), `"site":"quiet"`) {
		t.Fatalf("idle dry run: %d %s", r.Status, r.Body)
	}
	func() {
		old := a.Sites.Limits
		l := old
		l.IdleExemptFamilySites = false
		a.Sites.Limits = l
		defer func() { a.Sites.Limits = old }()
		a.Sites.SetIdleExempt(nil, "")
		r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/admin/idle-sites", nil, map[string]string{"X-API-Key": a.Admin})
		if !strings.Contains(string(r.Body), `"site":"voucher-meera"`) {
			t.Fatalf("exemption off: %s", r.Body)
		}
	}()
}

// A stranger's pending custom domain under a name never holds a family off
// (anyone can bind one without proof); a proven one does.
func TestFamilyPendingDomainDoesNotBlock(t *testing.T) {
	a, _, _, dns := newFamilyApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "zed")
	a.Deploy(t, oscar, "yak")
	ctx := context.Background()
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/zed/domain", map[string]string{"domain": "zed.block.test"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != 200 {
		t.Fatalf("oscar binds: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.block.test"}, map[string]string{"X-API-Key": olive.Key})
	if r.Status != http.StatusCreated {
		t.Fatalf("olive blocked by a pending domain: %d %s", r.Status, r.Body)
	}
	f, _ := sitesrepo.GetFamilyByID(ctx, a.Database, r.JSON(t)["id"].(string))
	dns.Txt["_simple-host.block.test"] = f.Token
	a.Sites.CheckFamily(ctx, f, a.Sites.Hosts.ServerAddrs(ctx))
	if f, _ = sitesrepo.GetFamilyByID(ctx, a.Database, f.ID); !f.VerifiedAt.Valid {
		t.Fatalf("not verified past a pending domain: %+v", f)
	}
	// A proven domain of another account does hold a name.
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/yak/domain", map[string]string{"domain": "yak.proven.test"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != 200 {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_verified_at = now(), domain_status = 'active' WHERE custom_domain = 'yak.proven.test'`); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.proven.test"}, map[string]string{"X-API-Key": olive.Key}); r.Status != http.StatusConflict {
		t.Fatalf("family over a proven domain: %d %s", r.Status, r.Body)
	}
}

// A host under a connected family is never handed to the platform's own
// pages, whatever the family's state: pending, verified but not live (no
// ready marker yet, or the routing index not refreshed yet), live with a
// missing label, or let go. It is a plain 404. And "live" in the API is
// what requests are routed by, so a caller waiting for it (family-adopt.sh)
// knows the family answers.
func TestFamilyHostNeverPlatform(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	a.Mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("PLATFORM-HOME")) })
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "meera")
	uid, oh := a.UserID(t, olive)
	servertest.MarkReady(t, siteDir, oh)
	okey := map[string]string{"X-API-Key": olive.Key}
	ctx := context.Background()

	if r := a.At(t, "GET", "nope.other.test", "/", nil, nil); string(r.Body) != "PLATFORM-HOME" {
		t.Fatalf("control: an unrelated host should reach the platform: %d %s", r.Status, r.Body)
	}
	plain404 := func(stage, host string) {
		t.Helper()
		for _, p := range []string{"/", "/index.html", "/x/y"} {
			r := a.At(t, "GET", host, p, nil, nil)
			if r.Status != http.StatusNotFound || strings.Contains(string(r.Body), "PLATFORM") {
				t.Errorf("%s: %s%s = %d %q, want a plain 404", stage, host, p, r.Status, r.Body)
			}
		}
	}
	liveOf := func(suffix string) bool {
		t.Helper()
		r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/admin/address-families", nil, map[string]string{"X-API-Key": a.Admin})
		for _, f := range r.JSON(t)["address_families"].([]any) {
			m := f.(map[string]any)
			if m["suffix"] == suffix {
				return m["live"].(bool)
			}
		}
		t.Fatalf("%s not listed: %s", suffix, r.Body)
		return false
	}

	// Pending and unproven: anyone may connect any name, so it must not
	// blank that name's hosts; they are what they were before.
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/me/address-families", map[string]any{"suffix": "*.pend.fam.test"}, okey); r.Status != http.StatusCreated {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "nosuch.pend.fam.test", "/", nil, nil); string(r.Body) != "PLATFORM-HOME" {
		t.Errorf("an unproven family changed its hosts: %d %s", r.Status, r.Body)
	}
	// Pending but proof-exempt (the admin connected it for a name the
	// operator runs): its hosts are the family's, a plain 404.
	r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/admin/users/"+uid+"/address-families",
		map[string]any{"suffix": "*.exempt.fam.test", "proof_exempt": true, "canonical": false}, map[string]string{"X-API-Key": a.Admin})
	if r.Status != http.StatusCreated {
		t.Fatalf("admin connect: %d %s", r.Status, r.Body)
	}
	plain404("pending, proof-exempt", "meera.exempt.fam.test")
	plain404("pending, proof-exempt", "nosuch.exempt.fam.test")

	// Live, then a missing label.
	f := a.LiveFamily(t, famDir, dns, olive, "trips.fam.test", "", false, 2)
	if !liveOf("trips.fam.test") {
		t.Fatal("live family reported not live")
	}
	if r := a.At(t, "GET", "meera.trips.fam.test", "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>meera</h1>" {
		t.Fatalf("live: %d %s", r.Status, r.Body)
	}
	plain404("live, missing label", "nosuch.trips.fam.test")
	plain404("live, reserved label", "www.trips.fam.test")

	// The ready marker gone (a rollback), the index not refreshed yet: the
	// API still says live because requests are still routed as live; after
	// the refresh it says not live, and nothing reaches the platform.
	if err := os.Remove(filepath.Join(famDir, "ready", "trips.fam.test")); err != nil {
		t.Fatal(err)
	}
	if !liveOf("trips.fam.test") {
		t.Error("live must follow the routing index, not the disk")
	}
	a.Sites.Hosts.RefreshFamilies(ctx)
	if liveOf("trips.fam.test") {
		t.Error("not live after the refresh")
	}
	plain404("verified, not live", "meera.trips.fam.test")
	plain404("verified, not live", "nosuch.trips.fam.test")

	// The marker back, index not refreshed: not live yet (the window
	// family-adopt.sh waits out); then live.
	servertest.MarkFamilyReady(t, famDir, "trips.fam.test", "")
	if liveOf("trips.fam.test") {
		t.Error("live before the index knows it")
	}
	plain404("ready, index stale", "nosuch.trips.fam.test")
	r = a.At(t, "POST", servertest.PcSiteDomain, "/v1/admin/address-families/"+f.ID+"/check", nil, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 200 || r.JSON(t)["live"] != true {
		t.Fatalf("check did not bring the index up to date: %d %s", r.Status, r.Body)
	}

	// Let go (a lapse, or the admin's disconnect): still never the platform.
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/admin/address-families/"+f.ID, nil, map[string]string{"X-API-Key": a.Admin}); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	a.Sites.Hosts.RefreshFamilies(ctx)
	plain404("released", "meera.trips.fam.test")
	plain404("released", "nosuch.trips.fam.test")
	if r := a.At(t, "GET", "nope.other.test", "/", nil, nil); string(r.Body) != "PLATFORM-HOME" {
		t.Fatalf("control after: %d %s", r.Status, r.Body)
	}
}
