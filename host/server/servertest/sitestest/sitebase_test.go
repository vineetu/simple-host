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

// The split-domain move (docs/history/site-base-domain-move.md): the app on
// rt.SiteDomain, people's addresses moving to sbBase.
const sbBase = "sh-site.test"

// newBaseApp is NewApp with the base moving to sbBase in move, and a second
// certificate hand-off for it (returned).
func newBaseApp(t *testing.T, move string) (*App, string) {
	t.Helper()
	a := NewApp(t)
	baseDir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(baseDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Hosts.SetSiteBase(sbBase, move, baseDir)
	accountsrepo.SetPlatformDomains(a.Sites.HandoutBase(), a.Sites.ServedBases()...)
	return a, baseDir
}

// Paused (off): the base only redirects to the old addresses, nothing hands
// it out and no certificate is asked for under it.
func TestPausedSiteBaseDeployment(t *testing.T) {
	a, baseDir := newBaseApp(t, "off")
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
	a.Sites.RequestSiteCertsForAll(context.Background())
	if requests, err := os.ReadDir(filepath.Join(baseDir, "requests")); err != nil || len(requests) != 0 {
		t.Fatalf("paused base requested certificates: %v %v", requests, err)
	}
}

// Each mode over the host kinds: what answers, what redirects where (status,
// path, query and escapes kept), what is handed out, the claimed-name
// namespace, and where certificates are asked for.
func TestSiteBaseMatrix(t *testing.T) {
	for _, move := range []string{"off", "serve", "permanent"} {
		t.Run(move, func(t *testing.T) {
			a, baseDir := newBaseApp(t, move)
			olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
			a.Deploy(t, olive, "shop")
			a.Deploy(t, oscar, "shop")
			oh, sh := olive.Handle, oscar.Handle
			if err := os.WriteFile(filepath.Join(baseDir, "ready", oh), nil, 0o644); err != nil { // oscar has none under the base
				t.Fatal(err)
			}
			split, redirect := move != "off", move == "permanent"
			handout := rt.SiteDomain
			if redirect {
				handout = sbBase
			}
			if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), `"https://shop.`+oh+`.`+handout+`/"`) {
				t.Fatalf("site_url: %s", r.Body)
			}

			// The old base.
			p := "/a%2Fb/c?x=1&y=%20z"
			oldSite := "shop." + oh + "." + rt.SiteDomain
			r := a.At(t, "GET", oldSite, p, nil, nil)
			if redirect && (r.Status != http.StatusMovedPermanently || location(r) != "https://shop."+oh+"."+sbBase+p) {
				t.Fatalf("old site host: %d %q", r.Status, location(r))
			}
			if r := a.At(t, "GET", oldSite, "/", nil, nil); !redirect && (r.Status != 200 || string(r.Body) != "<h1>shop</h1>") {
				t.Fatalf("old site host root: %d", r.Status)
			}
			if r := a.At(t, "GET", oldSite, "//evil.example/x", nil, nil); redirect && !strings.HasPrefix(location(r), "https://shop."+oh+"."+sbBase+"/") {
				t.Fatalf("double slash: %q", location(r))
			}
			if r := a.At(t, "GET", "shop."+sh+"."+rt.SiteDomain, "/x?y=1", nil, nil); redirect && (r.Status != http.StatusFound || location(r) != "https://"+sh+"."+sbBase+"/shop/x?y=1") {
				t.Fatalf("not-ready old site host: %d %q", r.Status, location(r))
			}
			for _, h := range []string{rt.ContentHost, rt.SiteDomain, "cname." + rt.SiteDomain, "www." + rt.SiteDomain} {
				if loc := location(a.At(t, "GET", h, "/", nil, nil)); strings.Contains(loc, sbBase) {
					t.Fatalf("%s moved: %q", h, loc)
				}
			}
			wantStatus := http.StatusFound
			if redirect {
				wantStatus = http.StatusMovedPermanently
			}
			if r := a.At(t, "GET", rt.ContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil); r.Status != wantStatus || location(r) != "https://shop."+oh+"."+handout+"/sub/?q=1" {
				t.Fatalf("content-host redirect: %d %q", r.Status, location(r))
			}

			// The new base.
			newSite, newPerson := "shop."+oh+"."+sbBase, oh+"."+sbBase
			if r := a.At(t, "GET", newSite, "/", nil, nil); split != (r.Status == 200 && string(r.Body) == "<h1>shop</h1>") {
				t.Fatalf("new site host: %d %s", r.Status, r.Body)
			}
			if split {
				if r := a.At(t, "GET", newPerson, "/shop/x?y=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+newSite+"/x?y=1" {
					t.Fatalf("new person path: %d %q", r.Status, location(r))
				}
				if r := a.At(t, "GET", sh+"."+sbBase, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
					t.Fatalf("not-ready new person path: %d %q", r.Status, location(r))
				}
				for _, h := range []string{sbBase, "www." + sbBase, "sites." + sbBase} {
					if r := a.At(t, "GET", h, "/x", nil, nil); r.Status != http.StatusMovedPermanently || location(r) != "https://"+rt.SiteDomain+"/" {
						t.Fatalf("%s: %d %q", h, r.Status, location(r))
					}
				}
			}
			if a.Hosts.IsApexHost(sbBase) != split {
				t.Fatalf("base apex is the app's: %v, split %v", a.Hosts.IsApexHost(sbBase), split)
			}

			// A claimed free name: stored as the handed-out form, one namespace.
			free := "clay" + fmt.Sprint(time.Now().UnixNano()%1e9)
			asked := free + "." + rt.SiteDomain
			if split {
				asked = free + "." + sbBase
			}
			if r := a.bind(t, olive, "shop", asked); r.Status != 200 || r.JSON(t)["domain"] != free+"."+handout {
				t.Fatalf("claim: %d %s", r.Status, r.Body)
			}
			for _, b := range []string{rt.SiteDomain, sbBase} {
				r := a.At(t, "GET", free+"."+b, "/?k=v", nil, nil)
				switch {
				case b == rt.SiteDomain && redirect:
					if r.Status != http.StatusMovedPermanently || location(r) != "https://"+free+"."+sbBase+"/?k=v" {
						t.Fatalf("old claimed name: %d %q", r.Status, location(r))
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
			if taken, err := accountsrepo.HandleInUse(context.Background(), a.Database, "", free); err != nil || !taken {
				t.Fatalf("claimed name free as a handle: %v %v", taken, err)
			}
			for _, b := range []string{rt.SiteDomain, sbBase} {
				if r := a.bind(t, oscar, "shop", free+"."+b); split || b == rt.SiteDomain {
					if r.Status != http.StatusConflict {
						t.Fatalf("second claim of the same name (%s): %d %s", b, r.Status, r.Body)
					}
				}
			}

			// Certificates: once handed out, new people get them under the base only.
			newbie := a.NewPerson(t, "newbie")
			a.files(t, newbie, "POST", "shop", map[string]string{"index.html": "x"})
			a.Sites.RequestSiteCertsForAll(context.Background())
			_, eOld := os.Stat(filepath.Join(a.CertDir, "requests", newbie.Handle))
			_, eBase := os.Stat(filepath.Join(baseDir, "requests", newbie.Handle))
			if (eOld == nil) != !redirect || (eBase == nil) != split {
				t.Fatalf("certificate requests: old %v base %v", eOld, eBase)
			}
		})
	}
}

// A page on either base passes as the site's own host (the person host only
// for a home site), and an allow-listed origin counts for its twin while the
// move is on.
func TestSiteBaseOrigins(t *testing.T) {
	a, baseDir := newBaseApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	id := a.Deploy(t, olive, "shop")
	oh := olive.Handle
	if err := os.WriteFile(filepath.Join(baseDir, "ready", oh), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, host := range []string{"shop." + oh + "." + rt.SiteDomain, "shop." + oh + "." + sbBase} {
		if !a.Hosts.OriginIsSiteHost(ctx, id, host) {
			t.Errorf("site host %s refused", host)
		}
	}
	if a.Hosts.OriginIsPersonHost(ctx, id, oh+"."+sbBase) {
		t.Error("showcase origin accepted for site")
	}
	a.setHome(t, olive, "shop")
	if !a.Hosts.OriginIsPersonHost(ctx, id, oh+"."+sbBase) {
		t.Error("home's person host refused")
	}
	for _, host := range []string{"blog." + oh + "." + sbBase, "shop.someone-else." + sbBase, "shop." + oh + ".evil.test"} {
		if a.Hosts.OriginIsSiteHost(ctx, id, host) || a.Hosts.OriginIsPersonHost(ctx, id, host) {
			t.Errorf("%s accepted", host)
		}
	}
	if _, err := a.Database.Exec(`UPDATE sites SET allowed_origins = 'https://pinned.`+rt.SiteDomain+`' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if !a.Hosts.OriginListed(ctx, id, "https://pinned."+sbBase) || a.Hosts.OriginListed(ctx, id, "http://pinned."+sbBase) || a.Hosts.OriginListed(ctx, id, "https://other."+sbBase) {
		t.Fatal("allow-list twin")
	}
	a.Hosts.SetSiteBase(sbBase, "off", baseDir)
	if a.Hosts.OriginListed(ctx, id, "https://pinned."+sbBase) || a.Hosts.OriginIsPersonHost(ctx, id, oh+"."+sbBase) {
		t.Fatal("twin accepted while off")
	}
}

// The data rewrite of a base move (dry run counts, apply, idempotent) never
// touches custom domains, reserved names or multi-label names, and lookups
// find a name in either form before and after.
func TestMoveSiteBase(t *testing.T) {
	a, _ := newBaseApp(t, "serve")
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
