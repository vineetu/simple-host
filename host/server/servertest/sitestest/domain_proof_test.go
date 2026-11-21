package sitestest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

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

// A custom domain is verified, and gets a certificate request, only with the
// site's TXT ownership record; the request names the token and the link.
func TestDomainNeedsTXTProof(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.Hosts.SetDomainCertDir(dir)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	uid, _ := a.UserID(t, olive)
	shopID := a.SiteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.Key}
	dom := servertest.Uniq("shop") + ".example.test"

	r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, key)
	body := r.JSON(t)
	txt, _ := body["dns_txt"].(map[string]any)
	info := a.DomainInfo(t, shopID)
	if r.Status != http.StatusOK || txt == nil || txt["type"] != "TXT" || txt["host"] != "_simple-host."+dom || txt["value"] != info.Token || !strings.HasPrefix(info.Token, "sh-") {
		t.Fatalf("bind: %d %s (token %q)", r.Status, r.Body, info.Token)
	}
	list := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, key)
	var listed []struct {
		TXT map[string]any `json:"domain_dns_txt"`
	}
	if err := json.Unmarshal(list.Body, &listed); err != nil || len(listed) != 1 ||
		listed[0].TXT["host"] != "_simple-host."+dom || listed[0].TXT["type"] != "TXT" || listed[0].TXT["value"] != info.Token {
		t.Fatalf("site list lacks the TXT record: %s", list.Body)
	}

	// DNS points here, but no TXT: no request, not verified, the reason names the record.
	// (203.0.113.7 never answers: the HTTPS half fails after the short probe timeout.)
	oldTimeout := siteshttp.DomainProbeTimeout
	siteshttp.DomainProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { siteshttp.DomainProbeTimeout = oldTimeout })
	ours := map[string]bool{"203.0.113.7": true}
	fakeDNS(t, "203.0.113.7", nil)
	a.Sites.CheckDomain(context.Background(), a.BoundDomain(t, shopID), ours)
	info = a.DomainInfo(t, shopID)
	if info.VerifiedAt.Valid || info.CertStatus != "pending" || !strings.Contains(info.LastError, info.Token) {
		t.Fatalf("without TXT: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request written without the ownership record: %v", err)
	}
	// Another site's token proves nothing.
	fakeDNS(t, "203.0.113.7", map[string]string{"_simple-host." + dom: "sh-ffffffffffffffffffffffffffffffff"})
	a.Sites.CheckDomain(context.Background(), a.BoundDomain(t, shopID), ours)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request written for a wrong token: %v", err)
	}

	// With the TXT: the certificate is asked for, naming token and link target.
	fakeDNS(t, "203.0.113.7", map[string]string{"_simple-host." + dom: info.Token})
	a.Sites.CheckDomain(context.Background(), a.BoundDomain(t, shopID), ours)
	b, err := os.ReadFile(filepath.Join(dir, "requests", dom))
	if err != nil || string(b) != info.Token+"\n"+filepath.Join("..", "by-id", uid, "shop")+"\n" {
		t.Fatalf("request: %q %v", b, err)
	}
	if got := a.DomainInfo(t, shopID); got.CertStatus != "issuing" || got.VerifiedAt.Valid {
		t.Fatalf("with TXT, before HTTPS: %+v", got)
	}

	// Past its proof, the binding cannot be taken over by another account.
	oscar := a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "mine")
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/mine/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
		t.Fatalf("proven binding taken over: %d %s", r.Status, r.Body)
	}
	// A taken-down site gets no checks and its request is withdrawn.
	bd := a.BoundDomain(t, shopID)
	bd.Suspended = true
	a.Sites.CheckDomain(context.Background(), bd, ours)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request kept for a taken-down site: %v", err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET suspended_at = now(), suspended_reason = 'x' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	due, err := sitesrepo.ListDomainsToCheck(context.Background(), a.Database, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.SiteID == shopID && !d.Suspended {
			t.Fatalf("taken-down site listed as checkable: %+v", d)
		}
	}
}

// Domains verified before the proof existed keep working without it while
// they stay verified; a proven-by-TXT domain must keep its record.
func TestGrandfatheredDomainKeepsWorking(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "old")
	a.Deploy(t, olive, "new")
	key := map[string]string{"X-API-Key": olive.Key}
	oldID, newID := a.SiteID(t, olive, "old"), a.SiteID(t, olive, "new")
	oldDom, newDom := servertest.Uniq("old")+".example.test", servertest.Uniq("new")+".example.test"
	a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/old/domain", map[string]string{"domain": oldDom}, key)
	a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/new/domain", map[string]string{"domain": newDom}, key)
	a.Verify(t, oldID, oldDom)
	a.Verify(t, newID, newDom)
	if _, err := a.Database.Exec(`UPDATE sites SET domain_proof_exempt = ARRAY[custom_domain] WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	// Neither has a TXT record; both resolve elsewhere, so both fail the
	// HTTPS half. Only the grandfathered one gets past the ownership check.
	fakeDNS(t, "192.0.2.9", nil)
	a.Sites.CheckDomain(context.Background(), a.BoundDomain(t, oldID), map[string]bool{})
	a.Sites.CheckDomain(context.Background(), a.BoundDomain(t, newID), map[string]bool{})
	if info := a.DomainInfo(t, oldID); !strings.Contains(info.LastError, "not to this server") || !info.ProofExempt {
		t.Fatalf("grandfathered: %+v", info)
	}
	if info := a.DomainInfo(t, newID); !strings.Contains(info.LastError, "ownership record") || !info.FailingSince.Valid {
		t.Fatalf("TXT-proven domain without its record: %+v", info)
	}
	// Lapse ends the grandfathering.
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours' WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	a.Sites.ApplyDomainCheck(context.Background(), a.BoundDomain(t, oldID), "pending", "x", "pending")
	var exempt []byte
	if err := a.Database.QueryRow(`SELECT domain_proof_exempt::text FROM sites WHERE id = $1`, oldID).Scan(&exempt); err != nil || string(exempt) != "{}" {
		t.Fatalf("exemption after lapse: %s %v", exempt, err)
	}
}

// Five new certificates per account per day; retries of the same domain do
// not count twice.
func TestDomainCertDailyCap(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "requests"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.Sites.Hosts.SetDomainCertDir(dir)
	olive := a.NewPerson(t, "olive")
	uid, _ := a.UserID(t, olive)
	d := model.BoundDomain{UserID: uid, Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	for i := 0; i < 5; i++ {
		d.Domain = servertest.Uniq("c") + ".example.test"
		if st, why := a.Sites.DomainCertProgress(context.Background(), d); st != "issuing" {
			t.Fatalf("request %d: %s %s", i, st, why)
		}
		// Asked again (a retry): still fine, not counted again.
		if st, _ := a.Sites.DomainCertProgress(context.Background(), d); st != "issuing" {
			t.Fatalf("retry %d: %s", i, st)
		}
	}
	d.Domain = servertest.Uniq("over") + ".example.test"
	st, why := a.Sites.DomainCertProgress(context.Background(), d)
	if st != "failed" || !strings.Contains(why, "in the last day") {
		t.Fatalf("over the cap: %s %s", st, why)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", d.Domain)); !os.IsNotExist(err) {
		t.Fatalf("request written over the cap: %v", err)
	}
}

// "Check again" is limited per account, and a domain the issuer is still
// releasing cannot be connected until it has let go.
func TestCheckAgainLimitAndReleasing(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.Hosts.SetDomainCertDir(dir)
	fakeDNS(t, "192.0.2.9", nil)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.Key}
	dom := servertest.Uniq("shop") + ".example.test"
	a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, key)
	limited := false
	for i := 0; i < 6; i++ {
		// Different client IPs: the per-account limit is what bites.
		r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain/check", nil, map[string]string{"X-API-Key": olive.Key, "X-Forwarded-For": "198.51.100." + string(rune('1'+i))})
		if r.Status == http.StatusTooManyRequests {
			if r.JSON(t)["code"] != "rate_limited" {
				t.Fatalf("429 without code: %s", r.Body)
			}
			limited = true
			break
		}
		if r.Status != http.StatusOK {
			t.Fatalf("check %d: %d %s", i, r.Status, r.Body)
		}
	}
	if !limited {
		t.Fatal("Check again never limited per account")
	}

	gone := servertest.Uniq("gone") + ".example.test"
	if err := os.WriteFile(filepath.Join(dir, "ready", gone), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.Deploy(t, olive, "blog")
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": gone}, key); r.Status != http.StatusConflict || r.JSON(t)["code"] != "domain_releasing" {
		t.Fatalf("bound a domain still being released: %d %s", r.Status, r.Body)
	}
}
