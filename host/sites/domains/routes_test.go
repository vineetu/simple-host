package domains_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/domains"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// app is the fixture with the domain routes mounted.
type app struct{ *fixture.App }

func newApp(t *testing.T) app {
	t.Helper()
	a := fixture.New(t)
	domains.Register(a.Mux, a.H, a.Auth, a.Notice)
	domains.RegisterAdmin(a.Mux, a.H, a.Auth)
	return app{a}
}

func (a app) checks() domains.Handler { return domains.Handler{Handler: a.H} }

// fakeDNS stands in for the resolver: TXT records by name, and every host
// resolving to ip.
func fakeDNS(t *testing.T, ip string, txt map[string]string) {
	t.Helper()
	oldTXT, oldHost := sites.LookupTXT, sites.LookupHost
	t.Cleanup(func() { sites.LookupTXT, sites.LookupHost = oldTXT, oldHost })
	sites.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := txt[name]; ok {
			return []string{v}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	sites.LookupHost = func(_ context.Context, name string) ([]string, error) { return []string{ip}, nil }
}

// domainCertDir gives the app a custom-domain certificate hand-off.
func (a app) domainCertDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Hosts.SetDomainCertDir(dir)
	return dir
}

func (a app) bind(t *testing.T, p rt.Person, site, domain string) rt.Resp {
	t.Helper()
	return a.API(t, p, "POST", "/v1/sites/"+site+"/domain", map[string]string{"domain": domain})
}

func (a app) domainInfo(t *testing.T, siteID string) model.SiteDomainInfo {
	t.Helper()
	info, _, err := sitesrepo.GetSiteDomainInfo(context.Background(), a.Database, siteID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func (a app) boundDomain(t *testing.T, siteID string) model.BoundDomain {
	t.Helper()
	return model.BoundDomainOf(a.domainInfo(t, siteID))
}

// verify stands in for a passing domain check (DNS, certificate, HTTPS 2xx).
func (a app) verify(t *testing.T, siteID, domain string) {
	t.Helper()
	info := a.domainInfo(t, siteID)
	a.checks().ApplyDomainCheck(context.Background(), model.BoundDomain{
		SiteID: siteID, Domain: domain, Status: info.Status, Verified: info.VerifiedAt.Valid, PreviousDomain: info.PreviousDomain,
	}, "active", "", "live")
}

// A custom domain is verified, and gets a certificate request, only with the
// site's TXT ownership record; the request names the token and the link.
func TestDomainNeedsTXTProof(t *testing.T) {
	a := newApp(t)
	dir := a.domainCertDir(t)
	olive := a.NewPerson(t, "olive")
	shopID := a.Deploy(t, olive, "shop")
	dom := fixture.Uniq("shop") + ".example.test"

	r := a.bind(t, olive, "shop", dom)
	txt, _ := r.JSON(t)["dns_txt"].(map[string]any)
	info := a.domainInfo(t, shopID)
	if r.Status != http.StatusOK || txt == nil || txt["host"] != "_simple-host."+dom || txt["value"] != info.Token || !strings.HasPrefix(info.Token, "sh-") {
		t.Fatalf("bind: %d %s (token %q)", r.Status, r.Body, info.Token)
	}

	// DNS points here, but no TXT (or another site's token): no request, not verified.
	oldTimeout := sitesroute.DomainProbeTimeout
	sitesroute.DomainProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sitesroute.DomainProbeTimeout = oldTimeout })
	ours := map[string]bool{"203.0.113.7": true}
	for _, txt := range []map[string]string{nil, {"_simple-host." + dom: "sh-ffffffffffffffffffffffffffffffff"}} {
		fakeDNS(t, "203.0.113.7", txt)
		a.checks().CheckDomain(context.Background(), a.boundDomain(t, shopID), ours)
		if info := a.domainInfo(t, shopID); info.VerifiedAt.Valid || info.CertStatus != "pending" {
			t.Fatalf("without the TXT: %+v", info)
		}
		if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
			t.Fatalf("request written without the ownership record: %v", err)
		}
	}

	// With the TXT: the certificate is asked for, naming token and link target.
	fakeDNS(t, "203.0.113.7", map[string]string{"_simple-host." + dom: info.Token})
	a.checks().CheckDomain(context.Background(), a.boundDomain(t, shopID), ours)
	b, err := os.ReadFile(filepath.Join(dir, "requests", dom))
	if err != nil || string(b) != info.Token+"\n"+filepath.Join("..", "by-id", olive.ID, "shop")+"\n" {
		t.Fatalf("request: %q %v", b, err)
	}

	// Past its proof, the binding cannot be taken over by another account.
	oscar := a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "mine")
	if r := a.bind(t, oscar, "mine", dom); r.Status != http.StatusConflict {
		t.Fatalf("proven binding taken over: %d %s", r.Status, r.Body)
	}
	// A taken-down site gets no checks and its request is withdrawn.
	bd := a.boundDomain(t, shopID)
	bd.Suspended = true
	a.checks().CheckDomain(context.Background(), bd, ours)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request kept for a taken-down site: %v", err)
	}
}

// Domains verified before the proof existed keep working without it while
// they stay verified; a lapse ends the exemption.
func TestGrandfatheredDomainKeepsWorking(t *testing.T) {
	a := newApp(t)
	olive := a.NewPerson(t, "olive")
	oldID, newID := a.Deploy(t, olive, "old"), a.Deploy(t, olive, "new")
	oldDom, newDom := fixture.Uniq("old")+".example.test", fixture.Uniq("new")+".example.test"
	a.bind(t, olive, "old", oldDom)
	a.bind(t, olive, "new", newDom)
	a.verify(t, oldID, oldDom)
	a.verify(t, newID, newDom)
	if _, err := a.Database.Exec(`UPDATE sites SET domain_proof_exempt = ARRAY[custom_domain] WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	fakeDNS(t, "192.0.2.9", nil)
	a.checks().CheckDomain(context.Background(), a.boundDomain(t, oldID), map[string]bool{})
	a.checks().CheckDomain(context.Background(), a.boundDomain(t, newID), map[string]bool{})
	if info := a.domainInfo(t, oldID); !strings.Contains(info.LastError, "not to this server") || !info.ProofExempt {
		t.Fatalf("grandfathered: %+v", info)
	}
	if info := a.domainInfo(t, newID); !strings.Contains(info.LastError, "ownership record") || !info.FailingSince.Valid {
		t.Fatalf("TXT-proven domain without its record: %+v", info)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours' WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	a.checks().ApplyDomainCheck(context.Background(), a.boundDomain(t, oldID), "pending", "x", "pending")
	var exempt []byte
	if err := a.Database.QueryRow(`SELECT domain_proof_exempt::text FROM sites WHERE id = $1`, oldID).Scan(&exempt); err != nil || string(exempt) != "{}" {
		t.Fatalf("exemption after lapse: %s %v", exempt, err)
	}
}

// The certificate hand-off with the root issuer, file by file, and its daily
// cap: five new certificates per account per day; retries do not count twice.
func TestDomainCertProgress(t *testing.T) {
	a := newApp(t)
	olive := a.NewPerson(t, "olive")
	ctx := context.Background()
	d := model.BoundDomain{Domain: fixture.Uniq("shop") + ".example.com", UserID: olive.ID, Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	if st, _ := a.checks().DomainCertProgress(ctx, d); st != "issuing" {
		t.Fatalf("no hand-off dir: %q (the operator issues by hand)", st)
	}
	dir := a.domainCertDir(t)
	dom := d.Domain
	if st, _ := a.checks().DomainCertProgress(ctx, d); st != "issuing" {
		t.Fatalf("first check: %q", st)
	}
	// An out-of-date request (the site was renamed) is brought up to date.
	d.Name = "shop2"
	a.checks().DomainCertProgress(ctx, d)
	if b, _ := os.ReadFile(filepath.Join(dir, "requests", dom)); !strings.HasSuffix(string(b), "/shop2\n") {
		t.Fatalf("request not rewritten: %q", b)
	}
	if err := os.WriteFile(filepath.Join(dir, "failed", dom), []byte("CAA record forbids Let's Encrypt\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, why := a.checks().DomainCertProgress(ctx, d); st != "failed" || !strings.Contains(why, "CAA record forbids") || strings.Contains(why, "more") {
		t.Fatalf("failed: %q %q", st, why)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready", dom), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.checks().DomainCertProgress(ctx, d); st != "live" {
		t.Fatalf("ready: %q", st)
	}
	a.Hosts.CancelDomainCert(dom)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request not withdrawn: %v", err)
	}
	if p := a.Hosts.DomainCertFile("requests", "../x"); p != "" {
		t.Fatalf("path escape accepted: %q", p)
	}

	// The daily cap (the first domain above counted once).
	for i := 0; i < 4; i++ {
		d.Domain = fixture.Uniq("c") + ".example.test"
		for retry := 0; retry < 2; retry++ {
			if st, why := a.checks().DomainCertProgress(ctx, d); st != "issuing" {
				t.Fatalf("request %d/%d: %s %s", i, retry, st, why)
			}
		}
	}
	d.Domain = fixture.Uniq("over") + ".example.test"
	if st, _ := a.checks().DomainCertProgress(ctx, d); st != "failed" {
		t.Fatalf("over the cap: %s", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", d.Domain)); !os.IsNotExist(err) {
		t.Fatalf("request written over the cap: %v", err)
	}
}

// "Check again" is limited per account, and a domain the issuer is still
// releasing cannot be connected until it has let go.
func TestCheckAgainLimitAndReleasing(t *testing.T) {
	a := newApp(t)
	dir := a.domainCertDir(t)
	fakeDNS(t, "192.0.2.9", nil)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.bind(t, olive, "shop", fixture.Uniq("shop")+".example.test")
	limited := false
	for i := 0; i < 6 && !limited; i++ {
		// Different client IPs: the per-account limit is what bites.
		r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/shop/domain/check", nil, map[string]string{"X-API-Key": olive.Key, "X-Forwarded-For": "198.51.100." + string(rune('1'+i))})
		limited = r.Status == http.StatusTooManyRequests && r.Code() == "rate_limited"
		if !limited && r.Status != http.StatusOK {
			t.Fatalf("check %d: %d %s", i, r.Status, r.Body)
		}
	}
	if !limited {
		t.Fatal("Check again never limited per account")
	}
	gone := fixture.Uniq("gone") + ".example.test"
	if err := os.WriteFile(filepath.Join(dir, "ready", gone), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.Deploy(t, olive, "blog")
	if r := a.bind(t, olive, "blog", gone); r.Status != http.StatusConflict || r.Code() != "domain_releasing" {
		t.Fatalf("bound a domain still being released: %d %s", r.Status, r.Body)
	}
}

// A partner another account's site has bound is never asked for or offered.
func TestDomainPartnerBoundElsewhere(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shop, grab := a.Deploy(t, olive, "shop"), a.Deploy(t, oscar, "grab")
	apex := fixture.Uniq("brand") + ".test"
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2 WHERE id = $1`, shop, apex); err != nil {
		t.Fatal(err)
	}
	d := model.BoundDomain{SiteID: shop, Domain: apex, UserID: "u", Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	if got := (domains.Service{Service: a.H.SitesSvc()}).RequestablePartner(ctx, d.Domain, d.SiteID); got != "www."+apex {
		t.Fatalf("free partner: %q", got)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2 WHERE id = $1`, grab, "www."+apex); err != nil {
		t.Fatal(err)
	}
	if got := (domains.Service{Service: a.H.SitesSvc()}).RequestablePartner(ctx, d.Domain, d.SiteID); got != "" {
		t.Fatalf("partner bound to another account's site still asked for: %q", got)
	}
	a.domainCertDir(t)
	if p := a.H.PartnerInfoFor(apex, shop); p != nil {
		t.Fatalf("partner offered: %+v", p)
	}
}
