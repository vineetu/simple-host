package handler

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// fakeDNS stands in for the resolver: TXT records by name, and every host
// resolving to ip.
func fakeDNS(t *testing.T, ip string, txt map[string]string) {
	t.Helper()
	oldTXT, oldHost := lookupTXT, lookupHost
	t.Cleanup(func() { lookupTXT, lookupHost = oldTXT, oldHost })
	lookupTXT = func(_ context.Context, name string) ([]string, error) {
		if v, ok := txt[name]; ok {
			return []string{v}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	lookupHost = func(_ context.Context, name string) ([]string, error) { return []string{ip}, nil }
}

func (a *privateApp) boundDomain(t *testing.T, siteID string) db.BoundDomain {
	t.Helper()
	return db.BoundDomainOf(a.domainInfo(t, siteID))
}

// A custom domain is verified, and gets a certificate request, only with the
// site's TXT ownership record; the request names the token and the link.
func TestDomainNeedsTXTProof(t *testing.T) {
	a := newPersonApp(t, "canonical")
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sites.SetDomainCerts(dir)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	uid, _ := a.userID(t, olive)
	shopID := a.siteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	dom := uniq("shop") + ".example.test"

	r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, key)
	body := r.json(t)
	txt, _ := body["dns_txt"].(map[string]any)
	info := a.domainInfo(t, shopID)
	if r.status != http.StatusOK || txt == nil || txt["type"] != "TXT" || txt["host"] != "_simple-host."+dom || txt["value"] != info.Token || !strings.HasPrefix(info.Token, "sh-") {
		t.Fatalf("bind: %d %s (token %q)", r.status, r.body, info.Token)
	}
	list := a.at(t, "GET", pcSiteDomain, "/v1/sites", nil, key)
	if !strings.Contains(string(list.body), `"domain_dns_txt":{"host":"_simple-host.`+dom+`","type":"TXT","value":"`+info.Token+`"}`) {
		t.Fatalf("site list lacks the TXT record: %s", list.body)
	}

	// DNS points here, but no TXT: no request, not verified, the reason names the record.
	// (203.0.113.7 never answers: the HTTPS half fails after the short probe timeout.)
	oldTimeout := domainProbeTimeout
	domainProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { domainProbeTimeout = oldTimeout })
	ours := map[string]bool{"203.0.113.7": true}
	fakeDNS(t, "203.0.113.7", nil)
	a.sites.checkDomain(context.Background(), a.boundDomain(t, shopID), ours)
	info = a.domainInfo(t, shopID)
	if info.VerifiedAt.Valid || info.CertStatus != "pending" || !strings.Contains(info.LastError, info.Token) {
		t.Fatalf("without TXT: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request written without the ownership record: %v", err)
	}
	// Another site's token proves nothing.
	fakeDNS(t, "203.0.113.7", map[string]string{"_simple-host." + dom: "sh-ffffffffffffffffffffffffffffffff"})
	a.sites.checkDomain(context.Background(), a.boundDomain(t, shopID), ours)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request written for a wrong token: %v", err)
	}

	// With the TXT: the certificate is asked for, naming token and link target.
	fakeDNS(t, "203.0.113.7", map[string]string{"_simple-host." + dom: info.Token})
	a.sites.checkDomain(context.Background(), a.boundDomain(t, shopID), ours)
	b, err := os.ReadFile(filepath.Join(dir, "requests", dom))
	if err != nil || string(b) != info.Token+"\n"+filepath.Join("..", "by-id", uid, "shop")+"\n" {
		t.Fatalf("request: %q %v", b, err)
	}
	if got := a.domainInfo(t, shopID); got.CertStatus != "issuing" || got.VerifiedAt.Valid {
		t.Fatalf("with TXT, before HTTPS: %+v", got)
	}

	// Past its proof, the binding cannot be taken over by another account.
	oscar := a.newPerson(t, "oscar")
	a.deploy(t, oscar, "mine")
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/mine/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("proven binding taken over: %d %s", r.status, r.body)
	}
	// A taken-down site gets no checks and its request is withdrawn.
	bd := a.boundDomain(t, shopID)
	bd.Suspended = true
	a.sites.checkDomain(context.Background(), bd, ours)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request kept for a taken-down site: %v", err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET suspended_at = now(), suspended_reason = 'x' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	due, err := db.ListDomainsToCheck(context.Background(), a.database, time.Hour)
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
	a := newPersonApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "old")
	a.deploy(t, olive, "new")
	key := map[string]string{"X-API-Key": olive.key}
	oldID, newID := a.siteID(t, olive, "old"), a.siteID(t, olive, "new")
	oldDom, newDom := uniq("old")+".example.test", uniq("new")+".example.test"
	a.at(t, "POST", pcSiteDomain, "/v1/sites/old/domain", map[string]string{"domain": oldDom}, key)
	a.at(t, "POST", pcSiteDomain, "/v1/sites/new/domain", map[string]string{"domain": newDom}, key)
	a.verify(t, oldID, oldDom)
	a.verify(t, newID, newDom)
	if _, err := a.database.Exec(`UPDATE sites SET domain_proof_exempt = ARRAY[custom_domain] WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	// Neither has a TXT record; both resolve elsewhere, so both fail the
	// HTTPS half. Only the grandfathered one gets past the ownership check.
	fakeDNS(t, "192.0.2.9", nil)
	a.sites.checkDomain(context.Background(), a.boundDomain(t, oldID), map[string]bool{})
	a.sites.checkDomain(context.Background(), a.boundDomain(t, newID), map[string]bool{})
	if info := a.domainInfo(t, oldID); !strings.Contains(info.LastError, "not to this server") || !info.ProofExempt {
		t.Fatalf("grandfathered: %+v", info)
	}
	if info := a.domainInfo(t, newID); !strings.Contains(info.LastError, "ownership record") || !info.FailingSince.Valid {
		t.Fatalf("TXT-proven domain without its record: %+v", info)
	}
	// Lapse ends the grandfathering.
	if _, err := a.database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours' WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	a.sites.applyDomainCheck(context.Background(), a.boundDomain(t, oldID), "pending", "x", "pending")
	var exempt []byte
	if err := a.database.QueryRow(`SELECT domain_proof_exempt::text FROM sites WHERE id = $1`, oldID).Scan(&exempt); err != nil || string(exempt) != "{}" {
		t.Fatalf("exemption after lapse: %s %v", exempt, err)
	}
}

// Five new certificates per account per day; retries of the same domain do
// not count twice.
func TestDomainCertDailyCap(t *testing.T) {
	a := newPersonApp(t, "canonical")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "requests"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.sites.SetDomainCerts(dir)
	olive := a.newPerson(t, "olive")
	uid, _ := a.userID(t, olive)
	d := db.BoundDomain{UserID: uid, Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	for i := 0; i < db.DomainCertDailyCap(); i++ {
		d.Domain = uniq("c") + ".example.test"
		if st, why := a.sites.domainCertProgress(context.Background(), d); st != "issuing" {
			t.Fatalf("request %d: %s %s", i, st, why)
		}
		// Asked again (a retry): still fine, not counted again.
		if st, _ := a.sites.domainCertProgress(context.Background(), d); st != "issuing" {
			t.Fatalf("retry %d: %s", i, st)
		}
	}
	d.Domain = uniq("over") + ".example.test"
	st, why := a.sites.domainCertProgress(context.Background(), d)
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
	a := newPersonApp(t, "canonical")
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sites.SetDomainCerts(dir)
	fakeDNS(t, "192.0.2.9", nil)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	dom := uniq("shop") + ".example.test"
	a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, key)
	limited := false
	for i := 0; i < 6; i++ {
		// Different client IPs: the per-account limit is what bites.
		r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain/check", nil, map[string]string{"X-API-Key": olive.key, "X-Forwarded-For": "198.51.100." + string(rune('1'+i))})
		if r.status == http.StatusTooManyRequests {
			if r.json(t)["code"] != "rate_limited" {
				t.Fatalf("429 without code: %s", r.body)
			}
			limited = true
			break
		}
		if r.status != http.StatusOK {
			t.Fatalf("check %d: %d %s", i, r.status, r.body)
		}
	}
	if !limited {
		t.Fatal("Check again never limited per account")
	}

	gone := uniq("gone") + ".example.test"
	if err := os.WriteFile(filepath.Join(dir, "ready", gone), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.deploy(t, olive, "blog")
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": gone}, key); r.status != http.StatusConflict || r.json(t)["code"] != "domain_releasing" {
		t.Fatalf("bound a domain still being released: %d %s", r.status, r.body)
	}
}

// The status probe connects only to the address it was given and never
// follows a redirect; only public addresses count as this server.
func TestDomainProbePinnedNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/", http.StatusFound)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	res, err := domainProbeVia(host).Get("http://pinned.example.invalid:" + port + "/")
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("redirect followed: %d", res.StatusCode)
	}
	for ip, want := range map[string]bool{"203.0.113.7": true, "147.224.49.228": true, "127.0.0.1": false, "10.0.0.1": false, "169.254.169.254": false,
		"100.64.1.1": false, "::1": false, "fd00::1": false, "0.0.0.0": false, "2606:4700::1111": true, "::ffff:10.0.0.1": false, "bogus": false} {
		if got := isPublicIP(ip); got != want {
			t.Errorf("isPublicIP(%s) = %v", ip, got)
		}
	}
	// A domain resolving only to a private address is not "here".
	h := &SiteHandler{}
	old := lookupHost
	defer func() { lookupHost = old }()
	lookupHost = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	if st, _, here := h.verifyDomain(context.Background(), "x.example.test", map[string]bool{"127.0.0.1": true}); st != "pending" || here {
		t.Fatalf("private address counted as here: %s %v", st, here)
	}
	lookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("nx") }
	if st, _, _ := h.verifyDomain(context.Background(), "x.example.test", nil); st != "pending" {
		t.Fatalf("unresolvable: %s", st)
	}
}
