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
	"github.com/vsriram/simple-host/host/sites/domains"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
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

func location(r rt.Resp) string { return r.Header.Get("Location") }

// Connecting a custom domain keeps the site's working address until the new
// one is proven; then the old one redirects to it. Disconnecting gives the
// site its own address back.
func TestNewDomainWaitsUntilItWorks(t *testing.T) {
	a := NewApp(t)
	a.Hosts.SetSiteHosts("off", "")
	olive := a.NewPerson(t, "olive")
	shopID := a.Deploy(t, olive, "shop")
	person := olive.Handle + "." + rt.SiteDomain
	free := fixture.Uniq("olv") + "." + rt.SiteDomain
	custom := fixture.Uniq("shop") + ".example.test"

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

	if r := a.bind(t, olive, "blog", fixture.Uniq("blog")+".example.test"); r.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if m := a.redirectMarker(t, olive, "blog"); m != "" {
		t.Fatalf("marker for a pending domain: %q", m)
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/internal/domain-redirect/"+olive.Handle+"/blog/", nil, nil); r.Status != http.StatusNotFound {
		t.Fatalf("redirect to a pending domain: %d %q", r.Status, location(r))
	}

	free := fixture.Uniq("olv") + "." + rt.SiteDomain
	pending := fixture.Uniq("shop") + ".example.test"
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
	dom := fixture.Uniq("shop") + ".example.test"
	a.bind(t, olive, "shop", dom)
	a.verify(t, shopID, dom)
	failing := model.BoundDomain{SiteID: shopID, Domain: dom, Status: "active", Verified: true}
	ctx := context.Background()
	const why = "resolves to 192.0.2.9, not to this server"

	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	if info := a.domainInfo(t, shopID); !info.FailingSince.Valid || !info.VerifiedAt.Valid {
		t.Fatalf("failing clock not started: %+v", info)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '25 hours' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	failing.Status = "pending"
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(ctx, failing, "pending", why, "pending")
	if len(mail.sent) != 1 || !strings.HasPrefix(mail.sent[0], olive.Email+"|") {
		t.Fatalf("owner notices: %q", mail.sent)
	}
	if r := a.At(t, "GET", person, "/shop/", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+dom+"/" {
		t.Fatalf("before lapse: %d %q", r.Status, location(r))
	}
	if _, err := a.Database.Exec(`UPDATE sites SET domain_failing_since = now() - interval '73 hours', domain_release_at = now() - interval '1 hour' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(ctx, failing, "pending", why, "pending")
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
	label := fixture.Uniq("brand")
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
