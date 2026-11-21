package sitestest

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
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

// domainCertDir gives the app a custom-domain certificate hand-off.
func (a *App) domainCertDir(t *testing.T) string {
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

func (a *App) bind(t *testing.T, p rt.Person, site, domain string) rt.Resp {
	t.Helper()
	return a.api(t, p, "POST", "/v1/sites/"+site+"/domain", map[string]string{"domain": domain})
}

func (a *App) boundDomain(t *testing.T, siteID string) model.BoundDomain {
	t.Helper()
	return model.BoundDomainOf(a.domainInfo(t, siteID))
}

func location(r rt.Resp) string { return r.Header.Get("Location") }

// A custom domain is verified, and gets a certificate request, only with the
// site's TXT ownership record; the request names the token and the link.
func TestDomainNeedsTXTProof(t *testing.T) {
	a := NewApp(t)
	dir := a.domainCertDir(t)
	olive := a.NewPerson(t, "olive")
	shopID := a.Deploy(t, olive, "shop")
	dom := uniq("shop") + ".example.test"

	r := a.bind(t, olive, "shop", dom)
	txt, _ := r.JSON(t)["dns_txt"].(map[string]any)
	info := a.domainInfo(t, shopID)
	if r.Status != http.StatusOK || txt == nil || txt["host"] != "_simple-host."+dom || txt["value"] != info.Token || !strings.HasPrefix(info.Token, "sh-") {
		t.Fatalf("bind: %d %s (token %q)", r.Status, r.Body, info.Token)
	}

	// DNS points here, but no TXT (or another site's token): no request, not verified.
	oldTimeout := siteshttp.DomainProbeTimeout
	siteshttp.DomainProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { siteshttp.DomainProbeTimeout = oldTimeout })
	ours := map[string]bool{"203.0.113.7": true}
	for _, txt := range []map[string]string{nil, {"_simple-host." + dom: "sh-ffffffffffffffffffffffffffffffff"}} {
		fakeDNS(t, "203.0.113.7", txt)
		a.Sites.CheckDomain(context.Background(), a.boundDomain(t, shopID), ours)
		if info := a.domainInfo(t, shopID); info.VerifiedAt.Valid || info.CertStatus != "pending" {
			t.Fatalf("without the TXT: %+v", info)
		}
		if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
			t.Fatalf("request written without the ownership record: %v", err)
		}
	}

	// With the TXT: the certificate is asked for, naming token and link target.
	fakeDNS(t, "203.0.113.7", map[string]string{"_simple-host." + dom: info.Token})
	a.Sites.CheckDomain(context.Background(), a.boundDomain(t, shopID), ours)
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
	a.Sites.CheckDomain(context.Background(), bd, ours)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request kept for a taken-down site: %v", err)
	}
}

// Domains verified before the proof existed keep working without it while
// they stay verified; a lapse ends the exemption.
func TestGrandfatheredDomainKeepsWorking(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	oldID, newID := a.Deploy(t, olive, "old"), a.Deploy(t, olive, "new")
	oldDom, newDom := uniq("old")+".example.test", uniq("new")+".example.test"
	a.bind(t, olive, "old", oldDom)
	a.bind(t, olive, "new", newDom)
	a.verify(t, oldID, oldDom)
	a.verify(t, newID, newDom)
	if _, err := a.Database.Exec(`UPDATE sites SET domain_proof_exempt = ARRAY[custom_domain] WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	fakeDNS(t, "192.0.2.9", nil)
	a.Sites.CheckDomain(context.Background(), a.boundDomain(t, oldID), map[string]bool{})
	a.Sites.CheckDomain(context.Background(), a.boundDomain(t, newID), map[string]bool{})
	if info := a.domainInfo(t, oldID); !strings.Contains(info.LastError, "not to this server") || !info.ProofExempt {
		t.Fatalf("grandfathered: %+v", info)
	}
	if info := a.domainInfo(t, newID); !strings.Contains(info.LastError, "ownership record") || !info.FailingSince.Valid {
		t.Fatalf("TXT-proven domain without its record: %+v", info)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours' WHERE id = $1`, oldID); err != nil {
		t.Fatal(err)
	}
	a.Sites.ApplyDomainCheck(context.Background(), a.boundDomain(t, oldID), "pending", "x", "pending")
	var exempt []byte
	if err := a.Database.QueryRow(`SELECT domain_proof_exempt::text FROM sites WHERE id = $1`, oldID).Scan(&exempt); err != nil || string(exempt) != "{}" {
		t.Fatalf("exemption after lapse: %s %v", exempt, err)
	}
}

// The certificate hand-off with the root issuer, file by file, and its daily
// cap: five new certificates per account per day; retries do not count twice.
func TestDomainCertProgress(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	ctx := context.Background()
	d := model.BoundDomain{Domain: uniq("shop") + ".example.com", UserID: olive.ID, Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	if st, _ := a.Sites.DomainCertProgress(ctx, d); st != "issuing" {
		t.Fatalf("no hand-off dir: %q (the operator issues by hand)", st)
	}
	dir := a.domainCertDir(t)
	dom := d.Domain
	if st, _ := a.Sites.DomainCertProgress(ctx, d); st != "issuing" {
		t.Fatalf("first check: %q", st)
	}
	// An out-of-date request (the site was renamed) is brought up to date.
	d.Name = "shop2"
	a.Sites.DomainCertProgress(ctx, d)
	if b, _ := os.ReadFile(filepath.Join(dir, "requests", dom)); !strings.HasSuffix(string(b), "/shop2\n") {
		t.Fatalf("request not rewritten: %q", b)
	}
	if err := os.WriteFile(filepath.Join(dir, "failed", dom), []byte("CAA record forbids Let's Encrypt\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, why := a.Sites.DomainCertProgress(ctx, d); st != "failed" || !strings.Contains(why, "CAA record forbids") || strings.Contains(why, "more") {
		t.Fatalf("failed: %q %q", st, why)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready", dom), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Sites.DomainCertProgress(ctx, d); st != "live" {
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
		d.Domain = uniq("c") + ".example.test"
		for retry := 0; retry < 2; retry++ {
			if st, why := a.Sites.DomainCertProgress(ctx, d); st != "issuing" {
				t.Fatalf("request %d/%d: %s %s", i, retry, st, why)
			}
		}
	}
	d.Domain = uniq("over") + ".example.test"
	if st, _ := a.Sites.DomainCertProgress(ctx, d); st != "failed" {
		t.Fatalf("over the cap: %s", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", d.Domain)); !os.IsNotExist(err) {
		t.Fatalf("request written over the cap: %v", err)
	}
}

// "Check again" is limited per account, and a domain the issuer is still
// releasing cannot be connected until it has let go.
func TestCheckAgainLimitAndReleasing(t *testing.T) {
	a := NewApp(t)
	dir := a.domainCertDir(t)
	fakeDNS(t, "192.0.2.9", nil)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.bind(t, olive, "shop", uniq("shop")+".example.test")
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
	gone := uniq("gone") + ".example.test"
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
	a := NewApp(t)
	ctx := context.Background()
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shop, grab := a.Deploy(t, olive, "shop"), a.Deploy(t, oscar, "grab")
	apex := uniq("brand") + ".test"
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2 WHERE id = $1`, shop, apex); err != nil {
		t.Fatal(err)
	}
	d := model.BoundDomain{SiteID: shop, Domain: apex, UserID: "u", Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	if got := a.Sites.SitesSvc().RequestablePartner(ctx, d.Domain, d.SiteID); got != "www."+apex {
		t.Fatalf("free partner: %q", got)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2 WHERE id = $1`, grab, "www."+apex); err != nil {
		t.Fatal(err)
	}
	if got := a.Sites.SitesSvc().RequestablePartner(ctx, d.Domain, d.SiteID); got != "" {
		t.Fatalf("partner bound to another account's site still asked for: %q", got)
	}
	a.domainCertDir(t)
	if p := a.Sites.PartnerInfoFor(apex, shop); p != nil {
		t.Fatalf("partner offered: %+v", p)
	}
}

// Connecting a custom domain keeps the site's working address until the new
// one is proven; then the old one redirects to it. Disconnecting gives the
// site its own address back.
func TestNewDomainWaitsUntilItWorks(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetSiteHosts("off", "")
	olive := a.NewPerson(t, "olive")
	shopID := a.Deploy(t, olive, "shop")
	person := olive.Handle + "." + rt.SiteDomain
	free := uniq("olv") + "." + rt.SiteDomain
	custom := uniq("shop") + ".example.test"

	if r := a.bind(t, olive, "shop", free); r.Status != http.StatusOK {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	r := a.bind(t, olive, "shop", custom)
	if body := r.JSON(t); r.Status != http.StatusOK || body["status"] != "pending" || body["previous_domain"] != free {
		t.Fatalf("bind custom: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", free, "/", nil, nil); r.Status != http.StatusOK {
		t.Fatalf("free name while pending: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", person, "/shop/", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+free+"/" {
		t.Fatalf("person path while pending: %d %s", r.Status, location(r))
	}
	if m := a.redirectMarker(t, olive, "shop"); m != free {
		t.Fatalf("content-host marker while pending: %q", m)
	}
	oscar := a.NewPerson(t, "oscar")
	a.Deploy(t, oscar, "other")
	if r := a.bind(t, oscar, "other", free); r.Status != http.StatusConflict {
		t.Fatalf("stranger took the kept name: %d %s", r.Status, r.Body)
	}

	a.verify(t, shopID, custom)
	if info := a.domainInfo(t, shopID); info.Domain != custom || !info.VerifiedAt.Valid || info.PreviousDomain != "" {
		t.Fatalf("after verify: %+v", info)
	}
	if r := a.At(t, "GET", free, "/menu?x=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+custom+"/menu?x=1" {
		t.Fatalf("old free name after verify: %d %q", r.Status, location(r))
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/internal/domain-redirect/"+olive.Handle+"/shop/a", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+custom+"/a" {
		t.Fatalf("content-host redirect: %d %q", r.Status, location(r))
	}

	if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if m := a.redirectMarker(t, olive, "shop"); m != "" {
		t.Fatalf("marker after disconnect: %q", m)
	}
	if r := a.At(t, "GET", free, "/", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+person+"/shop/" {
		t.Fatalf("old free name after disconnect: %d %q", r.Status, location(r))
	}
}

// A pending domain never gets the content-host redirect, and giving up on it
// (disconnect or expiry) hands the site back its earlier address.
func TestPendingDomainGivesBackTheEarlierAddress(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	shopID, blogID := a.Deploy(t, olive, "shop"), a.Deploy(t, olive, "blog")
	ctx := context.Background()

	if r := a.bind(t, olive, "blog", uniq("blog")+".example.test"); r.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if m := a.redirectMarker(t, olive, "blog"); m != "" {
		t.Fatalf("marker for a pending domain: %q", m)
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/internal/domain-redirect/"+olive.Handle+"/blog/", nil, nil); r.Status != http.StatusNotFound {
		t.Fatalf("redirect to a pending domain: %d %q", r.Status, location(r))
	}

	free := uniq("olv") + "." + rt.SiteDomain
	pending := uniq("shop") + ".example.test"
	a.bind(t, olive, "shop", free)
	a.bind(t, olive, "shop", pending)
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Status)
	}
	if info := a.domainInfo(t, shopID); info.Domain != free || info.Status != "active" || !info.VerifiedAt.Valid {
		t.Fatalf("earlier address not restored: %+v", info)
	}

	// Expiry: not while the certificate is being issued; then back to the earlier address.
	a.bind(t, olive, "shop", pending)
	if _, err := a.Database.Exec(`UPDATE sites SET domain_bound_at = now() - interval '25 hours', domain_cert_status = 'issuing' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_bound_at = now() - interval '25 hours' WHERE id = $1`, blogID); err != nil {
		t.Fatal(err)
	}
	released, err := sitesrepo.ReleaseExpiredDomains(ctx, a.Database, 7*24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range released {
		if r.SiteID == shopID {
			t.Fatalf("released a binding whose certificate is being issued: %+v", r)
		}
	}
	if info := a.domainInfo(t, blogID); info.Domain != "" {
		t.Fatalf("unproven blog binding not released: %+v", info)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_cert_status = 'pending' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	if _, err := sitesrepo.ReleaseExpiredDomains(ctx, a.Database, 7*24*time.Hour, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if info := a.domainInfo(t, shopID); info.Domain != free || !info.VerifiedAt.Valid {
		t.Fatalf("expiry did not restore the earlier address: %+v", info)
	}
}

type noticeMailer struct{ sent []string }

func (m *noticeMailer) SendSignInCode(string, string, string) error { return nil }
func (m *noticeMailer) SendNotice(to, subject, text string) error {
	m.sent = append(m.sent, to+"|"+subject+"|"+text)
	return nil
}

// A verified domain that keeps failing: the owner is emailed once after a
// day; after three days it stops being the site's home and can be claimed by
// whoever holds it now.
func TestLapsedDomainLetsGo(t *testing.T) {
	mail := &noticeMailer{}
	a := NewAppMailer(t, mail)
	a.Hosts.SetSiteHosts("off", "")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shopID := a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "mine")
	person := olive.Handle + "." + rt.SiteDomain
	dom := uniq("shop") + ".example.test"
	a.bind(t, olive, "shop", dom)
	a.verify(t, shopID, dom)
	failing := model.BoundDomain{SiteID: shopID, Domain: dom, Status: "active", Verified: true}
	ctx := context.Background()
	const why = "resolves to 192.0.2.9, not to this server"

	a.Sites.ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	if info := a.domainInfo(t, shopID); !info.FailingSince.Valid || !info.VerifiedAt.Valid {
		t.Fatalf("failing clock not started: %+v", info)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '25 hours' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	failing.Status = "pending"
	a.Sites.ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	a.Sites.ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	if len(mail.sent) != 1 || !strings.HasPrefix(mail.sent[0], olive.Email+"|") {
		t.Fatalf("owner notices: %q", mail.sent)
	}
	if r := a.At(t, "GET", person, "/shop/", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+dom+"/" {
		t.Fatalf("before lapse: %d %q", r.Status, location(r))
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours', domain_release_at = now() - interval '1 hour' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.Sites.ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	if info, has, _ := sitesrepo.GetSiteDomainInfo(ctx, a.Database, shopID); has {
		t.Fatalf("lapsed domain still bound: %+v", info)
	}
	if _, err := os.Lstat(filepath.Join(a.Disk.DataDir(), "domains", dom)); !os.IsNotExist(err) {
		t.Fatalf("lapsed domain's link kept: %v", err)
	}
	if r := a.At(t, "GET", person, "/shop/", nil, nil); r.Status != http.StatusOK {
		t.Fatalf("site's own address after lapse: %d %q", r.Status, location(r))
	}
	if r := a.bind(t, oscar, "mine", dom); r.Status != http.StatusOK {
		t.Fatalf("new holder cannot claim the lapsed domain: %d %s", r.Status, r.Body)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("extra notices: %q", mail.sent)
	}
}

// A released free name stays with its site (and outlives it); it never passes
// to a stranger's site that happens to share the name.
func TestReleasedFreeNameStaysWithItsSite(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetSiteHosts("off", "")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	label := uniq("brand")
	a.Deploy(t, oscar, label) // older site with the same name: the old fallback's pick
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	free := label + "." + rt.SiteDomain

	if r := a.bind(t, olive, "shop", free); r.Status != http.StatusOK {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain", nil); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d", r.Status)
	}
	if r := a.At(t, "GET", free, "/p?q=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+olive.Handle+"."+rt.SiteDomain+"/shop/p?q=1" {
		t.Fatalf("released name: %d %q", r.Status, location(r))
	}
	if r := a.bind(t, oscar, label, free); r.Status != http.StatusConflict {
		t.Fatalf("stranger claimed a retired name: %d %s", r.Status, r.Body)
	}
	// The same account may reuse its retired name on another site.
	if r := a.bind(t, olive, "blog", free); r.Status != http.StatusOK {
		t.Fatalf("owner reclaim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", free, "/", nil, nil); r.Status != http.StatusOK {
		t.Fatalf("reclaimed name does not serve: %d", r.Status)
	}
	// Deleting the site: the name stays taken.
	if r := a.api(t, olive, "DELETE", "/v1/sites/blog", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", free, "/", nil, nil); r.Status != http.StatusNotFound {
		t.Fatalf("name of a deleted site: %d %s", r.Status, r.Body)
	}
	if r := a.bind(t, oscar, label, free); r.Status != http.StatusConflict {
		t.Fatalf("stranger claimed a deleted site's name: %d %s", r.Status, r.Body)
	}
}
