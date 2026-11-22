package sitestest

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sitefamilies "github.com/vsriram/simple-host/host/sites/families"

	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// Address families (*.<suffix> serving one account's sites).

const famIP = "203.0.113.7"

// familyDNS stands in for DNS: every name resolves to famIP (this server)
// unless listed in away; TXT records by name.
type familyDNS struct {
	txt  map[string]string
	away map[string]string // host (or "*.<suffix>") -> another address
}

func stubFamilyDNS(t *testing.T) *familyDNS {
	t.Helper()
	d := &familyDNS{txt: map[string]string{}, away: map[string]string{}}
	oldTXT, oldHost := sites.LookupTXT, sites.LookupHost
	t.Cleanup(func() { sites.LookupTXT, sites.LookupHost = oldTXT, oldHost })
	sites.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := d.txt[name]; ok {
			return []string{v}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	sites.LookupHost = func(_ context.Context, name string) ([]string, error) {
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

type familyApp struct {
	*App
	famDir string
	dns    *familyDNS
}

// families turns address families on for a, forgetting families and custom
// domains of earlier runs under the .test TLD (a verified family is exclusive).
func families(t *testing.T, a *App) *familyApp {
	t.Helper()
	famDir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(famDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.SetAddressFamilies(famDir)
	a.Hosts.SetPlatformZones(rt.SiteDomain, "simple-hack.test")
	if _, err := a.Database.Exec(`DELETE FROM address_families WHERE suffix LIKE '%.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = NULL, domain_status = NULL, domain_verified_at = NULL, previous_domain = NULL
		WHERE custom_domain LIKE '%.fam.test' OR custom_domain LIKE '%.block.test' OR custom_domain LIKE '%.proven.test'`); err != nil {
		t.Fatal(err)
	}
	return &familyApp{App: a, famDir: famDir, dns: stubFamilyDNS(t)}
}

func newFamilyApp(t *testing.T) *familyApp { return families(t, NewApp(t)) }

func (f *familyApp) connect(t *testing.T, p rt.Person, suffix string) rt.Resp {
	t.Helper()
	return f.api(t, p, "POST", "/v1/me/address-families", map[string]any{"suffix": suffix})
}

// markFamilyReady is the issuer's ready marker for suffix serving prefix.
func (f *familyApp) markFamilyReady(t *testing.T, suffix, prefix string) {
	t.Helper()
	body := "prefix=" + prefix + "\ncert=" + suffix + "\nexpires=1900000000\nreserved=www\n"
	if err := os.WriteFile(filepath.Join(f.famDir, "ready", suffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// liveFamily connects *.<suffix> for p and brings it all the way to live:
// the TXT record, the check, the operator's certificate and the issuer's
// ready marker.
func (f *familyApp) liveFamily(t *testing.T, p rt.Person, suffix, prefix string, canonical bool, rank int) model.AddressFamily {
	t.Helper()
	ctx := context.Background()
	r := f.api(t, p, "POST", "/v1/me/address-families", map[string]any{"suffix": "*." + suffix, "site_prefix": prefix, "canonical": canonical, "rank": rank})
	if r.Status != http.StatusCreated {
		t.Fatalf("connect *.%s: %d %s", suffix, r.Status, r.Body)
	}
	id := r.JSON(t)["id"].(string)
	fam, err := sitesrepo.GetFamilyByID(ctx, f.Database, id)
	if err != nil {
		t.Fatal(err)
	}
	f.dns.txt["_simple-host."+suffix] = fam.Token
	sitefamilies.Handler{Handler: f.Sites.Handler}.CheckFamily(ctx, fam, f.Hosts.ServerAddrs(ctx))
	if r := f.admin(t, "PUT", "/v1/admin/address-families/"+id+"/cert-mode", map[string]string{"cert_mode": "wildcard", "cert_name": suffix}); r.Status != 200 {
		t.Fatalf("cert: %d %s", r.Status, r.Body)
	}
	if fam, _ = sitesrepo.GetFamilyByID(ctx, f.Database, id); !fam.VerifiedAt.Valid {
		t.Fatalf("*.%s not verified: %+v", suffix, fam)
	}
	req, err := os.ReadFile(filepath.Join(f.famDir, "requests", suffix))
	if want := fam.Token + "\n../by-id/" + fam.UserID + "\nwildcard\n" + suffix + "\n" + prefix + "\nwww\n"; err != nil || string(req) != want {
		t.Fatalf("issuer request = %q (%v), want %q", req, err, want)
	}
	f.markFamilyReady(t, suffix, prefix)
	f.Hosts.RefreshFamilies(ctx)
	return fam
}

// A live family serves its account's sites by label (with its prefix), never
// another account's or a reserved label; the most specific canonical family
// is the main address; it follows renames, deletes, offline and an own
// domain, and stops at a disconnect.
func TestFamilyServing(t *testing.T) {
	f := newFamilyApp(t)
	olive, oscar := f.NewPerson(t, "olive"), f.NewPerson(t, "oscar")
	for _, s := range []string{"meera", "voucher-meera", "draft-guide", "shop"} {
		f.Deploy(t, olive, s)
	}
	f.Deploy(t, oscar, "zed")
	ctx := context.Background()
	get := func(host, path string) rt.Resp { return f.At(t, "GET", host, path, nil, nil) }
	if r := get("meera.quotes.fam.test", "/"); r.Status == 200 {
		t.Fatalf("pending family served: %d %s", r.Status, r.Body)
	}
	f.liveFamily(t, olive, "quotes.fam.test", "", true, 1)
	f.liveFamily(t, olive, "voucher.fam.test", "voucher-", true, 0)
	for host, want := range map[string]string{
		"meera.quotes.fam.test":         "<h1>meera</h1>",
		"voucher-meera.quotes.fam.test": "<h1>voucher-meera</h1>",
		"meera.voucher.fam.test":        "<h1>voucher-meera</h1>",
	} {
		if r := get(host, "/"); r.Status != 200 || string(r.Body) != want {
			t.Errorf("%s: %d %q, want %q", host, r.Status, r.Body, want)
		}
	}
	for _, host := range []string{"zed.quotes.fam.test", "www.quotes.fam.test", "nope.quotes.fam.test", "shop.voucher.fam.test", "-x.quotes.fam.test"} {
		if r := get(host, "/"); r.Status == 200 {
			t.Errorf("%s served: %s", host, r.Body)
		}
	}
	for _, p := range []string{"/../../etc/passwd", "/.env"} {
		if r := get("meera.quotes.fam.test", p); r.Status == 200 {
			t.Errorf("GET %s: %d", p, r.Status)
		}
	}

	// The main address: the most specific canonical family, handed out and redirected to.
	if r := f.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), `"family_address":"https://meera.voucher.fam.test/"`) {
		t.Fatalf("family addresses in the site list: %s", r.Body)
	}
	siteHost := olive.SiteHost("voucher-meera")
	if r := get(siteHost, "/sub/?x=1"); r.Status != http.StatusFound || location(r) != "https://meera.voucher.fam.test/sub/?x=1" {
		t.Fatalf("site host -> family: %d %q", r.Status, location(r))
	}
	if m := f.redirectMarker(t, olive, "meera"); m != "meera.quotes.fam.test" {
		t.Fatalf("content-host marker = %q", m)
	}
	if r := f.api(t, olive, "PATCH", "/v1/me/address-families/quotes.fam.test", map[string]any{"canonical": false}); r.Status != 200 {
		t.Fatalf("patch: %d %s", r.Status, r.Body)
	}
	if r := get(olive.SiteHost("meera"), "/"); r.Status != 200 || f.redirectMarker(t, olive, "meera") != "" {
		t.Fatalf("canonical off: site host %d %q", r.Status, location(r))
	}
	f.api(t, olive, "PATCH", "/v1/me/address-families/quotes.fam.test", map[string]any{"canonical": true})

	// A prefix change waits for the issuer to serve it.
	if r := f.api(t, olive, "PATCH", "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": "draft-"}); r.Status != 200 || r.JSON(t)["live"] != false {
		t.Fatalf("prefix change live before the issuer: %d %s", r.Status, r.Body)
	}
	if r := get("guide.quotes.fam.test", "/"); r.Status == 200 {
		t.Fatal("new prefix served before the issuer re-rendered")
	}
	f.markFamilyReady(t, "quotes.fam.test", "draft-")
	f.Hosts.RefreshFamilies(ctx)
	if r := get("guide.quotes.fam.test", "/"); r.Status != 200 || string(r.Body) != "<h1>draft-guide</h1>" {
		t.Fatalf("prefix draft-: %d %s", r.Status, r.Body)
	}
	f.api(t, olive, "PATCH", "/v1/me/address-families/quotes.fam.test", map[string]any{"site_prefix": ""})
	f.markFamilyReady(t, "quotes.fam.test", "")
	f.Hosts.RefreshFamilies(ctx)

	// Rename redirects, delete hides, a new site answers at once, offline holds.
	if r := f.api(t, olive, "PATCH", "/v1/sites/meera", map[string]string{"name": "meera2"}); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if r := get("meera.quotes.fam.test", "/x?y=1"); r.Status != http.StatusFound || location(r) != "https://meera2.quotes.fam.test/x?y=1" {
		t.Fatalf("renamed: %d %q", r.Status, location(r))
	}
	if r := f.api(t, olive, "DELETE", "/v1/sites/draft-guide", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := get("draft-guide.quotes.fam.test", "/"); r.Status != http.StatusNotFound {
		t.Fatalf("deleted site: %d", r.Status)
	}
	f.Deploy(t, olive, "later")
	if r := get("later.quotes.fam.test", "/"); r.Status != 200 {
		t.Fatalf("new site: %d", r.Status)
	}
	if r := f.api(t, olive, "PATCH", "/v1/sites/shop", map[string]bool{"offline": true}); r.Status != 200 {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	if r := get("shop.quotes.fam.test", "/"); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("offline on family: %d", r.Status)
	}
	f.api(t, olive, "PATCH", "/v1/sites/shop", map[string]bool{"offline": false})

	// A domain of its own wins, and the marker tells nginx.
	own := fixture.Uniq("shop") + "." + rt.SiteDomain
	if r := f.bind(t, olive, "shop", own); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := get("shop.quotes.fam.test", "/a?b=1"); r.Status != http.StatusFound || location(r) != "https://"+own+"/a?b=1" {
		t.Fatalf("family -> own domain: %d %q", r.Status, location(r))
	}
	if b, err := os.ReadFile(filepath.Join(f.siteDir(olive, "shop"), "lives-elsewhere")); err != nil || strings.TrimSpace(string(b)) != own {
		t.Fatalf("lives-elsewhere marker: %q %v", b, err)
	}
	if r := f.api(t, olive, "DELETE", "/v1/sites/shop/domain", nil); r.Status != http.StatusNoContent {
		t.Fatalf("drop domain: %d %s", r.Status, r.Body)
	}

	// Oscar cannot take any of it; the family is exclusive once verified.
	for _, suffix := range []string{"*.quotes.fam.test", "*.x.quotes.fam.test", "*.fam.test"} {
		if r := f.connect(t, oscar, suffix); r.Status != http.StatusConflict {
			t.Fatalf("oscar on %s: %d %s", suffix, r.Status, r.Body)
		}
	}
	if r := f.bind(t, oscar, "zed", "zed.quotes.fam.test"); r.Status != http.StatusConflict || r.Code() != "domain_taken" {
		t.Fatalf("oscar's custom domain under olive's family: %d %s", r.Status, r.Body)
	}

	// Disconnect: nothing answers there, link and request gone, the next family is main.
	if r := f.api(t, olive, "DELETE", "/v1/me/address-families/*.voucher.fam.test", nil); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	if r := get("meera.voucher.fam.test", "/"); r.Status == 200 {
		t.Fatal("disconnected family still serves")
	}
	if f.Disk.FamilyLinkTarget("voucher.fam.test") != "" {
		t.Fatal("link left behind")
	}
	if _, err := os.Stat(filepath.Join(f.famDir, "requests", "voucher.fam.test")); !os.IsNotExist(err) {
		t.Fatal("issuer request left behind")
	}
	if r := get(siteHost, "/"); r.Status != http.StatusFound || location(r) != "https://voucher-meera.quotes.fam.test/" {
		t.Fatalf("after disconnect the next family is main: %d %q", r.Status, location(r))
	}
}

// Suffix and label rules, the certificate modes, the cap, and the custom
// domain side (platform zones and *.x refused there).
func TestFamilyBindRules(t *testing.T) {
	f := newFamilyApp(t)
	olive := f.NewPerson(t, "olive")
	f.Deploy(t, olive, "shop")
	post := func(body map[string]any) rt.Resp {
		return f.api(t, olive, "POST", "/v1/me/address-families", body)
	}
	for _, bad := range []string{"*." + rt.SiteDomain, "*.sites." + rt.SiteDomain, "*.a.simple-hack.test", "*.co.uk", "*.com", "*.bad_name.com", "localhost", ""} {
		if r := post(map[string]any{"suffix": bad}); r.Status != http.StatusBadRequest {
			t.Errorf("%q: %d %s", bad, r.Status, r.Body)
		}
	}
	if r := post(map[string]any{"suffix": "*.brand.test", "cert_mode": "per_host"}); r.Status != http.StatusBadRequest || r.Code() != "cert_mode_unavailable" {
		t.Errorf("per_host: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"-x", "a.b"} {
		if r := post(map[string]any{"suffix": "*.brand.test", "site_prefix": p}); r.Status != http.StatusBadRequest {
			t.Errorf("prefix %q: %d", p, r.Status)
		}
	}
	r := post(map[string]any{"suffix": "https://*.Brand.test/", "site_prefix": "voucher-"})
	j := r.JSON(t)
	if r.Status != http.StatusCreated || j["family"] != "*.brand.test" || j["status"] != "pending" {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	if dnsRec, _ := j["dns"].(map[string]any); dnsRec["host"] != "*.brand.test" || dnsRec["value"] != "cname."+rt.SiteDomain {
		t.Errorf("dns: %v", j["dns"])
	}
	if txt, _ := j["dns_txt"].(map[string]any); txt["host"] != "_simple-host.brand.test" {
		t.Errorf("txt: %v", j["dns_txt"])
	}
	if r := post(map[string]any{"suffix": "brand.test"}); r.Status != 200 {
		t.Errorf("again: %d %s", r.Status, r.Body)
	}
	f.Sites.Limits.FamiliesPerAccount = 1
	if r := post(map[string]any{"suffix": "*.other.test"}); r.Status != http.StatusConflict || r.Code() != "address_family_limit" {
		t.Errorf("cap: %d %s", r.Status, r.Body)
	}
	for _, d := range []string{"simple-hack.test", "x.simple-hack.test"} {
		if r := f.bind(t, olive, "shop", d); r.Status != http.StatusBadRequest {
			t.Errorf("custom domain %s: %d %s", d, r.Status, r.Body)
		}
	}
	if r := f.bind(t, olive, "shop", "*.shop.test"); r.Status != http.StatusBadRequest || r.Code() != "use_address_family" {
		t.Errorf("*.x as a site domain: %d %s", r.Status, r.Body)
	}
	f.Sites.SetAddressFamilies("")
	if r := post(map[string]any{"suffix": "*.late.test"}); r.Status != http.StatusConflict || r.Code() != "address_families_unavailable" {
		t.Errorf("off: %d %s", r.Status, r.Body)
	}
}

// A protected, offline or taken-down site is never served on its family
// address, whether reached directly or through nginx's marker rewrite.
func TestFamilyGates(t *testing.T) {
	f := newFamilyApp(t)
	olive := f.NewPerson(t, "olive")
	f.Deploy(t, olive, "meera")
	f.Deploy(t, olive, "calm")
	f.Deploy(t, olive, "gone")
	f.liveFamily(t, olive, "quotes.fam.test", "", true, 0)
	f.protect(t, olive, "meera")
	f.offline(t, olive, "calm")
	f.suspend(t, olive, "gone")
	for site, want := range map[string]int{"meera": 403, "calm": 503, "gone": 410} {
		for _, p := range []string{"/", "/sub/", "/internal/passcode/", "/internal/family/"} {
			if r := f.At(t, "GET", site+".quotes.fam.test", p, nil, nil); r.Status != want || strings.Contains(string(r.Body), "<h1>") {
				t.Errorf("%s%s: %d, want %d", site, p, r.Status, want)
			}
		}
	}
}

// The report form counts a family address as hosted here.
func TestFamilyReportHosted(t *testing.T) {
	f := newFamilyApp(t)
	olive := f.NewPerson(t, "olive")
	f.Deploy(t, olive, "meera")
	f.liveFamily(t, olive, "quotes.fam.test", "", true, 0)
	rh := f.Sites.ReportHandler().(*siteshttp.ReportHandler)
	ctx := context.Background()
	if ok, err := rh.Hosted(ctx, "meera.quotes.fam.test"); !ok || err != nil {
		t.Errorf("family address not hosted: %v %v", ok, err)
	}
	if ok, _ := rh.Hosted(ctx, "nope.quotes.fam.test"); ok {
		t.Error("missing site counted as hosted")
	}
}

// Idle cleanup keeps a site whose main address is a verified family of its
// account (IDLE_EXEMPT_FAMILY_SITES), and not one outside the family.
func TestFamilyIdleExempt(t *testing.T) {
	f := newFamilyApp(t)
	ctx := context.Background()
	f.Sites.SetIdleCleanup(true)
	f.Sites.SetIdleExempt(nil, "")
	olive := f.NewPerson(t, "olive")
	for _, s := range []string{"voucher-meera", "quiet"} {
		id := f.Deploy(t, olive, s)
		if _, err := f.Database.ExecContext(ctx, `UPDATE sites SET created_at = now() - interval '130 days', updated_at = now() - interval '130 days' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Database.ExecContext(ctx, `UPDATE versions SET created_at = now() - interval '130 days' WHERE site_id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Database.ExecContext(ctx, `INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Database.ExecContext(ctx, `INSERT INTO address_families (user_id, suffix, site_prefix, verified_at, status) VALUES ($1, $2, 'voucher-', now(), 'active')`, olive.ID, "idle-"+strings.ToLower(olive.ID[:8])+".test"); err != nil {
		t.Fatal(err)
	}
	if r := f.admin(t, "GET", "/v1/admin/idle-sites", nil); r.Status != 200 || strings.Contains(string(r.Body), `"site":"voucher-meera"`) || !strings.Contains(string(r.Body), `"site":"quiet"`) {
		t.Fatalf("idle list: %d %s", r.Status, r.Body)
	}
	f.Sites.Limits.IdleExemptFamilySites = false
	f.Sites.SetIdleExempt(nil, "")
	if r := f.admin(t, "GET", "/v1/admin/idle-sites", nil); !strings.Contains(string(r.Body), `"site":"voucher-meera"`) {
		t.Fatalf("exemption off: %s", r.Body)
	}
}

// A stranger's pending custom domain under a name never holds a family off
// (anyone can bind one without proof); a proven one does.
func TestFamilyPendingDomainDoesNotBlock(t *testing.T) {
	f := newFamilyApp(t)
	olive, oscar := f.NewPerson(t, "olive"), f.NewPerson(t, "oscar")
	f.Deploy(t, oscar, "zed")
	f.Deploy(t, oscar, "yak")
	ctx := context.Background()
	if r := f.bind(t, oscar, "zed", "zed.block.test"); r.Status != 200 {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	r := f.connect(t, olive, "*.block.test")
	if r.Status != http.StatusCreated {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	fam, _ := sitesrepo.GetFamilyByID(ctx, f.Database, r.JSON(t)["id"].(string))
	f.dns.txt["_simple-host.block.test"] = fam.Token
	sitefamilies.Handler{Handler: f.Sites.Handler}.CheckFamily(ctx, fam, f.Hosts.ServerAddrs(ctx))
	if fam, _ = sitesrepo.GetFamilyByID(ctx, f.Database, fam.ID); !fam.VerifiedAt.Valid {
		t.Fatalf("a pending domain held the family off: %+v", fam)
	}
	if r := f.bind(t, oscar, "yak", "yak.proven.test"); r.Status != 200 {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if _, err := f.Database.Exec(`UPDATE sites SET domain_verified_at = now(), domain_status = 'active' WHERE custom_domain = 'yak.proven.test'`); err != nil {
		t.Fatal(err)
	}
	if r := f.connect(t, olive, "*.proven.test"); r.Status != http.StatusConflict {
		t.Fatalf("a proven domain did not hold the name: %d %s", r.Status, r.Body)
	}
}

// A host under a connected family is never handed to the platform's own
// pages, whatever the family's state; "live" in the API follows the routing
// index.
func TestFamilyHostNeverPlatform(t *testing.T) {
	f := newFamilyApp(t)
	f.Mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("PLATFORM-HOME")) })
	olive := f.NewPerson(t, "olive")
	f.Deploy(t, olive, "meera")
	ctx := context.Background()
	if r := f.At(t, "GET", "nope.other.test", "/", nil, nil); string(r.Body) != "PLATFORM-HOME" {
		t.Fatalf("control: %d %s", r.Status, r.Body)
	}
	plain404 := func(stage, host string) {
		t.Helper()
		for _, p := range []string{"/", "/x/y"} {
			if r := f.At(t, "GET", host, p, nil, nil); r.Status != http.StatusNotFound || strings.Contains(string(r.Body), "PLATFORM") {
				t.Errorf("%s: %s%s = %d %q, want a plain 404", stage, host, p, r.Status, r.Body)
			}
		}
	}
	liveOf := func(suffix string) bool {
		t.Helper()
		for _, x := range f.admin(t, "GET", "/v1/admin/address-families", nil).JSON(t)["address_families"].([]any) {
			if m := x.(map[string]any); m["suffix"] == suffix {
				return m["live"].(bool)
			}
		}
		return false
	}
	// Pending and unproven: anyone may connect any name, so it changes nothing.
	if r := f.connect(t, olive, "*.pend.fam.test"); r.Status != http.StatusCreated {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	if r := f.At(t, "GET", "nosuch.pend.fam.test", "/", nil, nil); string(r.Body) != "PLATFORM-HOME" {
		t.Errorf("an unproven family changed its hosts: %d %s", r.Status, r.Body)
	}
	// Pending but proof-exempt: the family's, a plain 404.
	if r := f.admin(t, "POST", "/v1/admin/users/"+olive.ID+"/address-families", map[string]any{"suffix": "*.exempt.fam.test", "proof_exempt": true, "canonical": false}); r.Status != http.StatusCreated {
		t.Fatalf("exempt: %d %s", r.Status, r.Body)
	}
	plain404("pending, proof-exempt", "nosuch.exempt.fam.test")

	fam := f.liveFamily(t, olive, "trips.fam.test", "", false, 2)
	if !liveOf("trips.fam.test") {
		t.Fatal("not live")
	}
	plain404("live, missing label", "nosuch.trips.fam.test")
	plain404("live, reserved label", "www.trips.fam.test")
	// The ready marker gone: live until the index refreshes, then not, and
	// nothing reaches the platform.
	if err := os.Remove(filepath.Join(f.famDir, "ready", "trips.fam.test")); err != nil {
		t.Fatal(err)
	}
	if !liveOf("trips.fam.test") {
		t.Error("live must follow the routing index, not the disk")
	}
	f.Hosts.RefreshFamilies(ctx)
	if liveOf("trips.fam.test") {
		t.Error("live after the refresh")
	}
	plain404("verified, not live", "meera.trips.fam.test")
	f.markFamilyReady(t, "trips.fam.test", "")
	if r := f.admin(t, "POST", "/v1/admin/address-families/"+fam.ID+"/check", nil); r.Status != 200 || r.JSON(t)["live"] != true {
		t.Fatalf("check: %d %s", r.Status, r.Body)
	}
	if r := f.admin(t, "DELETE", "/v1/admin/address-families/"+fam.ID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("admin disconnect: %d %s", r.Status, r.Body)
	}
	f.Hosts.RefreshFamilies(ctx)
	plain404("released", "meera.trips.fam.test")
}
