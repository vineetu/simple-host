package handler

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

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Address families (familyhost.go, familyapi.go). Needs DB_DSN.

const famIP = "203.0.113.7"

// familyDNS stands in for DNS: every name resolves to famIP (this server,
// through the CNAME target) unless listed in away; TXT records by name.
type familyDNS struct {
	txt  map[string]string
	away map[string]string // host (or "*.<suffix>") -> another address
}

func stubFamilyDNS(t *testing.T) *familyDNS {
	t.Helper()
	d := &familyDNS{txt: map[string]string{}, away: map[string]string{}}
	oldTXT, oldHost := lookupTXT, lookupHost
	t.Cleanup(func() { lookupTXT, lookupHost = oldTXT, oldHost })
	lookupTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := d.txt[name]; ok {
			return []string{v}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	lookupHost = func(_ context.Context, name string) ([]string, error) {
		if ip, ok := d.away[name]; ok {
			return []string{ip}, nil
		}
		if _, suffix, ok := strings.Cut(name, "."); ok {
			if ip, ok := d.away["*."+suffix]; ok {
				return []string{ip}, nil
			}
		}
		return []string{famIP}, nil
	}
	return d
}

// newFamilyApp is a site-hosts app with address families on.
func newFamilyApp(t *testing.T) (*privateApp, string, string, *familyDNS) {
	t.Helper()
	a, siteDir := newSiteApp(t, "canonical")
	famDir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(famDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sites.SetAddressFamilies(famDir)
	a.sites.SetPlatformZones(pcSiteDomain, "simple-hack.test")
	return a, siteDir, famDir, stubFamilyDNS(t)
}

// liveFamily connects *.<suffix> for p and brings it all the way to live:
// the TXT record, the check, the operator's certificate and the issuer's
// ready marker.
func (a *privateApp) liveFamily(t *testing.T, famDir string, dns *familyDNS, p person, suffix, prefix string, canonical bool, rank int) db.AddressFamily {
	t.Helper()
	r := a.at(t, "POST", pcSiteDomain, "/v1/me/address-families",
		map[string]any{"suffix": "*." + suffix, "site_prefix": prefix, "canonical": canonical, "rank": rank},
		map[string]string{"X-API-Key": p.key})
	if r.status != http.StatusCreated {
		t.Fatalf("connect *.%s: %d %s", suffix, r.status, r.body)
	}
	id := r.json(t)["id"].(string)
	f, err := db.GetFamilyByID(context.Background(), a.database, id)
	if err != nil {
		t.Fatal(err)
	}
	dns.txt["_simple-host."+suffix] = f.Token
	a.sites.checkFamily(context.Background(), f, a.sites.serverAddrs(context.Background()))
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/admin/address-families/"+id+"/cert-mode",
		map[string]string{"cert_mode": "wildcard", "cert_name": suffix}, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("cert: %d %s", r.status, r.body)
	}
	f, _ = db.GetFamilyByID(context.Background(), a.database, id)
	if !f.VerifiedAt.Valid {
		t.Fatalf("*.%s not verified: %+v", suffix, f)
	}
	req, err := os.ReadFile(filepath.Join(famDir, "requests", suffix))
	if err != nil {
		t.Fatalf("no issuer request: %v", err)
	}
	want := f.Token + "\n../by-id/" + f.UserID + "\nwildcard\n" + suffix + "\n" + prefix + "\nwww\n"
	if string(req) != want {
		t.Fatalf("request = %q, want %q", req, want)
	}
	if got := a.sites.disk.FamilyLinkTarget(suffix); got != "../by-id/"+f.UserID {
		t.Fatalf("family link = %q", got)
	}
	markFamilyReady(t, famDir, suffix, prefix)
	a.sites.refreshFamilies(context.Background())
	return f
}

func markFamilyReady(t *testing.T, famDir, suffix, prefix string) {
	t.Helper()
	body := "prefix=" + prefix + "\ncert=" + suffix + "\nexpires=" + "1900000000" + "\nreserved=www\n"
	if err := os.WriteFile(filepath.Join(famDir, "ready", suffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFamilyServing(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	for _, s := range []string{"meera", "voucher-meera", "draft-guide", "shop"} {
		a.deploy(t, olive, s)
	}
	a.deploy(t, oscar, "zed")
	uid, oh := a.userID(t, olive)
	markReady(t, siteDir, oh)
	okey := map[string]string{"X-API-Key": olive.key}
	ctx := context.Background()

	// Before anything is live, nothing answers under the family.
	if r := a.at(t, "GET", "meera.quotes.fam.test", "/", nil, nil); r.status == 200 {
		t.Fatalf("pending family served: %d %s", r.status, r.body)
	}
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 1)
	a.liveFamily(t, famDir, dns, olive, "voucher.fam.test", "voucher-", true, 0)

	get := func(host, path string) resp { return a.at(t, "GET", host, path, nil, nil) }
	for host, want := range map[string]string{
		"meera.quotes.fam.test":         "<h1>meera</h1>",
		"voucher-meera.quotes.fam.test": "<h1>voucher-meera</h1>",
		"draft-guide.quotes.fam.test":   "<h1>draft-guide</h1>",
		"meera.voucher.fam.test":        "<h1>voucher-meera</h1>",
	} {
		if r := get(host, "/"); r.status != 200 || string(r.body) != want {
			t.Errorf("%s: %d %q, want %q", host, r.status, r.body, want)
		}
	}
	if r := get("meera.quotes.fam.test", "/sub/"); r.status != 200 || string(r.body) != "sub" {
		t.Errorf("subdirectory: %d %s", r.status, r.body)
	}
	// Never another account's site, never a reserved or odd label.
	for _, host := range []string{"zed.quotes.fam.test", "www.quotes.fam.test", "xn--mera-1qa.quotes.fam.test", "nope.quotes.fam.test", "shop.voucher.fam.test", "-x.quotes.fam.test"} {
		if r := get(host, "/"); r.status == 200 {
			t.Errorf("%s served: %s", host, r.body)
		}
	}
	for _, p := range []string{"/../../etc/passwd", "/.env", "/sub/../../x"} {
		if r := get("meera.quotes.fam.test", p); r.status == 200 {
			t.Errorf("GET %s: %d", p, r.status)
		}
	}
	if r := a.at(t, "POST", "meera.quotes.fam.test", "/", "x", nil); r.status != http.StatusMethodNotAllowed {
		t.Errorf("POST a file: %d", r.status)
	}

	// The main address: the most specific canonical family (the voucher-
	// prefix beats none), handed out in the API and redirected to.
	r := a.at(t, "GET", pcSiteDomain, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.body), `"family_address":"https://meera.voucher.fam.test/"`) ||
		!strings.Contains(string(r.body), `"https://voucher-meera.quotes.fam.test/"`) {
		t.Fatalf("family addresses in the site list: %s", r.body)
	}
	siteHost := "voucher-meera." + oh + "." + pcSiteDomain
	if r := get(siteHost, "/sub/?x=1"); r.status != http.StatusFound || r.header.Get("Location") != "https://meera.voucher.fam.test/sub/?x=1" {
		t.Fatalf("site host -> family: %d %q", r.status, r.header.Get("Location"))
	}
	if r := get("meera."+oh+"."+pcSiteDomain, "/"); r.status != http.StatusFound || r.header.Get("Location") != "https://meera.quotes.fam.test/" {
		t.Fatalf("site host -> quotes family: %d %q", r.status, r.header.Get("Location"))
	}
	if m := a.redirectMarker(t, olive, "meera"); m != "meera.quotes.fam.test" {
		t.Fatalf("content-host marker = %q", m)
	}
	// Every family address keeps working, the main one or not.
	if r := get("voucher-meera.quotes.fam.test", "/"); r.status != 200 {
		t.Fatalf("second family address: %d", r.status)
	}

	// canonical off: the site host is the address again.
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"canonical": false}, okey); r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	if r := get("meera."+oh+"."+pcSiteDomain, "/"); r.status != 200 {
		t.Fatalf("canonical off: site host %d %q", r.status, r.header.Get("Location"))
	}
	if m := a.redirectMarker(t, olive, "meera"); m != "" {
		t.Fatalf("canonical off: marker %q", m)
	}
	if r := get("meera.quotes.fam.test", "/"); r.status != 200 {
		t.Fatalf("canonical off: family address %d", r.status)
	}
	a.at(t, "PATCH", pcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"canonical": true}, okey)

	// A prefix change waits for the issuer to serve it before it counts.
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": "draft-"}, okey); r.status != 200 || r.json(t)["live"] != false {
		t.Fatalf("prefix change live before the issuer: %d %s", r.status, r.body)
	}
	if r := get("guide.quotes.fam.test", "/"); r.status == 200 {
		t.Fatal("new prefix served before the issuer re-rendered")
	}
	markFamilyReady(t, famDir, "quotes.fam.test", "draft-")
	a.sites.refreshFamilies(ctx)
	if r := get("guide.quotes.fam.test", "/"); r.status != 200 || string(r.body) != "<h1>draft-guide</h1>" {
		t.Fatalf("prefix draft-: %d %s", r.status, r.body)
	}
	a.at(t, "PATCH", pcSiteDomain, "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": ""}, okey)
	markFamilyReady(t, famDir, "quotes.fam.test", "")
	a.sites.refreshFamilies(ctx)

	// Rename: the old label redirects to the new one; delete: gone; restore: back.
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/sites/meera", map[string]string{"name": "meera2"}, okey); r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	if r := get("meera.quotes.fam.test", "/x?y=1"); r.status != http.StatusFound || r.header.Get("Location") != "https://meera2.quotes.fam.test/x?y=1" {
		t.Fatalf("renamed: %d %q", r.status, r.header.Get("Location"))
	}
	if r := get("meera2.quotes.fam.test", "/"); r.status != 200 {
		t.Fatalf("new name: %d", r.status)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/draft-guide", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := get("draft-guide.quotes.fam.test", "/"); r.status != http.StatusNotFound {
		t.Fatalf("deleted site: %d", r.status)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/draft-guide/restore", nil, okey); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := get("draft-guide.quotes.fam.test", "/"); r.status != 200 {
		t.Fatalf("restored site: %d", r.status)
	}
	// A site created later answers at once.
	a.deploy(t, olive, "later")
	if r := get("later.quotes.fam.test", "/"); r.status != 200 {
		t.Fatalf("new site: %d", r.status)
	}
	if m := a.redirectMarker(t, olive, "later"); m != "later.quotes.fam.test" {
		t.Fatalf("new site marker = %q", m)
	}

	// Offline and take-down on the family address.
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/sites/shop", map[string]bool{"offline": true}, okey); r.status != 200 {
		t.Fatalf("offline: %d %s", r.status, r.body)
	}
	if r := get("shop.quotes.fam.test", "/"); r.status != http.StatusServiceUnavailable {
		t.Fatalf("offline on family: %d", r.status)
	}
	a.at(t, "PATCH", pcSiteDomain, "/v1/sites/shop", map[string]bool{"offline": false}, okey)

	// A domain of its own wins: the family addresses redirect there, and
	// the marker tells nginx.
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": "shop-" + oh + "." + pcSiteDomain}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := get("shop.quotes.fam.test", "/a?b=1"); r.status != http.StatusFound || r.header.Get("Location") != "https://shop-"+oh+"."+pcSiteDomain+"/a?b=1" {
		t.Fatalf("family -> own domain: %d %q", r.status, r.header.Get("Location"))
	}
	if b, err := os.ReadFile(filepath.Join(a.sites.disk.SiteDir(uid, "shop"), "lives-elsewhere")); err != nil || strings.TrimSpace(string(b)) != "shop-"+oh+"."+pcSiteDomain {
		t.Fatalf("lives-elsewhere marker: %q %v", b, err)
	}
	if r := a.at(t, "GET", "shop.quotes.fam.test", "/internal/family/a", nil, nil); r.status != http.StatusFound {
		// (nginx reaches this only through its own rewrite; the app answers it the same)
		t.Fatalf("/internal/family: %d", r.status)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("drop domain: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.SiteDir(uid, "shop"), "lives-elsewhere")); !os.IsNotExist(err) {
		t.Fatalf("lives-elsewhere marker stayed: %v", err)
	}

	// Oscar cannot take any of it; the family is exclusive once verified.
	if r := a.at(t, "POST", pcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.quotes.fam.test"}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict || r.json(t)["code"] != "domain_taken" {
		t.Fatalf("oscar on olive's family: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.x.quotes.fam.test"}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("oscar under olive's family: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": "*.fam.test"}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("oscar over olive's family: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/zed/domain", map[string]string{"domain": "zed.quotes.fam.test"}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict || r.json(t)["code"] != "domain_taken" {
		t.Fatalf("oscar's custom domain under olive's family: %d %s", r.status, r.body)
	}
	// Olive may put a custom domain of her own under her family: the exact
	// name wins.
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": "special.quotes.fam.test"}, okey); r.status != 200 {
		t.Fatalf("own custom domain under own family: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_verified_at = now(), domain_status = 'active' WHERE custom_domain = 'special.quotes.fam.test'`); err != nil {
		t.Fatal(err)
	}
	if m, ok, _ := a.sites.familySiteForHost(ctx, "special.quotes.fam.test"); ok {
		t.Fatalf("custom domain resolved as family site %s", m.Site.Name)
	}

	// Disconnect: nothing answers there any more, the site host is back.
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me/address-families/*.voucher.fam.test", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.status, r.body)
	}
	if r := get("meera.voucher.fam.test", "/"); r.status == 200 {
		t.Fatal("disconnected family still serves")
	}
	if a.sites.disk.FamilyLinkTarget("voucher.fam.test") != "" {
		t.Fatal("link left behind")
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "voucher.fam.test")); !os.IsNotExist(err) {
		t.Fatal("issuer request left behind")
	}
	if r := get(siteHost, "/"); r.status != http.StatusFound || r.header.Get("Location") != "https://voucher-meera.quotes.fam.test/" {
		t.Fatalf("after disconnect the next family is main: %d %q", r.status, r.header.Get("Location"))
	}
}

// The site API on a family address: the host names the site, the origin
// checks accept its own family addresses only, and saves need the page's
// own address.
func TestFamilyAPIAndOrigins(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "voucher-meera")
	a.deploy(t, olive, "meera")
	_, oh := a.userID(t, olive)
	markReady(t, siteDir, oh)
	a.liveFamily(t, famDir, dns, olive, "voucher.fam.test", "voucher-", true, 0)
	vm := a.siteID(t, olive, "voucher-meera")
	host := "meera.voucher.fam.test"

	// auth.js names the site by the first label: meera means voucher-meera here.
	for _, name := range []string{"meera", "voucher-meera"} {
		if id, err := a.sites.resolveSiteIDScoped(httptestRequest(host, "/v1/sites/"+name+"/state"), name); err != nil || id != vm {
			t.Errorf("resolve %s on %s: %q %v", name, host, id, err)
		}
	}
	if _, err := a.sites.resolveSiteIDScoped(httptestRequest(host, "/v1/sites/shop/state"), "shop"); err == nil {
		t.Error("another site resolved on a family address")
	}
	// A page on the family address may save; a page on a sibling may not.
	sess := a.session(t, olive, vm, host)
	if r := a.at(t, "PUT", host, "/v1/sites/meera/state", map[string]any{"n": 1}, browser(host, sess)); r.status != 200 {
		t.Fatalf("save on the family address: %d %s", r.status, r.body)
	}
	sib := "other.voucher.fam.test"
	if r := a.at(t, "PUT", host, "/v1/sites/meera/state", map[string]any{"n": 2}, browser(host, sess, "Origin", "https://"+sib, "Sec-Fetch-Site", "same-site")); r.status != http.StatusForbidden {
		t.Fatalf("sibling origin saved: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", host, "/v1/sites/meera/state", map[string]any{"n": 2}, browser(host, sess, "Origin", "http://"+host)); r.status != http.StatusForbidden {
		t.Fatalf("http origin saved: %d %s", r.status, r.body)
	}
	// The site host, no longer the address, takes no saves.
	siteHost := "voucher-meera." + oh + "." + pcSiteDomain
	sess2 := a.session(t, olive, vm, siteHost)
	if r := a.at(t, "PUT", siteHost, "/v1/sites/voucher-meera/state", map[string]any{"n": 3}, browser(siteHost, sess2)); r.status == 200 {
		t.Fatalf("site host saved while the family is the address: %d %s", r.status, r.body)
	}
	// Email sign-in: only from a page on this very host.
	r := a.at(t, "POST", host, "/v1/sites/meera/visitor/auth", map[string]string{"email": "v@example.com"},
		map[string]string{"Origin": "https://" + sib, "Sec-Fetch-Site": "same-site", "X-SH-CSRF": "1"})
	if r.status != http.StatusForbidden {
		t.Fatalf("email sign-in from a sibling: %d %s", r.status, r.body)
	}
	// The visitor cookie counts only as __Host- and same-origin here.
	if got := a.sites.sessionCookieFor(httptestRequestHeaders(host, map[string]string{"Cookie": visitorCookieHTTP + "=" + sess, "Sec-Fetch-Site": "same-origin", "X-Forwarded-Proto": "https"})); got != "" {
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
	a.withOAuth(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "meera")
	_, oh := a.userID(t, olive)
	markReady(t, siteDir, oh)
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	host := "meera.quotes.fam.test"
	returnTo := "https://" + host + "/page.html"
	state, nonce := a.signInStart(t, host, returnTo)
	loc := a.signInCallback(t, state, "visitor", host)
	r := a.at(t, "GET", host, loc.RequestURI(), nil, map[string]string{"Cookie": visitorNonceCookieHost + "=" + nonce, "X-Forwarded-For": "198.51.100.9"})
	if r.status != http.StatusFound || r.header.Get("Location") != returnTo {
		t.Fatalf("establish: %d %q %s", r.status, r.header.Get("Location"), r.body)
	}
	// DNS moved away: no sign-in returns there.
	dns.away[host] = "192.0.2.50"
	if r := a.at(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil); r.status == http.StatusFound {
		t.Fatalf("sign-in started for a host that points elsewhere: %d", r.status)
	}
	delete(dns.away, host)
	// Nor to another account's family-shaped name or a missing site.
	if r := a.at(t, "GET", "nope.quotes.fam.test", "/v1/visitor/oauth/google?return_to="+url.QueryEscape("https://nope.quotes.fam.test/"), nil, nil); r.status == http.StatusFound {
		t.Fatalf("sign-in for a missing site: %d", r.status)
	}
}

// Suffix and label rules, the certificate modes, the cap, and the custom
// domain side (simple-hack.app and *.x refused there).
func TestFamilyBindRules(t *testing.T) {
	a, _, _, _ := newFamilyApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	okey := map[string]string{"X-API-Key": olive.key}
	post := func(body map[string]any) resp {
		return a.at(t, "POST", pcSiteDomain, "/v1/me/address-families", body, okey)
	}
	for _, bad := range []string{"*." + pcSiteDomain, "x." + pcSiteDomain, "*.sites." + pcSiteDomain, "*.simple-hack.test", "*.a.simple-hack.test", "*.co.uk", "*.com", "*.bad_name.com", "*.xn--80ak6aa92e.com", "localhost", ""} {
		if r := post(map[string]any{"suffix": bad}); r.status != http.StatusBadRequest {
			t.Errorf("%q: %d %s", bad, r.status, r.body)
		}
	}
	if r := post(map[string]any{"suffix": "*.brand.test", "cert_mode": "per_host"}); r.status != http.StatusBadRequest || r.json(t)["code"] != "cert_mode_unavailable" {
		t.Errorf("per_host: %d %s", r.status, r.body)
	}
	for _, p := range []string{"-x", "a_b", strings.Repeat("a", 41), "a.b"} {
		if r := post(map[string]any{"suffix": "*.brand.test", "site_prefix": p}); r.status != http.StatusBadRequest {
			t.Errorf("prefix %q: %d", p, r.status)
		}
	}
	r := post(map[string]any{"suffix": "https://*.Brand.test/", "site_prefix": "voucher-"})
	if r.status != http.StatusCreated || r.json(t)["family"] != "*.brand.test" || r.json(t)["status"] != "pending" {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	j := r.json(t)
	if dnsRec, _ := j["dns"].(map[string]any); dnsRec["host"] != "*.brand.test" || dnsRec["value"] != "cname."+pcSiteDomain {
		t.Errorf("dns: %v", j["dns"])
	}
	if txt, _ := j["dns_txt"].(map[string]any); txt["host"] != "_simple-host.brand.test" || !strings.HasPrefix(txt["value"].(string), "sh-") {
		t.Errorf("txt: %v", j["dns_txt"])
	}
	if cert, _ := j["certificate"].(map[string]any); cert["status"] != "waiting_for_operator" {
		t.Errorf("certificate: %v", j["certificate"])
	}
	if r := post(map[string]any{"suffix": "brand.test"}); r.status != 200 {
		t.Errorf("again: %d %s", r.status, r.body)
	}
	func() {
		old := *config.Active()
		l := old
		l.FamiliesPerAccount = 1
		config.SetActive(l)
		defer config.SetActive(old)
		if r := post(map[string]any{"suffix": "*.other.test"}); r.status != http.StatusConflict || r.json(t)["code"] != "address_family_limit" {
			t.Errorf("cap: %d %s", r.status, r.body)
		}
	}()
	// The custom-domain side.
	for _, d := range []string{"simple-hack.test", "x.simple-hack.test"} {
		if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": d}, okey); r.status != http.StatusBadRequest {
			t.Errorf("custom domain %s: %d %s", d, r.status, r.body)
		}
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": "*.shop.test"}, okey); r.status != http.StatusBadRequest || r.json(t)["code"] != "use_address_family" {
		t.Errorf("*.x as a site domain: %d %s", r.status, r.body)
	}
	// Families off: refused with a clear code.
	a.sites.SetAddressFamilies("")
	if r := post(map[string]any{"suffix": "*.late.test"}); r.status != http.StatusConflict || r.json(t)["code"] != "address_families_unavailable" {
		t.Errorf("off: %d %s", r.status, r.body)
	}
}

// Verification, the first account to prove a name winning it, failing
// checks, the warning and the release; the proof exemption.
func TestFamilyLifecycle(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "meera")
	a.deploy(t, oscar, "zed")
	_, oh := a.userID(t, olive)
	markReady(t, siteDir, oh)
	ctx := context.Background()
	ours := a.sites.serverAddrs(ctx)

	mk := func(p person, suffix string) db.AddressFamily {
		r := a.at(t, "POST", pcSiteDomain, "/v1/me/address-families", map[string]string{"suffix": suffix}, map[string]string{"X-API-Key": p.key})
		if r.status != http.StatusCreated {
			t.Fatalf("connect %s: %d %s", suffix, r.status, r.body)
		}
		f, _ := db.GetFamilyByID(ctx, a.database, r.json(t)["id"].(string))
		return f
	}
	fo := mk(olive, "*.race.test")
	fs := mk(oscar, "*.race.test") // both may wait on it
	// No TXT yet: pending with the reason.
	a.sites.checkFamily(ctx, fo, ours)
	fo, _ = db.GetFamilyByID(ctx, a.database, fo.ID)
	if fo.VerifiedAt.Valid || !strings.Contains(fo.LastError, "_simple-host.race.test") {
		t.Fatalf("no TXT: %+v", fo)
	}
	// The wildcard pointing elsewhere is not enough either.
	dns.txt["_simple-host.race.test"] = fo.Token
	dns.away["*.race.test"] = "192.0.2.50"
	a.sites.checkFamily(ctx, fo, ours)
	fo, _ = db.GetFamilyByID(ctx, a.database, fo.ID)
	if fo.VerifiedAt.Valid || !strings.Contains(fo.LastError, "not this server") {
		t.Fatalf("wildcard elsewhere: %+v", fo)
	}
	delete(dns.away, "*.race.test")
	a.sites.checkFamily(ctx, fo, ours)
	fo, _ = db.GetFamilyByID(ctx, a.database, fo.ID)
	if !fo.VerifiedAt.Valid || fo.Status != "active" {
		t.Fatalf("verified: %+v", fo)
	}
	if _, err := db.GetFamilyByID(ctx, a.database, fs.ID); err == nil {
		t.Fatal("the other account's pending family was not let go")
	}

	// Failing: the clock starts; past the lapse it is let go.
	delete(dns.txt, "_simple-host.race.test")
	a.sites.checkFamily(ctx, fo, ours)
	fo, _ = db.GetFamilyByID(ctx, a.database, fo.ID)
	if fo.Status != "failing" || !fo.FailingSince.Valid || !fo.VerifiedAt.Valid {
		t.Fatalf("failing: %+v", fo)
	}
	if _, err := a.database.Exec(`UPDATE address_families SET failing_since = now() - interval '100 hours' WHERE id = $1`, fo.ID); err != nil {
		t.Fatal(err)
	}
	fo, _ = db.GetFamilyByID(ctx, a.database, fo.ID)
	a.sites.checkFamily(ctx, fo, ours)
	if _, err := db.GetFamilyByID(ctx, a.database, fo.ID); err == nil {
		t.Fatal("lapsed family kept")
	}
	if a.sites.disk.FamilyLinkTarget("race.test") != "" {
		t.Fatal("lapsed family's link kept")
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "race.test")); !os.IsNotExist(err) {
		t.Fatal("lapsed family's request kept")
	}

	// Proof exempt (admin): no TXT needed, the wildcard still is.
	r := a.at(t, "POST", pcSiteDomain, "/v1/admin/users/"+a.siteIDOwner(t, olive)+"/address-families",
		map[string]any{"suffix": "*.exempt.test", "proof_exempt": true, "cert_name": "exempt.test", "canonical": false},
		map[string]string{"X-API-Key": a.admin})
	if r.status != http.StatusCreated || r.json(t)["proof_exempt"] != true || r.json(t)["dns_txt"] != nil {
		t.Fatalf("admin create: %d %s", r.status, r.body)
	}
	fe, _ := db.GetFamilyByID(ctx, a.database, r.json(t)["id"].(string))
	a.sites.checkFamily(ctx, fe, ours)
	fe, _ = db.GetFamilyByID(ctx, a.database, fe.ID)
	if !fe.VerifiedAt.Valid {
		t.Fatalf("exempt family not verified: %+v", fe)
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "exempt.test")); err != nil {
		t.Fatalf("exempt family's request: %v", err)
	}
	// Owners cannot set the exemption or the certificate themselves.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/admin/address-families/"+fe.ID+"/proof-exempt", map[string]bool{"proof_exempt": false}, map[string]string{"X-API-Key": olive.key}); r.status == 200 {
		t.Fatalf("owner changed the proof exemption: %d", r.status)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/admin/address-families/"+fe.ID+"/cert-mode", map[string]string{"cert_mode": "per_host"}, map[string]string{"X-API-Key": a.admin}); r.status != http.StatusBadRequest {
		t.Fatalf("admin per_host: %d %s", r.status, r.body)
	}
	// Never proved: dropped after ADDRESS_FAMILY_UNPROVEN_HOURS.
	fp := mk(oscar, "*.late.test")
	if _, err := a.database.Exec(`UPDATE address_families SET bound_at = now() - interval '30 hours' WHERE id = $1`, fp.ID); err != nil {
		t.Fatal(err)
	}
	a.sites.checkFamilies(ctx)
	if _, err := db.GetFamilyByID(ctx, a.database, fp.ID); err == nil {
		t.Fatal("unproven family kept")
	}
	// The admin list shows every family with its owner.
	if r := a.at(t, "GET", pcSiteDomain, "/v1/admin/address-families", nil, map[string]string{"X-API-Key": a.admin}); r.status != 200 || !strings.Contains(string(r.body), "*.exempt.test") || !strings.Contains(string(r.body), olive.email) {
		t.Fatalf("admin list: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/admin/address-families", nil, map[string]string{"X-API-Key": olive.key}); r.status == 200 {
		t.Fatal("owner read the admin list")
	}
	_ = time.Now
}

// The passcode gate covers family addresses: the Go path, the nginx marker
// rewrite (/internal/passcode on the family host) and the unlock there.
func TestFamilyPasscode(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true })
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "meera")
	_, oh := a.userID(t, olive)
	markReady(t, siteDir, oh)
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	host := "meera.quotes.fam.test"
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/meera/lock", map[string]string{"passcode": "Trip-2026-x"}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	for _, p := range []string{"/", "/sub/", "/nope"} {
		if r := a.at(t, "GET", host, p, nil, nil); r.status != http.StatusUnauthorized || strings.Contains(string(r.body), "<h1>meera</h1>") {
			t.Errorf("family %s: %d", p, r.status)
		}
		if r := a.at(t, "GET", host, "/internal/passcode"+p, nil, nil); r.status != http.StatusUnauthorized || strings.Contains(string(r.body), "<h1>meera</h1>") {
			t.Errorf("marker rewrite %s: %d", p, r.status)
		}
	}
	form := url.Values{"passcode": {"Trip-2026-x"}, "next": {"/sub/"}}.Encode()
	r := a.at(t, "POST", host, "/v1/site-unlock", form, map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"})
	if r.status != http.StatusSeeOther {
		t.Fatalf("unlock: %d %s", r.status, r.body)
	}
	var cookie string
	for _, c := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "__Host-sh_pass_") {
			cookie = strings.SplitN(c, ";", 2)[0]
		}
	}
	if cookie == "" {
		t.Fatalf("no unlock cookie: %v", r.header.Values("Set-Cookie"))
	}
	if r := a.at(t, "GET", host, "/sub/", nil, map[string]string{"Cookie": cookie}); r.status != 200 || string(r.body) != "sub" {
		t.Fatalf("unlocked: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/internal/passcode/sub/", nil, map[string]string{"Cookie": cookie}); r.status != 200 {
		t.Fatalf("unlocked via marker: %d", r.status)
	}
	// The cookie is this host's only.
	a.deploy(t, olive, "other")
	if r := a.at(t, "GET", "meera."+oh+"."+pcSiteDomain, "/", nil, map[string]string{"Cookie": cookie}); r.status == 200 {
		t.Fatal("family unlock opened the site host")
	}
}

// Everything else that follows a site's addresses.
func TestFamilyElsewhere(t *testing.T) {
	a, siteDir, famDir, dns := newFamilyApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "meera")
	uid, oh := a.userID(t, olive)
	markReady(t, siteDir, oh)
	a.liveFamily(t, famDir, dns, olive, "quotes.fam.test", "", true, 0)
	ctx := context.Background()

	// The report form counts a family address as hosted here.
	rh := a.sites.ReportHandler().(*reportHandler)
	if ok, err := rh.hosted(ctx, "meera.quotes.fam.test"); !ok || err != nil {
		t.Errorf("report: family address not hosted: %v %v", ok, err)
	}
	if ok, _ := rh.hosted(ctx, "nope.quotes.fam.test"); ok {
		t.Error("report: missing site counted as hosted")
	}
	// The data download names the family.
	r := a.at(t, "GET", pcSiteDomain, "/v1/me/export.zip", nil, map[string]string{"X-API-Key": olive.key})
	found := false
	for _, body := range zipFiles(t, r.body) {
		found = found || strings.Contains(body, "*.quotes.fam.test")
	}
	if r.status != 200 || !found {
		t.Fatalf("export: %d", r.status)
	}
	// Deleting the account removes the link and the issuer request.
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me", map[string]string{"confirm": oh}, map[string]string{"X-API-Key": olive.key}); r.status/100 != 2 {
		t.Fatalf("delete account: %d %s", r.status, r.body)
	}
	if a.sites.disk.FamilyLinkTarget("quotes.fam.test") != "" {
		t.Fatal("erased account's family link kept")
	}
	if _, err := os.Stat(filepath.Join(famDir, "requests", "quotes.fam.test")); !os.IsNotExist(err) {
		t.Fatal("erased account's issuer request kept")
	}
	_ = uid
}
