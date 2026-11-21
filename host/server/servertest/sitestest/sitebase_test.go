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

	accountapp "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// The split-domain matrix (docs/history/site-base-domain-move.md, test plan):
// app on pcSiteDomain, people's addresses moving to sbBase.
const sbBase = "sh-site.test"

func TestPausedSiteBaseDeployment(t *testing.T) {
	a, dir, baseDir := newBaseApp(t, "off")
	owner := a.NewPerson(t, "paused")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, dir, handle)
	a.Deploy(t, owner, "shop")
	r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, map[string]string{"X-API-Key": owner.Key})
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), "https://shop."+handle+"."+servertest.PcSiteDomain+"/") || strings.Contains(string(r.Body), sbBase) {
		t.Fatalf("deployment address: %d %s", r.Status, r.Body)
	}
	for _, labels := range []string{handle, "shop." + handle} {
		r := a.At(t, "GET", labels+"."+sbBase, "/a%2Fb?x=1&y=%20z", nil, nil)
		want := "https://" + labels + "." + servertest.PcSiteDomain + "/a%2Fb?x=1&y=%20z"
		if r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != want {
			t.Fatalf("paused host: %d %q, want 301 %q", r.Status, r.Header.Get("Location"), want)
		}
	}
	a.Sites.RequestSiteCertsForAll(context.Background())
	requests, err := os.ReadDir(filepath.Join(baseDir, "requests"))
	if err != nil || len(requests) != 0 {
		t.Fatalf("paused base requested certificates: %v %v", requests, err)
	}
}

// newBaseApp is newSiteApp (person hosts and site hosts canonical) with the
// base moving to sbBase in move, and a second certificate hand-off for it.
// It returns the app, the SITE_DOMAIN cert dir and the base cert dir.
func newBaseApp(t *testing.T, move string) (*servertest.PrivateApp, string, string) {
	t.Helper()
	a, dir := servertest.NewSiteApp(t, "canonical")
	baseDir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(baseDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.Hosts.SetSiteBase(sbBase, move, baseDir)
	accountsrepo.SetPlatformDomains(a.Sites.HandoutBase(), a.Sites.ServedBases()...)
	t.Cleanup(func() { accountsrepo.SetPlatformDomain("") })
	return a, dir, baseDir
}

// TestSiteBaseMatrix walks every mode over the host kinds: what answers,
// what redirects where (status, path, query and escapes kept), and what is
// handed out.
func TestSiteBaseMatrix(t *testing.T) {
	for _, move := range []string{"off", "serve", "canonical", "redirect", "permanent"} {
		t.Run(move, func(t *testing.T) {
			a, dir, baseDir := newBaseApp(t, move)
			olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
			a.Deploy(t, olive, "shop")
			a.Deploy(t, oscar, "shop")
			_, oh := a.UserID(t, olive)
			_, sh := a.UserID(t, oscar)
			servertest.MarkReady(t, dir, oh)
			servertest.MarkReady(t, dir, sh)
			servertest.MarkReady(t, baseDir, oh) // oscar has no certificate under the base yet
			split := move != "off"
			handout := servertest.PcSiteDomain
			if move == "canonical" || move == "redirect" || move == "permanent" {
				handout = sbBase
			}
			redirect := move == "redirect" || move == "permanent"
			status := http.StatusFound
			if move == "permanent" {
				status = http.StatusMovedPermanently
			}
			okey := map[string]string{"X-API-Key": olive.Key}

			// ---- handed out --------------------------------------------------
			r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, okey)
			if !strings.Contains(string(r.Body), `"https://shop.`+oh+`.`+handout+`/"`) {
				t.Fatalf("site_url: %s", r.Body)
			}
			if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/me", nil, okey); r.JSON(t)["public_page"] != "https://"+oh+"."+handout+"/" {
				t.Fatalf("public_page: %s", r.Body)
			}
			r = a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites", nil, map[string]string{"X-API-Key": oscar.Key})
			if handout == sbBase {
				if !strings.Contains(string(r.Body), "https://"+sh+"."+sbBase+"/shop/") {
					t.Fatalf("not-ready site_url: %s", r.Body)
				}
			} else if !strings.Contains(string(r.Body), "https://shop."+sh+"."+servertest.PcSiteDomain+"/") {
				t.Fatalf("site_url on the old base: %s", r.Body)
			}

			// ---- the old base --------------------------------------------------
			p := "/a%2Fb/c?x=1&y=%20z"
			oldSite := "shop." + oh + "." + servertest.PcSiteDomain
			r = a.At(t, "GET", oldSite, p, nil, nil)
			if redirect {
				if r.Status != status || r.Header.Get("Location") != "https://shop."+oh+"."+sbBase+p {
					t.Fatalf("old site host: %d %q", r.Status, r.Header.Get("Location"))
				}
				if (status == http.StatusFound) != (r.Header.Get("Cache-Control") == "no-store") {
					t.Fatalf("cache-control %q on %d", r.Header.Get("Cache-Control"), status)
				}
			} else if r.Status != http.StatusNotFound {
				t.Fatalf("old site host served: %d %q", r.Status, r.Header.Get("Location"))
			}
			if r := a.At(t, "GET", oldSite, "/", nil, nil); !redirect && (r.Status != 200 || string(r.Body) != "<h1>shop</h1>") {
				t.Fatalf("old site host root: %d", r.Status)
			}
			if r := a.At(t, "GET", oldSite, "//evil.example/x", nil, nil); redirect && !strings.HasPrefix(r.Header.Get("Location"), "https://shop."+oh+"."+sbBase+"/") {
				t.Fatalf("double slash: %q", r.Header.Get("Location"))
			}
			// A site host whose owner has no certificate under the base: its
			// person path there, always 302.
			r = a.At(t, "GET", "shop."+sh+"."+servertest.PcSiteDomain, "/x?y=1", nil, nil)
			if redirect && (r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+sh+"."+sbBase+"/shop/x?y=1") {
				t.Fatalf("not-ready old site host: %d %q", r.Status, r.Header.Get("Location"))
			}
			oldPerson := oh + "." + servertest.PcSiteDomain
			r = a.At(t, "GET", oldPerson, "/?q=1", nil, nil)
			if redirect {
				if r.Status != status || r.Header.Get("Location") != "https://"+oh+"."+sbBase+"/?q=1" {
					t.Fatalf("old person host: %d %q", r.Status, r.Header.Get("Location"))
				}
			} else if r.Status != 200 {
				t.Fatalf("old person host: %d", r.Status)
			}
			// /v1/ is never redirected: old pages keep calling it.
			if r := a.At(t, "GET", oldSite, "/v1/sites/shop/me", nil, map[string]string{"Origin": "https://" + oldSite}); r.Status != 200 {
				t.Fatalf("old /v1/: %d %s", r.Status, r.Body)
			}
			// The content host and the app itself never move.
			for _, h := range []string{servertest.PcContentHost, servertest.PcSiteDomain, "cname." + servertest.PcSiteDomain, "www." + servertest.PcSiteDomain, "lab." + servertest.PcSiteDomain, "x.lab." + servertest.PcSiteDomain} {
				if loc := a.At(t, "GET", h, "/", nil, nil).Header.Get("Location"); strings.Contains(loc, sbBase) {
					t.Fatalf("%s moved: %q", h, loc)
				}
			}
			// Old content-host links go to the address handed out; 301 once permanent.
			r = a.At(t, "GET", servertest.PcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil)
			wantStatus := http.StatusFound
			if move == "permanent" {
				wantStatus = http.StatusMovedPermanently
			}
			if r.Status != wantStatus || r.Header.Get("Location") != "https://shop."+oh+"."+handout+"/sub/?q=1" {
				t.Fatalf("content-host redirect: %d %q", r.Status, r.Header.Get("Location"))
			}

			// ---- the new base --------------------------------------------------
			newSite := "shop." + oh + "." + sbBase
			newPerson := oh + "." + sbBase
			r = a.At(t, "GET", newSite, "/", nil, nil)
			if split != (r.Status == 200 && string(r.Body) == "<h1>shop</h1>") {
				t.Fatalf("new site host: %d %s", r.Status, r.Body)
			}
			r = a.At(t, "GET", newPerson, "/", nil, nil)
			if split != (r.Status == 200) {
				t.Fatalf("new person host: %d", r.Status)
			}
			if split {
				// Person path under the base goes to the site host under the base.
				if r := a.At(t, "GET", newPerson, "/shop/x?y=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+newSite+"/x?y=1" {
					t.Fatalf("new person path: %d %q", r.Status, r.Header.Get("Location"))
				}
				// No certificate under the base: the person path serves there.
				if r := a.At(t, "GET", sh+"."+sbBase, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
					t.Fatalf("not-ready new person path: %d %q", r.Status, r.Header.Get("Location"))
				}
				if r := a.At(t, "GET", "shop."+sh+"."+sbBase, "/x", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+sh+"."+sbBase+"/shop/x" {
					t.Fatalf("not-ready new site host: %d %q", r.Status, r.Header.Get("Location"))
				}
				// The API on the new site host, same-origin.
				if r := a.At(t, "GET", newSite, "/v1/sites/shop/me", nil, map[string]string{"Origin": "https://" + newSite}); r.Status != 200 {
					t.Fatalf("new /v1/: %d %s", r.Status, r.Body)
				}
				// Its apex, www and reserved names are the app's.
				for _, h := range []string{sbBase, "www." + sbBase, "sites." + sbBase, "cname." + sbBase} {
					if r := a.At(t, "GET", h, "/x", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != "https://"+servertest.PcSiteDomain+"/" {
						t.Fatalf("%s: %d %q", h, r.Status, r.Header.Get("Location"))
					}
				}
				if !a.Access.Hosts.IsApexHost(sbBase) || !a.Access.Hosts.IsApexHost("www."+sbBase) {
					t.Fatal("base apex is not the app's")
				}
			} else if a.Access.Hosts.IsApexHost(sbBase) {
				t.Fatal("base apex while off")
			}

			// ---- a claimed free name ---------------------------------------------
			free := "clay" + fmt.Sprint(time.Now().UnixNano()%1e9)
			asked := free + "." + servertest.PcSiteDomain
			if split {
				asked = free + "." + sbBase // what a newer skill sends
			}
			r = a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": asked}, okey)
			if !split {
				if r.Status != 200 || r.JSON(t)["domain"] != free+"."+servertest.PcSiteDomain {
					t.Fatalf("claim: %d %s", r.Status, r.Body)
				}
			} else if r.Status != 200 || r.JSON(t)["domain"] != free+"."+handout {
				t.Fatalf("claim stored as the handed-out form: %d %s", r.Status, r.Body)
			}
			for _, b := range []string{servertest.PcSiteDomain, sbBase} {
				r := a.At(t, "GET", free+"."+b, "/?k=v", nil, nil)
				switch {
				case b == servertest.PcSiteDomain && redirect:
					if r.Status != status || r.Header.Get("Location") != "https://"+free+"."+sbBase+"/?k=v" {
						t.Fatalf("old claimed name: %d %q", r.Status, r.Header.Get("Location"))
					}
				case b == sbBase && !split:
					if r.Status == 200 && string(r.Body) == "<h1>shop</h1>" {
						t.Fatalf("claimed name under the base while off")
					}
				default:
					if r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
						t.Fatalf("claimed name on %s: %d", b, r.Status)
					}
				}
			}
			// One namespace: the name is taken under either base, as a handle too.
			if taken, err := accountsrepo.HandleInUse(context.Background(), a.Database, "", free); err != nil || !taken {
				t.Fatalf("claimed name free as a handle: %v %v", taken, err)
			}
			r = a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free + "." + servertest.PcSiteDomain}, map[string]string{"X-API-Key": oscar.Key})
			if r.Status != http.StatusConflict {
				t.Fatalf("second claim of the same name: %d %s", r.Status, r.Body)
			}
			if split {
				if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free + "." + sbBase}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
					t.Fatalf("second claim, other form: %d %s", r.Status, r.Body)
				}
			}

			// ---- certificate requests --------------------------------------------
			a.Sites.RequestSiteCertsForAll(context.Background())
			_, errBase := os.Stat(filepath.Join(baseDir, "requests", sh))
			if split != (errBase == nil) {
				t.Fatalf("base request for %s: %v", sh, errBase)
			}
		})
	}
}

// TestSiteBaseCertRequestsStopOnOldBase: once the base is handed out, new
// people get certificates under it only.
func TestSiteBaseCertRequestsStopOnOldBase(t *testing.T) {
	for _, c := range []struct {
		move        string
		old, onBase bool
	}{{"off", true, false}, {"serve", true, true}, {"canonical", false, true}, {"permanent", false, true}} {
		a, dir, baseDir := newBaseApp(t, c.move)
		p := a.NewPerson(t, "newbie")
		a.Deploy(t, p, "shop")
		_, h := a.UserID(t, p)
		a.Sites.RequestSiteCertsForAll(context.Background())
		_, e1 := os.Stat(filepath.Join(dir, "requests", h))
		_, e2 := os.Stat(filepath.Join(baseDir, "requests", h))
		if (e1 == nil) != c.old || (e2 == nil) != c.onBase {
			t.Errorf("%s: old %v base %v", c.move, e1, e2)
		}
	}
}

// TestSiteBaseOrigins: a page on either base passes as the site's own host,
// and an allow-listed origin counts for its twin.
func TestSiteBaseOrigins(t *testing.T) {
	a, dir, baseDir := newBaseApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, dir, oh)
	servertest.MarkReady(t, baseDir, oh)
	id := a.SiteID(t, olive, "shop")
	ctx := context.Background()
	for _, host := range []string{"shop." + oh + "." + servertest.PcSiteDomain, "shop." + oh + "." + sbBase} {
		if !a.Sites.Hosts.OriginIsSiteHost(ctx, id, host) {
			t.Errorf("site host %s refused", host)
		}
	}
	for _, host := range []string{oh + "." + servertest.PcSiteDomain, oh + "." + sbBase} {
		if a.Sites.Hosts.OriginIsPersonHost(ctx, id, host) {
			t.Errorf("showcase origin accepted for site: %s", host)
		}
	}
	homeOwner, _ := a.UserID(t, olive)
	name := "shop"
	if err := sitesrepo.SetHomeSite(ctx, a.Database, homeOwner, &name); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{oh + "." + servertest.PcSiteDomain, oh + "." + sbBase} {
		if !a.Sites.Hosts.OriginIsPersonHost(ctx, id, host) {
			t.Errorf("person host %s refused", host)
		}
	}
	for _, host := range []string{"blog." + oh + "." + sbBase, "shop.someone-else." + sbBase, "someone-else." + sbBase, "shop." + oh + ".evil.test"} {
		if a.Sites.Hosts.OriginIsSiteHost(ctx, id, host) || a.Sites.Hosts.OriginIsPersonHost(ctx, id, host) {
			t.Errorf("%s accepted", host)
		}
	}
	if _, err := a.Database.Exec(`UPDATE sites SET allowed_origins = 'https://pinned.`+servertest.PcSiteDomain+`' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if !a.Sites.Hosts.OriginListed(ctx, id, "https://pinned."+sbBase) || a.Sites.Hosts.OriginListed(ctx, id, "http://pinned."+sbBase) ||
		a.Sites.Hosts.OriginListed(ctx, id, "https://other."+sbBase) {
		t.Fatal("allow-list twin")
	}
	a.Sites.Hosts.SetSiteBase(sbBase, "off", baseDir)
	if a.Sites.Hosts.OriginListed(ctx, id, "https://pinned."+sbBase) || a.Sites.Hosts.OriginIsPersonHost(ctx, id, oh+"."+sbBase) {
		t.Fatal("twin accepted while off")
	}
}

// TestSiteBaseReturnTo: sign-in may return to a person, site or claimed host
// under either base while it moves, and never to the base's apex, www or an
// unclaimed name under it.
func TestSiteBaseReturnTo(t *testing.T) {
	a, dir, baseDir := newBaseApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "shop") // no certificate: lives on its person path
	a.Deploy(t, olive, "blog") // holds the claimed name
	_, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	servertest.MarkReady(t, dir, oh)
	servertest.MarkReady(t, baseDir, oh)
	free := "rt" + fmt.Sprint(time.Now().UnixNano()%1e9)
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": free + "." + servertest.PcSiteDomain}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	cfg := config.Config{SiteDomain: servertest.PcSiteDomain, ContentHost: servertest.PcContentHost, CNAMETarget: "cname." + servertest.PcSiteDomain, PublicBaseURL: "https://" + servertest.PcSiteDomain}
	o := servertest.NewTestOAuthHandler(a.Database, accountapp.New(a.Database), cfg)
	o.SetPersonSiteResolver(a.Sites.Hosts.ReturnSite)
	check := func(raw string, ok bool) {
		t.Helper()
		_, site, host, _, err := o.SanitizeReturnTo(context.Background(), raw)
		if ok != (err == nil) {
			t.Errorf("%s: err %v", raw, err)
		}
		if ok && (!site.Valid || !strings.Contains(raw, host)) {
			t.Errorf("%s: site %v host %q", raw, site, host)
		}
	}
	o.SetSiteBases(a.Sites.ServedBases(), a.Sites.Hosts.SameUserHost)
	check("https://"+sh+"."+sbBase+"/shop/", true)
	check("https://"+sh+"."+servertest.PcSiteDomain+"/shop/", true)
	check("https://shop."+oh+"."+sbBase+"/", true)
	check("https://shop."+oh+"."+servertest.PcSiteDomain+"/", true)
	// A site on its own site host signs in there, not on the person path.
	check("https://"+oh+"."+sbBase+"/shop/", false)
	check("https://"+free+"."+servertest.PcSiteDomain+"/", true)
	check("https://"+free+"."+sbBase+"/", true)
	check("https://"+sbBase+"/", false)
	check("https://www."+sbBase+"/", false)
	check("https://unclaimed-name."+sbBase+"/", false)
	check("https://nope."+oh+"."+sbBase+"/", false)
	check("https://x.y.z."+sbBase+"/", false)
}

// TestMoveSiteBase: the data rewrite (dry run counts, apply, idempotent) never
// touches custom domains, reserved names or multi-label names, and lookups
// find a name in either form before and after.
func TestMoveSiteBase(t *testing.T) {
	a, _, _ := newBaseApp(t, "serve")
	ctx := context.Background()
	// Domains of this run only, so other tests' names are never moved.
	n0 := fmt.Sprint(time.Now().UnixNano() % 1e9)
	from, to := "from"+n0+".test", "to"+n0+".test"
	accountsrepo.SetPlatformDomains(to, from)
	olive := a.NewPerson(t, "olive")
	for _, s := range []string{"one", "two", "three", "four"} {
		a.Deploy(t, olive, s)
	}
	uid, _ := a.UserID(t, olive)
	n := fmt.Sprint(time.Now().UnixNano() % 1e9)
	set := func(site, cur, prev string) {
		t.Helper()
		var p sql.NullString
		if prev != "" {
			p = sql.NullString{String: prev, Valid: true}
		}
		if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2, previous_domain = $3, domain_verified_at = now() WHERE user_id = $1 AND name = $4`, uid, cur, p, site); err != nil {
			t.Fatal(err)
		}
	}
	set("one", "mv"+n+"."+from, "old"+n+"."+from)
	set("two", "shop"+n+".example.com", "")
	set("three", "a.b"+n+"."+from, "")
	set("four", "cname."+from, "")
	if _, err := a.Database.Exec(`INSERT INTO legacy_hostnames (hostname, site_id, user_id) VALUES ($1, NULL, $2)`, "gone"+n+"."+from, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := sitesrepo.GetSiteByCustomDomain(ctx, a.Database, "mv"+n+"."+to, accountsrepo.HostForms("mv"+n+"."+to)); err != nil {
		t.Fatalf("lookup of the new form before the move: %v", err)
	}
	dry, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, to, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.CustomDomains < 1 || dry.PreviousDomains < 1 || dry.RetiredNames < 1 {
		t.Fatalf("dry run: %+v", dry)
	}
	var cur string
	a.Database.QueryRow(`SELECT custom_domain FROM sites WHERE user_id = $1 AND name = 'one'`, uid).Scan(&cur)
	if cur != "mv"+n+"."+from {
		t.Fatalf("dry run wrote: %s", cur)
	}
	got, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, to, true)
	if err != nil || got.CustomDomains != dry.CustomDomains || got.RetiredNames != dry.RetiredNames {
		t.Fatalf("apply: %+v %v (dry %+v)", got, err, dry)
	}
	want := map[string]string{"one": "mv" + n + "." + to, "two": "shop" + n + ".example.com", "three": "a.b" + n + "." + from, "four": "cname." + from}
	for site, w := range want {
		var c string
		a.Database.QueryRow(`SELECT custom_domain FROM sites WHERE user_id = $1 AND name = $2`, uid, site).Scan(&c)
		if c != w {
			t.Errorf("%s: %s, want %s", site, c, w)
		}
	}
	var prev string
	a.Database.QueryRow(`SELECT previous_domain FROM sites WHERE user_id = $1 AND name = 'one'`, uid).Scan(&prev)
	if prev != "old"+n+"."+to {
		t.Errorf("previous: %s", prev)
	}
	if _, err := sitesrepo.GetRetiredName(ctx, a.Database, "gone"+n+"."+from, accountsrepo.HostForms("gone"+n+"."+from)); err != nil {
		t.Errorf("retired name under the old form: %v", err)
	}
	if _, err := sitesrepo.GetSiteByCustomDomain(ctx, a.Database, "mv"+n+"."+from, accountsrepo.HostForms("mv"+n+"."+from)); err != nil {
		t.Errorf("lookup of the old form after the move: %v", err)
	}
	again, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, to, true)
	if err != nil || again.CustomDomains+again.PreviousDomains+again.RetiredNames != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	if _, err := a.Sites.SitesSvc().MoveSiteBase(ctx, from, from, false); err == nil {
		t.Fatal("same domain accepted")
	}
}
