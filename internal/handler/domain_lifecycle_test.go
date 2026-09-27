package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// The certificate hand-off with the root issuer, file by file.
func TestDomainCertProgress(t *testing.T) {
	h := &SiteHandler{}
	if st, _ := h.domainCertProgress("shop.example.com"); st != "issuing" {
		t.Fatalf("no hand-off dir: %q (the operator issues by hand)", st)
	}
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.SetDomainCerts(dir)
	const dom = "shop.example.com"
	if st, _ := h.domainCertProgress(dom); st != "issuing" {
		t.Fatalf("first check: %q", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); err != nil {
		t.Fatalf("no request written: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "failed", dom), []byte("CAA record forbids Let's Encrypt\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, why := h.domainCertProgress(dom); st != "failed" || !strings.Contains(why, "CAA record forbids") || strings.Contains(why, "more") {
		t.Fatalf("failed: %q %q", st, why)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready", dom), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := h.domainCertProgress(dom); st != "live" {
		t.Fatalf("ready: %q", st)
	}
	h.cancelDomainCert(dom)
	if _, err := os.Stat(filepath.Join(dir, "requests", dom)); !os.IsNotExist(err) {
		t.Fatalf("request not withdrawn: %v", err)
	}
	if p := h.domainCertFile("requests", "../x"); p != "" {
		t.Fatalf("path escape accepted: %q", p)
	}
}

type noticeMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *noticeMailer) SendSignInCode(string, string, string) error { return nil }
func (m *noticeMailer) SendNotice(to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+subject+"|"+text)
	return nil
}

func uniq(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (a *privateApp) domainInfo(t *testing.T, siteID string) db.SiteDomainInfo {
	t.Helper()
	info, _, err := db.GetSiteDomainInfo(context.Background(), a.database, siteID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func (a *privateApp) redirectMarker(t *testing.T, p person, site string) string {
	t.Helper()
	uid, _ := a.userID(t, p)
	b, err := os.ReadFile(filepath.Join(a.sites.disk.SiteDir(uid, site), "domain-redirect"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// verify stands in for a passing check (DNS here, certificate live, HTTPS 2xx).
func (a *privateApp) verify(t *testing.T, siteID, domain string) {
	t.Helper()
	info := a.domainInfo(t, siteID)
	a.sites.applyDomainCheck(context.Background(), db.BoundDomain{
		SiteID: siteID, Domain: domain, Status: info.Status, Verified: info.VerifiedAt.Valid, PreviousDomain: info.PreviousDomain,
	}, "active", "", "live")
}

// Connecting a custom domain keeps the site's working address until the new
// one is proven; then the old one redirects to it. Old content-host links
// follow only a proven domain.
func TestNewDomainWaitsUntilItWorks(t *testing.T) {
	a := newPersonApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, handle := a.userID(t, olive)
	shopID := a.siteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	free := uniq("olv") + "." + pcSiteDomain
	custom := uniq("shop") + ".example.test"

	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free}, key); r.status != http.StatusOK {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": custom}, key)
	body := r.json(t)
	if r.status != http.StatusOK || body["status"] != "pending" || body["previous_domain"] != free || body["certificate_status"] != "pending" {
		t.Fatalf("bind custom: %d %s", r.status, r.body)
	}
	// The free name still serves the site, and is still its home.
	if r := a.at(t, "GET", free, "/", nil, nil); r.status != http.StatusOK || !strings.Contains(string(r.body), "shop") {
		t.Fatalf("free name while pending: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", handle+"."+pcSiteDomain, "/shop/", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+free+"/" {
		t.Fatalf("person path while pending: %d %s", r.status, r.header.Get("Location"))
	}
	if m := a.redirectMarker(t, olive, "shop"); m != free {
		t.Fatalf("content-host marker while pending: %q", m)
	}
	// Another site cannot take the free name the site is still using.
	oscar := a.newPerson(t, "oscar")
	a.deploy(t, oscar, "other")
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/other/domain", map[string]string{"domain": free}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("stranger took the kept name: %d %s", r.status, r.body)
	}

	a.verify(t, shopID, custom)
	info := a.domainInfo(t, shopID)
	if info.Domain != custom || !info.VerifiedAt.Valid || info.PreviousDomain != "" {
		t.Fatalf("after verify: %+v", info)
	}
	if m := a.redirectMarker(t, olive, "shop"); m != custom {
		t.Fatalf("content-host marker after verify: %q", m)
	}
	if r := a.at(t, "GET", free, "/menu?x=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+custom+"/menu?x=1" {
		t.Fatalf("old free name after verify: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", pcSiteDomain, "/internal/domain-redirect/"+handle+"/shop/a", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+custom+"/a" {
		t.Fatalf("content-host redirect: %d %q", r.status, r.header.Get("Location"))
	}

	// Disconnecting: the site's own address again; the free name follows it.
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain", nil, key); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if m := a.redirectMarker(t, olive, "shop"); m != "" {
		t.Fatalf("marker after disconnect: %q", m)
	}
	if r := a.at(t, "GET", free, "/", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+handle+"."+pcSiteDomain+"/shop/" {
		t.Fatalf("old free name after disconnect: %d %q", r.status, r.header.Get("Location"))
	}
}

// A pending domain never gets the content-host redirect, and giving up on it
// (disconnect or expiry) hands the site back its earlier address.
func TestPendingDomainGivesBackTheEarlierAddress(t *testing.T) {
	a := newPersonApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	_, handle := a.userID(t, olive)
	shopID, blogID := a.siteID(t, olive, "shop"), a.siteID(t, olive, "blog")
	key := map[string]string{"X-API-Key": olive.key}
	ctx := context.Background()

	// No earlier address: pending means no marker, and the redirect route refuses.
	dom := uniq("blog") + ".example.test"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": dom}, key); r.status != http.StatusOK {
		t.Fatalf("bind: %d %s", r.status, r.body)
	}
	if m := a.redirectMarker(t, olive, "blog"); m != "" {
		t.Fatalf("marker for a pending domain: %q", m)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/internal/domain-redirect/"+handle+"/blog/", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("redirect to a pending domain: %d %q", r.status, r.header.Get("Location"))
	}

	// With an earlier address: disconnecting the pending one restores it.
	free := uniq("olv") + "." + pcSiteDomain
	a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free}, key)
	pending := uniq("shop") + ".example.test"
	a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": pending}, key)
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain", nil, key); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d", r.status)
	}
	if info := a.domainInfo(t, shopID); info.Domain != free || info.Status != "active" || !info.VerifiedAt.Valid {
		t.Fatalf("earlier address not restored: %+v", info)
	}

	// Expiry: not while DNS points here (certificate past pending), then back
	// to the earlier address.
	a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": pending}, key)
	if _, err := a.database.Exec(`UPDATE sites SET domain_bound_at = now() - interval '25 hours', domain_cert_status = 'issuing' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_bound_at = now() - interval '25 hours' WHERE id = $1`, blogID); err != nil {
		t.Fatal(err)
	}
	released, err := db.ReleaseExpiredDomains(ctx, a.database)
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
	if _, err := a.database.Exec(`UPDATE sites SET domain_cert_status = 'pending' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	released, err = db.ReleaseExpiredDomains(ctx, a.database)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range released {
		if r.SiteID == shopID {
			found = r.Domain == pending && r.PreviousDomain == free
		}
	}
	if !found {
		t.Fatalf("expiry did not report the restored address: %+v", released)
	}
	if info := a.domainInfo(t, shopID); info.Domain != free || !info.VerifiedAt.Valid {
		t.Fatalf("expiry did not restore the earlier address: %+v", info)
	}
}

// A verified domain that keeps failing: the owner is emailed once after a
// day; after three days it stops being the site's home and can be claimed by
// whoever holds it now.
func TestLapsedDomainLetsGo(t *testing.T) {
	a := newPersonApp(t, "canonical")
	mail := &noticeMailer{}
	a.sites.mailer = mail
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "mine")
	_, handle := a.userID(t, olive)
	shopID := a.siteID(t, olive, "shop")
	dom := uniq("shop") + ".example.test"
	a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": olive.key})
	a.verify(t, shopID, dom)
	failing := db.BoundDomain{SiteID: shopID, Domain: dom, Status: "active", Verified: true}
	ctx := context.Background()

	a.sites.applyDomainCheck(ctx, failing, "pending", "resolves to 192.0.2.9, not to this server", "pending")
	info := a.domainInfo(t, shopID)
	if !info.FailingSince.Valid || !info.VerifiedAt.Valid {
		t.Fatalf("failing clock not started: %+v", info)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/domain", nil, map[string]string{"X-API-Key": olive.key}); r.json(t)["failing_since"] == nil {
		t.Fatalf("failing_since not exposed: %s", r.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '25 hours' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	failing.Status = "pending"
	a.sites.applyDomainCheck(ctx, failing, "pending", "resolves to 192.0.2.9, not to this server", "pending")
	a.sites.applyDomainCheck(ctx, failing, "pending", "resolves to 192.0.2.9, not to this server", "pending")
	if len(mail.sent) != 1 || !strings.HasPrefix(mail.sent[0], olive.email+"|") || !strings.Contains(mail.sent[0], "192.0.2.9") {
		t.Fatalf("owner notices: %q", mail.sent)
	}
	// Still home until 72 h.
	if r := a.at(t, "GET", handle+"."+pcSiteDomain, "/shop/", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+dom+"/" {
		t.Fatalf("before lapse: %d %q", r.status, r.header.Get("Location"))
	}
	if _, err := a.database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.applyDomainCheck(ctx, failing, "pending", "resolves to 192.0.2.9, not to this server", "pending")
	if info := a.domainInfo(t, shopID); info.VerifiedAt.Valid {
		t.Fatalf("lapsed domain still verified: %+v", info)
	}
	if r := a.at(t, "GET", handle+"."+pcSiteDomain, "/shop/", nil, nil); r.status != http.StatusOK {
		t.Fatalf("site's own address after lapse: %d %q", r.status, r.header.Get("Location"))
	}
	if m := a.redirectMarker(t, olive, "shop"); m != "" {
		t.Fatalf("marker after lapse: %q", m)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/mine/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusOK {
		t.Fatalf("new holder cannot claim the lapsed domain: %d %s", r.status, r.body)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("extra notices: %q", mail.sent)
	}
}

// A released free name stays with its site (and outlives it); it never passes
// to the oldest stranger's site that happens to share the name.
func TestReleasedFreeNameStaysWithItsSite(t *testing.T) {
	a := newPersonApp(t, "canonical")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	label := uniq("brand")
	a.deploy(t, oscar, label) // older site with the same name: the old fallback's pick
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	_, handle := a.userID(t, olive)
	key := map[string]string{"X-API-Key": olive.key}
	free := label + "." + pcSiteDomain

	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free}, key); r.status != http.StatusOK {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain", nil, key); r.status != http.StatusNoContent {
		t.Fatalf("disconnect: %d", r.status)
	}
	if r := a.at(t, "GET", free, "/p?q=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+handle+"."+pcSiteDomain+"/shop/p?q=1" {
		t.Fatalf("released name: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/"+label+"/domain", map[string]string{"domain": free}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("stranger claimed a retired name: %d %s", r.status, r.body)
	}

	// Switching to another free name retires the first one too.
	other := uniq("olv") + "." + pcSiteDomain
	a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": other}, key)
	next := uniq("olv") + "." + pcSiteDomain
	a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": next}, key)
	if r := a.at(t, "GET", other, "/", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+next+"/" {
		t.Fatalf("switched-from name: %d %q", r.status, r.header.Get("Location"))
	}

	// The same account may reuse its retired name on another site.
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": free}, key); r.status != http.StatusOK {
		t.Fatalf("owner reclaim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", free, "/", nil, nil); r.status != http.StatusOK {
		t.Fatalf("reclaimed name does not serve: %d", r.status)
	}

	// Deleting the site: the name says the site was removed, and stays taken.
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/blog", nil, key); r.status != http.StatusNoContent {
		t.Fatalf("delete site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", free, "/", nil, nil); r.status != http.StatusNotFound || !strings.Contains(string(r.body), "removed") {
		t.Fatalf("name of a deleted site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/"+label+"/domain", map[string]string{"domain": free}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("stranger claimed a deleted site's name: %d %s", r.status, r.body)
	}
}
