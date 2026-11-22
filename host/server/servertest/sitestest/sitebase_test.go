package sitestest

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// A second base for people's addresses (sitebase.go): the app on
// rt.SiteDomain, new accounts' flat addresses under sbBase.
const sbBase = "sh-site.test"

// newBaseApp is NewApp with SITE_BASE_DOMAIN sbBase in move.
func newBaseApp(t *testing.T, move string) *App {
	t.Helper()
	a := NewApp(t)
	a.Hosts.SetSiteBase(sbBase, move)
	a.Sites.SetTeamSiteFlatHosts(false)
	accountsrepo.SetPlatformDomains(a.Sites.HandoutBase(), a.Sites.ServedBases()...)
	return a
}

// Paused (off): the base only redirects to the old addresses, nothing hands
// it out and no certificate is asked for under it.
func TestPausedSiteBaseDeployment(t *testing.T) {
	a := newBaseApp(t, "off")
	owner := a.NewPerson(t, "paused")
	a.Deploy(t, owner, "shop")
	if r := a.api(t, owner, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+owner.SiteHost("shop")+"/") || strings.Contains(string(r.Body), sbBase) {
		t.Fatalf("deployment address: %d %s", r.Status, r.Body)
	}
	for _, labels := range []string{owner.Handle, "shop." + owner.Handle} {
		r := a.At(t, "GET", labels+"."+sbBase, "/a%2Fb?x=1&y=%20z", nil, nil)
		if want := "https://" + labels + "." + rt.SiteDomain + "/a%2Fb?x=1&y=%20z"; r.Status != http.StatusMovedPermanently || location(r) != want {
			t.Fatalf("paused host: %d %q, want 301 %q", r.Status, location(r), want)
		}
	}
}

// Flat base: a host resolves to exactly one site or person. The split is at
// the first "--", hosts under the wrong base go to the right one (pages
// only: the API there is a 404), free names stay on SITE_DOMAIN, and the
// base's apex is the app's.
func TestFlatSiteBaseHosts(t *testing.T) {
	a := newBaseApp(t, "flat")
	ctx := context.Background()
	nina := a.NewPerson(t, "nina") // new: no certificate of her own
	a.files(t, nina, "POST", "shop", map[string]string{"index.html": "<h1>nina shop</h1>"})
	olive := a.NewPerson(t, "olive") // existing: has one
	a.Deploy(t, olive, "shop")
	flat := "shop--" + nina.Handle + "." + sbBase

	if r := a.api(t, nina, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), `"https://`+flat+`/"`) {
		t.Fatalf("new account's address: %s", r.Body)
	}
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), `"https://`+olive.SiteHost("shop")+`/"`) || strings.Contains(string(r.Body), sbBase) {
		t.Fatalf("existing account's address: %s", r.Body)
	}
	if site, handle, _, ok := a.Hosts.SiteHostParts(ctx, "x--"+nina.Handle+"--y."+sbBase); !ok || site != "x" || handle != nina.Handle+"--y" {
		t.Fatalf("split at the first --: %q %q", site, handle)
	}
	if _, _, _, ok := a.Hosts.SiteHostParts(ctx, "shop--"+nina.Handle+"."+rt.SiteDomain); ok {
		t.Fatal("flat label parsed under SITE_DOMAIN")
	}
	for host, want := range map[string][2]string{
		flat:                                  {"200", "<h1>nina shop</h1>"},
		nina.Handle + "." + sbBase + "/shop/": {"302", "https://" + flat + "/"},
		nina.Handle + "." + rt.SiteDomain + "/shop/?q=1":   {"302", "https://" + nina.Handle + "." + sbBase + "/shop/?q=1"},
		"shop." + nina.Handle + "." + rt.SiteDomain + "/a": {"302", "https://" + flat + "/a"},
		olive.SiteHost("shop"):                             {"200", "<h1>shop</h1>"},
		"shop--" + olive.Handle + "." + sbBase + "/a?b=1":  {"302", "https://" + olive.SiteHost("shop") + "/a?b=1"},
		olive.Handle + "." + sbBase + "/x":                 {"302", "https://" + olive.Handle + "." + rt.SiteDomain + "/x"},
		"shop." + olive.Handle + "." + sbBase + "/x":       {"302", "https://shop." + olive.Handle + "." + rt.SiteDomain + "/x"},
		sbBase + "/x":          {"301", "https://" + rt.SiteDomain + "/"},
		"www." + sbBase + "/x": {"301", "https://" + rt.SiteDomain + "/"},
	} {
		h, p, _ := strings.Cut(host, "/")
		r := a.At(t, "GET", h, "/"+p, nil, nil)
		if fmt.Sprint(r.Status) != want[0] || (r.Status == 200 && string(r.Body) != want[1]) || (r.Status != 200 && location(r) != want[1]) {
			t.Errorf("%s: %d %q %s, want %s %s", host, r.Status, location(r), r.Body, want[0], want[1])
		}
	}
	// The wrong shape: the API, a preflight and a POST get a plain 404, and
	// a page redirect drops sign-in values.
	for _, host := range []string{"shop--" + olive.Handle + "." + sbBase, olive.Handle + "." + sbBase, "shop." + nina.Handle + "." + sbBase, nina.Handle + "." + rt.SiteDomain, "shop." + nina.Handle + "." + rt.SiteDomain} {
		for _, m := range []string{"GET", "OPTIONS", "POST"} {
			if r := a.At(t, m, host, "/v1/sites/shop/me", nil, nil); r.Status != http.StatusNotFound {
				t.Errorf("%s %s /v1/: %d %q", m, host, r.Status, location(r))
			}
		}
		if r := a.At(t, "POST", host, "/form", nil, nil); r.Status != http.StatusNotFound {
			t.Errorf("POST %s page: %d", host, r.Status)
		}
	}
	if r := a.At(t, "GET", "shop--"+olive.Handle+"."+sbBase, "/p?once=abc&cn=def&k=v", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+olive.SiteHost("shop")+"/p?k=v" {
		t.Errorf("sign-in values carried: %d %q", r.Status, location(r))
	}
	// Sign-in returns only to the owner's own shape.
	if _, ok := a.Hosts.ReturnSite(ctx, "shop."+nina.Handle+"."+sbBase, "/"); ok {
		t.Error("sign-in return to a two-label host under the base for a flat person")
	}
	if _, ok := a.Hosts.ReturnSite(ctx, "shop--"+olive.Handle+"."+sbBase, "/"); ok {
		t.Error("sign-in return to the flat shape of a two-label person")
	}
	if id, ok := a.Hosts.ReturnSite(ctx, flat, "/"); !ok || id != a.SiteID(t, nina, "shop") {
		t.Error("sign-in return to a flat person's own host")
	}
	// Origins: the flat host is the new account's site's own; nothing else is.
	ninaShop := a.SiteID(t, nina, "shop")
	if !a.Hosts.OriginIsSiteHost(ctx, ninaShop, flat) || a.Hosts.OriginIsSiteHost(ctx, ninaShop, "shop."+nina.Handle+"."+rt.SiteDomain) {
		t.Fatal("new account's origin")
	}
	oliveShop := a.SiteID(t, olive, "shop")
	if !a.Hosts.OriginIsSiteHost(ctx, oliveShop, olive.SiteHost("shop")) || a.Hosts.OriginIsSiteHost(ctx, oliveShop, "shop--"+olive.Handle+"."+sbBase) {
		t.Fatal("existing account's origin")
	}
	// A free name is claimed under SITE_DOMAIN, never with "--".
	free := "clay" + fmt.Sprint(time.Now().UnixNano()%1e9)
	if r := a.bind(t, nina, "shop", free+"."+sbBase); r.Status != 200 || r.JSON(t)["domain"] != free+"."+rt.SiteDomain {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.bind(t, olive, "shop", "a--"+free+"."+rt.SiteDomain); r.Status != http.StatusBadRequest {
		t.Fatalf("claim with --: %d %s", r.Status, r.Body)
	}
	// Certificates: none for the new account, a stale request withdrawn.
	if err := os.WriteFile(filepath.Join(a.CertDir, "requests", nina.Handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	a.Sites.RequestSiteCertsForAll(ctx)
	if _, err := os.Stat(filepath.Join(a.CertDir, "requests", nina.Handle)); !os.IsNotExist(err) {
		t.Fatalf("certificate request for a flat account: %v", err)
	}
	// In flat mode nothing ever writes a request: everyone is either ready or flat.
	a.Hosts.RequestSiteCert(ctx, a.NewPerson(t, "fresh").Handle)
	if left, err := os.ReadDir(filepath.Join(a.CertDir, "requests")); err != nil || len(left) != 0 {
		t.Fatalf("certificate requests written in flat mode: %v %v", left, err)
	}
}

// The data rewrite of a base move (dry run counts, apply, idempotent) never
// touches custom domains, reserved names or multi-label names, and lookups
// find a name in either form before and after.
func TestMoveSiteBase(t *testing.T) {
	a := newBaseApp(t, "flat")
	ctx := context.Background()
	n := fmt.Sprint(time.Now().UnixNano() % 1e9)
	from, to := "from"+n+".test", "to"+n+".test"
	accountsrepo.SetPlatformDomains(to, from)
	olive := a.NewPerson(t, "olive")
	for _, s := range []string{"one", "two", "three", "four"} {
		a.Deploy(t, olive, s)
	}
	set := func(site, cur, prev string) {
		t.Helper()
		p := sql.NullString{String: prev, Valid: prev != ""}
		if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2, previous_domain = $3, domain_verified_at = now() WHERE user_id = $1 AND name = $4`, olive.ID, cur, p, site); err != nil {
			t.Fatal(err)
		}
	}
	set("one", "mv"+n+"."+from, "old"+n+"."+from)
	set("two", "shop"+n+".example.com", "")
	set("three", "a.b"+n+"."+from, "")
	set("four", "cname."+from, "")
	if _, err := a.Database.Exec(`INSERT INTO legacy_hostnames (hostname, site_id, user_id) VALUES ($1, NULL, $2)`, "gone"+n+"."+from, olive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sitesrepo.GetSiteByCustomDomain(ctx, a.Database, "mv"+n+"."+to, accountsrepo.HostForms("mv"+n+"."+to)); err != nil {
		t.Fatalf("lookup of the new form before the move: %v", err)
	}
	dry, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, to, false)
	if err != nil || dry.CustomDomains < 1 || dry.PreviousDomains < 1 || dry.RetiredNames < 1 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	domain := func(site string) (cur, prev string) {
		var p sql.NullString
		_ = a.Database.QueryRow(`SELECT custom_domain, previous_domain FROM sites WHERE user_id = $1 AND name = $2`, olive.ID, site).Scan(&cur, &p)
		return cur, p.String
	}
	if cur, _ := domain("one"); cur != "mv"+n+"."+from {
		t.Fatalf("dry run wrote: %s", cur)
	}
	if got, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, to, true); err != nil || got.CustomDomains != dry.CustomDomains || got.RetiredNames != dry.RetiredNames {
		t.Fatalf("apply: %+v %v (dry %+v)", got, err, dry)
	}
	for site, w := range map[string]string{"one": "mv" + n + "." + to, "two": "shop" + n + ".example.com", "three": "a.b" + n + "." + from, "four": "cname." + from} {
		if c, _ := domain(site); c != w {
			t.Errorf("%s: %s, want %s", site, c, w)
		}
	}
	if _, prev := domain("one"); prev != "old"+n+"."+to {
		t.Errorf("previous: %s", prev)
	}
	if _, err := sitesrepo.GetSiteByCustomDomain(ctx, a.Database, "mv"+n+"."+from, accountsrepo.HostForms("mv"+n+"."+from)); err != nil {
		t.Errorf("lookup of the old form after the move: %v", err)
	}
	if again, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, to, true); err != nil || again.CustomDomains+again.PreviousDomains+again.RetiredNames != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	if _, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, from, false); err == nil {
		t.Fatal("same domain accepted")
	}
}
