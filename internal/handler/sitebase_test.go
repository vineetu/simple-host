package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// The split-domain matrix (docs/designs/site-base-domain-move.md, test plan):
// app on pcSiteDomain, people's addresses moving to sbBase.
const sbBase = "sh-site.test"

// newBaseApp is newSiteApp (person hosts and site hosts canonical) with the
// base moving to sbBase in move, and a second certificate hand-off for it.
// It returns the app, the SITE_DOMAIN cert dir and the base cert dir.
func newBaseApp(t *testing.T, move string) (*privateApp, string, string) {
	t.Helper()
	a, dir := newSiteApp(t, "canonical")
	baseDir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(baseDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.sites.SetSiteBase(sbBase, move, baseDir)
	db.SetPlatformDomains(a.sites.HandoutBase(), a.sites.ServedBases()...)
	t.Cleanup(func() { db.SetPlatformDomain("") })
	return a, dir, baseDir
}

func TestSiteBaseModes(t *testing.T) {
	a := &SiteHandler{siteDomain: "simple-host.app"}
	for _, c := range []struct {
		base, move      string
		split           bool
		handout         string
		served          string
		redirects, perm bool
	}{
		{"", "", false, "simple-host.app", "simple-host.app", false, false},
		{"simple-host.app", "permanent", false, "simple-host.app", "simple-host.app", false, false},
		{"simple-host.site", "off", false, "simple-host.app", "simple-host.app", false, false},
		{"simple-host.site", "bogus", false, "simple-host.app", "simple-host.app", false, false},
		{"simple-host.site", "serve", true, "simple-host.app", "simple-host.site simple-host.app", false, false},
		{"simple-host.site", "canonical", true, "simple-host.site", "simple-host.site simple-host.app", false, false},
		{"simple-host.site", "redirect", true, "simple-host.site", "simple-host.site simple-host.app", true, false},
		{"Simple-Host.Site.", "permanent", true, "simple-host.site", "simple-host.site simple-host.app", true, true},
		// Nested with the app's domain: refused.
		{"u.simple-host.app", "canonical", false, "simple-host.app", "simple-host.app", false, false},
	} {
		a.SetSiteBase(c.base, c.move, "")
		if a.baseSplit() != c.split || a.handoutBase() != c.handout || strings.Join(a.servedBases(), " ") != c.served ||
			a.legacyRedirects() != c.redirects || (a.moveStatus() == http.StatusMovedPermanently) != c.perm {
			t.Errorf("%q/%q: split %v handout %s served %v redirects %v status %d", c.base, c.move,
				a.baseSplit(), a.handoutBase(), a.servedBases(), a.legacyRedirects(), a.moveStatus())
		}
		cfg := config.Config{SiteDomain: "simple-host.app", SiteBaseDomain: strings.ToLower(strings.TrimSuffix(c.base, ".")), SiteBaseMove: c.move}
		if cfg.SiteBaseDomain == "" {
			cfg.SiteBaseDomain = cfg.SiteDomain
		}
		if got := cfg.HandoutBase(); got != c.handout {
			t.Errorf("%q/%q: config handout %s, handler %s", c.base, c.move, got, c.handout)
		}
	}
}

func TestTwinHost(t *testing.T) {
	a := &SiteHandler{siteDomain: "simple-host.app", contentHost: "sites.simple-host.app"}
	a.SetSiteBase("simple-host.site", "off", "")
	if a.twinHost("clay.simple-host.app") != "" {
		t.Fatal("twin while off")
	}
	a.SetSiteBase("simple-host.site", "serve", "")
	for host, want := range map[string]string{
		"clay.simple-host.app":         "clay.simple-host.site",
		"CLAY.simple-host.site.":       "clay.simple-host.app",
		"shop.olive.simple-host.app":   "shop.olive.simple-host.site",
		"shop.olive.simple-host.site":  "shop.olive.simple-host.app",
		"a.b.c.simple-host.app":        "",
		"simple-host.app":              "",
		"www.simple-host.site":         "",
		"sites.simple-host.app":        "",
		"cname.simple-host.site":       "",
		"x.lab.simple-host.app":        "",
		".olive.simple-host.app":       "",
		"clay.example.com":             "",
		"clay.simple-host.app.example": "",
	} {
		if got := a.twinHost(host); got != want {
			t.Errorf("twin(%q) = %q, want %q", host, got, want)
		}
	}
	if !a.sameUserHost("olive.simple-host.site", "OLIVE.simple-host.app") || a.sameUserHost("olive.simple-host.site", "oscar.simple-host.app") {
		t.Fatal("sameUserHost")
	}
	if a.sameOriginAcrossBases("https://x.simple-host.app", "http://x.simple-host.site") ||
		a.sameOriginAcrossBases("https://x.simple-host.app:8443", "https://x.simple-host.site") ||
		!a.sameOriginAcrossBases("https://x.simple-host.app", "https://x.simple-host.site") {
		t.Fatal("sameOriginAcrossBases")
	}
}

// TestSiteBaseMatrix walks every mode over the host kinds: what answers,
// what redirects where (status, path, query and escapes kept), and what is
// handed out.
func TestSiteBaseMatrix(t *testing.T) {
	for _, move := range []string{"off", "serve", "canonical", "redirect", "permanent"} {
		t.Run(move, func(t *testing.T) {
			a, dir, baseDir := newBaseApp(t, move)
			olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
			a.deploy(t, olive, "shop")
			a.deploy(t, oscar, "shop")
			_, oh := a.userID(t, olive)
			_, sh := a.userID(t, oscar)
			markReady(t, dir, oh)
			markReady(t, dir, sh)
			markReady(t, baseDir, oh) // oscar has no certificate under the base yet
			split := move != "off"
			handout := pcSiteDomain
			if move == "canonical" || move == "redirect" || move == "permanent" {
				handout = sbBase
			}
			redirect := move == "redirect" || move == "permanent"
			status := http.StatusFound
			if move == "permanent" {
				status = http.StatusMovedPermanently
			}
			okey := map[string]string{"X-API-Key": olive.key}

			// ---- handed out --------------------------------------------------
			r := a.at(t, "GET", pcSiteDomain, "/v1/sites", nil, okey)
			if !strings.Contains(string(r.body), `"https://shop.`+oh+`.`+handout+`/"`) {
				t.Fatalf("site_url: %s", r.body)
			}
			if r := a.at(t, "GET", pcSiteDomain, "/v1/me", nil, okey); r.json(t)["public_page"] != "https://"+oh+"."+handout+"/" {
				t.Fatalf("public_page: %s", r.body)
			}
			r = a.at(t, "GET", pcSiteDomain, "/v1/sites", nil, map[string]string{"X-API-Key": oscar.key})
			if handout == sbBase {
				if !strings.Contains(string(r.body), "https://"+sh+"."+sbBase+"/shop/") {
					t.Fatalf("not-ready site_url: %s", r.body)
				}
			} else if !strings.Contains(string(r.body), "https://shop."+sh+"."+pcSiteDomain+"/") {
				t.Fatalf("site_url on the old base: %s", r.body)
			}

			// ---- the old base --------------------------------------------------
			p := "/a%2Fb/c?x=1&y=%20z"
			oldSite := "shop." + oh + "." + pcSiteDomain
			r = a.at(t, "GET", oldSite, p, nil, nil)
			if redirect {
				if r.status != status || r.header.Get("Location") != "https://shop."+oh+"."+sbBase+p {
					t.Fatalf("old site host: %d %q", r.status, r.header.Get("Location"))
				}
				if (status == http.StatusFound) != (r.header.Get("Cache-Control") == "no-store") {
					t.Fatalf("cache-control %q on %d", r.header.Get("Cache-Control"), status)
				}
			} else if r.status != http.StatusNotFound {
				t.Fatalf("old site host served: %d %q", r.status, r.header.Get("Location"))
			}
			if r := a.at(t, "GET", oldSite, "/", nil, nil); !redirect && (r.status != 200 || string(r.body) != "<h1>shop</h1>") {
				t.Fatalf("old site host root: %d", r.status)
			}
			if r := a.at(t, "GET", oldSite, "//evil.example/x", nil, nil); redirect && !strings.HasPrefix(r.header.Get("Location"), "https://shop."+oh+"."+sbBase+"/") {
				t.Fatalf("double slash: %q", r.header.Get("Location"))
			}
			// A site host whose owner has no certificate under the base: its
			// person path there, always 302.
			r = a.at(t, "GET", "shop."+sh+"."+pcSiteDomain, "/x?y=1", nil, nil)
			if redirect && (r.status != http.StatusFound || r.header.Get("Location") != "https://"+sh+"."+sbBase+"/shop/x?y=1") {
				t.Fatalf("not-ready old site host: %d %q", r.status, r.header.Get("Location"))
			}
			oldPerson := oh + "." + pcSiteDomain
			r = a.at(t, "GET", oldPerson, "/?q=1", nil, nil)
			if redirect {
				if r.status != status || r.header.Get("Location") != "https://"+oh+"."+sbBase+"/?q=1" {
					t.Fatalf("old person host: %d %q", r.status, r.header.Get("Location"))
				}
			} else if r.status != 200 {
				t.Fatalf("old person host: %d", r.status)
			}
			// /v1/ is never redirected: old pages keep calling it.
			if r := a.at(t, "GET", oldSite, "/v1/sites/shop/state", nil, map[string]string{"Origin": "https://" + oldSite}); r.status != 200 {
				t.Fatalf("old /v1/: %d %s", r.status, r.body)
			}
			// The content host and the app itself never move.
			for _, h := range []string{pcContentHost, pcSiteDomain, "cname." + pcSiteDomain, "www." + pcSiteDomain, "lab." + pcSiteDomain, "x.lab." + pcSiteDomain} {
				if loc := a.at(t, "GET", h, "/", nil, nil).header.Get("Location"); strings.Contains(loc, sbBase) {
					t.Fatalf("%s moved: %q", h, loc)
				}
			}
			// Old content-host links go to the address handed out; 301 once permanent.
			r = a.at(t, "GET", pcContentHost, "/internal/site-redirect/"+oh+"/shop/sub/?q=1", nil, nil)
			wantStatus := http.StatusFound
			if move == "permanent" {
				wantStatus = http.StatusMovedPermanently
			}
			if r.status != wantStatus || r.header.Get("Location") != "https://shop."+oh+"."+handout+"/sub/?q=1" {
				t.Fatalf("content-host redirect: %d %q", r.status, r.header.Get("Location"))
			}

			// ---- the new base --------------------------------------------------
			newSite := "shop." + oh + "." + sbBase
			newPerson := oh + "." + sbBase
			r = a.at(t, "GET", newSite, "/", nil, nil)
			if split != (r.status == 200 && string(r.body) == "<h1>shop</h1>") {
				t.Fatalf("new site host: %d %s", r.status, r.body)
			}
			r = a.at(t, "GET", newPerson, "/", nil, nil)
			if split != (r.status == 200) {
				t.Fatalf("new person host: %d", r.status)
			}
			if split {
				// Person path under the base goes to the site host under the base.
				if r := a.at(t, "GET", newPerson, "/shop/x?y=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+newSite+"/x?y=1" {
					t.Fatalf("new person path: %d %q", r.status, r.header.Get("Location"))
				}
				// No certificate under the base: the person path serves there.
				if r := a.at(t, "GET", sh+"."+sbBase, "/shop/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
					t.Fatalf("not-ready new person path: %d %q", r.status, r.header.Get("Location"))
				}
				if r := a.at(t, "GET", "shop."+sh+"."+sbBase, "/x", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+sh+"."+sbBase+"/shop/x" {
					t.Fatalf("not-ready new site host: %d %q", r.status, r.header.Get("Location"))
				}
				// The API on the new site host, same-origin.
				if r := a.at(t, "GET", newSite, "/v1/sites/shop/state", nil, map[string]string{"Origin": "https://" + newSite}); r.status != 200 {
					t.Fatalf("new /v1/: %d %s", r.status, r.body)
				}
				// Its apex, www and reserved names are the app's.
				for _, h := range []string{sbBase, "www." + sbBase, "sites." + sbBase, "cname." + sbBase} {
					if r := a.at(t, "GET", h, "/x", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != "https://"+pcSiteDomain+"/" {
						t.Fatalf("%s: %d %q", h, r.status, r.header.Get("Location"))
					}
				}
				if !a.sites.isVisitorApexHost(sbBase) || !a.sites.isVisitorApexHost("www."+sbBase) {
					t.Fatal("base apex is not the app's")
				}
			} else if a.sites.isVisitorApexHost(sbBase) {
				t.Fatal("base apex while off")
			}

			// ---- a claimed free name ---------------------------------------------
			free := "clay" + fmt.Sprint(time.Now().UnixNano()%1e9)
			asked := free + "." + pcSiteDomain
			if split {
				asked = free + "." + sbBase // what a newer skill sends
			}
			r = a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": asked}, okey)
			if !split {
				if r.status != 200 || r.json(t)["domain"] != free+"."+pcSiteDomain {
					t.Fatalf("claim: %d %s", r.status, r.body)
				}
			} else if r.status != 200 || r.json(t)["domain"] != free+"."+handout {
				t.Fatalf("claim stored as the handed-out form: %d %s", r.status, r.body)
			}
			for _, b := range []string{pcSiteDomain, sbBase} {
				r := a.at(t, "GET", free+"."+b, "/?k=v", nil, nil)
				switch {
				case b == pcSiteDomain && redirect:
					if r.status != status || r.header.Get("Location") != "https://"+free+"."+sbBase+"/?k=v" {
						t.Fatalf("old claimed name: %d %q", r.status, r.header.Get("Location"))
					}
				case b == sbBase && !split:
					if r.status == 200 && string(r.body) == "<h1>shop</h1>" {
						t.Fatalf("claimed name under the base while off")
					}
				default:
					if r.status != 200 || string(r.body) != "<h1>shop</h1>" {
						t.Fatalf("claimed name on %s: %d", b, r.status)
					}
				}
			}
			// One namespace: the name is taken under either base, as a handle too.
			if taken, err := db.HandleInUse(context.Background(), a.database, "", free); err != nil || !taken {
				t.Fatalf("claimed name free as a handle: %v %v", taken, err)
			}
			r = a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free + "." + pcSiteDomain}, map[string]string{"X-API-Key": oscar.key})
			if r.status != http.StatusConflict {
				t.Fatalf("second claim of the same name: %d %s", r.status, r.body)
			}
			if split {
				if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free + "." + sbBase}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
					t.Fatalf("second claim, other form: %d %s", r.status, r.body)
				}
			}

			// ---- certificate requests --------------------------------------------
			a.sites.requestSiteCertsForAll(context.Background())
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
		p := a.newPerson(t, "newbie")
		a.deploy(t, p, "shop")
		_, h := a.userID(t, p)
		a.sites.requestSiteCertsForAll(context.Background())
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
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	markReady(t, baseDir, oh)
	id := a.siteID(t, olive, "shop")
	ctx := context.Background()
	for _, host := range []string{"shop." + oh + "." + pcSiteDomain, "shop." + oh + "." + sbBase} {
		if !a.sites.originIsSiteHostID(ctx, id, host) {
			t.Errorf("site host %s refused", host)
		}
	}
	for _, host := range []string{oh + "." + pcSiteDomain, oh + "." + sbBase} {
		if !a.sites.originIsPersonHostID(ctx, id, host) {
			t.Errorf("person host %s refused", host)
		}
	}
	for _, host := range []string{"blog." + oh + "." + sbBase, "shop.someone-else." + sbBase, "someone-else." + sbBase, "shop." + oh + ".evil.test"} {
		if a.sites.originIsSiteHostID(ctx, id, host) || a.sites.originIsPersonHostID(ctx, id, host) {
			t.Errorf("%s accepted", host)
		}
	}
	if _, err := a.database.Exec(`UPDATE sites SET allowed_origins = 'https://pinned.`+pcSiteDomain+`' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if !a.sites.originAllowedForSiteID(ctx, id, "https://pinned."+sbBase) || a.sites.originAllowedForSiteID(ctx, id, "http://pinned."+sbBase) ||
		a.sites.originAllowedForSiteID(ctx, id, "https://other."+sbBase) {
		t.Fatal("allow-list twin")
	}
	a.sites.SetSiteBase(sbBase, "off", baseDir)
	if a.sites.originAllowedForSiteID(ctx, id, "https://pinned."+sbBase) || a.sites.originIsPersonHostID(ctx, id, oh+"."+sbBase) {
		t.Fatal("twin accepted while off")
	}
}

// TestSiteBaseReturnTo: sign-in may return to a person, site or claimed host
// under either base while it moves, and never to the base's apex, www or an
// unclaimed name under it.
func TestSiteBaseReturnTo(t *testing.T) {
	a, dir, baseDir := newBaseApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "shop") // no certificate: lives on its person path
	a.deploy(t, olive, "blog") // holds the claimed name
	_, oh := a.userID(t, olive)
	_, sh := a.userID(t, oscar)
	markReady(t, dir, oh)
	markReady(t, baseDir, oh)
	free := "rt" + fmt.Sprint(time.Now().UnixNano()%1e9)
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": free + "." + pcSiteDomain}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	cfg := config.Config{SiteDomain: pcSiteDomain, ContentHost: pcContentHost, CNAMETarget: "cname." + pcSiteDomain, PublicBaseURL: "https://" + pcSiteDomain}
	o := NewOAuthHandler(a.database, cfg)
	o.SetPersonSiteResolver(a.sites.PersonReturnSite)
	check := func(raw string, ok bool) {
		t.Helper()
		_, site, host, _, err := o.sanitizeReturnTo(context.Background(), raw)
		if ok != (err == nil) {
			t.Errorf("%s: err %v", raw, err)
		}
		if ok && (!site.Valid || !strings.Contains(raw, host)) {
			t.Errorf("%s: site %v host %q", raw, site, host)
		}
	}
	o.SetSiteBases(a.sites.ServedBases(), a.sites.SameUserHost)
	check("https://"+sh+"."+sbBase+"/shop/", true)
	check("https://"+sh+"."+pcSiteDomain+"/shop/", true)
	check("https://shop."+oh+"."+sbBase+"/", true)
	check("https://shop."+oh+"."+pcSiteDomain+"/", true)
	// A site on its own site host signs in there, not on the person path.
	check("https://"+oh+"."+sbBase+"/shop/", false)
	check("https://"+free+"."+pcSiteDomain+"/", true)
	check("https://"+free+"."+sbBase+"/", true)
	check("https://"+sbBase+"/", false)
	check("https://www."+sbBase+"/", false)
	check("https://unclaimed-name."+sbBase+"/", false)
	check("https://nope."+oh+"."+sbBase+"/", false)
	check("https://x.y.z."+sbBase+"/", false)
}

// TestSiteBaseNormalizeDomain: a custom domain may never be one of our zones,
// the base included even before the move is on.
func TestSiteBaseNormalizeDomain(t *testing.T) {
	h := &SiteHandler{siteDomain: "simple-host.app", contentHost: "sites.simple-host.app"}
	h.SetSiteBase("simple-host.site", "off", "")
	for _, d := range []string{"simple-host.site", "x.simple-host.site", "a.b.simple-host.site", "x.simple-host.app"} {
		if _, err := h.normalizeDomain(d); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
	if _, err := h.normalizeDomain("shop.example.com"); err != nil {
		t.Fatal(err)
	}
}

// TestSiteBaseDefaultsUnchanged is the self-host matrix: an install that sets
// nothing (SITE_BASE_DOMAIN = SITE_DOMAIN, SITE_BASE_MOVE off), or sets the
// base but leaves the move off, answers every host kind byte for byte as one
// that never heard of the move.
func TestSiteBaseDefaultsUnchanged(t *testing.T) {
	type answer struct {
		status          int
		location, cache string
		body            string
	}
	run := func(setup func(a *privateApp, baseDir string)) map[string]answer {
		a, dir := newSiteApp(t, "canonical")
		baseDir := t.TempDir()
		setup(a, baseDir)
		db.SetPlatformDomains(a.sites.HandoutBase(), a.sites.ServedBases()...)
		olive := a.newPerson(t, "olive")
		a.deploy(t, olive, "shop")
		a.deploy(t, olive, "blog")
		uid, oh := a.userID(t, olive)
		markReady(t, dir, oh)
		free := "dflt" + fmt.Sprint(time.Now().UnixNano()%1e9)
		if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": free + "." + pcSiteDomain}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
			t.Fatalf("claim: %d %s", r.status, r.body)
		}
		// Names differ per run: compare with them replaced.
		norm := func(s string) string {
			s = nonceRe.ReplaceAllString(s, `nonce="-"`)
			return strings.NewReplacer(oh, "<h>", free, "<free>", uid, "<uid>").Replace(s)
		}
		out := map[string]answer{}
		hosts := []string{pcSiteDomain, "www." + pcSiteDomain, pcContentHost, "cname." + pcSiteDomain, oh + "." + pcSiteDomain,
			"shop." + oh + "." + pcSiteDomain, free + "." + pcSiteDomain, "unclaimed-zz." + pcSiteDomain, "x.lab." + pcSiteDomain,
			sbBase, "www." + sbBase, oh + "." + sbBase, "shop." + oh + "." + sbBase, free + "." + sbBase, "shop.example.com"}
		paths := []string{"/", "/shop/", "/x?y=1", "/a%2Fb/c", "//evil.example/x", "/v1/sites/shop/state", "/internal/site-redirect/" + oh + "/shop/x?q=1"}
		for _, h := range hosts {
			for _, p := range paths {
				r := a.at(t, "GET", h, p, nil, map[string]string{"Origin": "https://" + h})
				body := string(r.body)
				if len(body) > 400 {
					body = body[:400]
				}
				out[norm(h+p)] = answer{r.status, norm(r.header.Get("Location")), r.header.Get("Cache-Control"), norm(body)}
			}
		}
		r := a.at(t, "GET", pcSiteDomain, "/v1/sites", nil, map[string]string{"X-API-Key": olive.key})
		var urls []string
		for _, f := range strings.Split(string(r.body), `"`) {
			if strings.HasPrefix(f, "https://") {
				urls = append(urls, norm(f))
			}
		}
		sort.Strings(urls)
		out["site urls"] = answer{r.status, strings.Join(urls, " "), "", ""}
		return out
	}
	before := run(func(a *privateApp, _ string) {})
	for name, setup := range map[string]func(a *privateApp, baseDir string){
		"base unset": func(a *privateApp, _ string) { a.sites.SetSiteBase(pcSiteDomain, "off", "") },
		"base = SITE_DOMAIN, any move": func(a *privateApp, d string) {
			a.sites.SetSiteBase(pcSiteDomain, "permanent", d)
		},
		"base set, move off": func(a *privateApp, d string) { a.sites.SetSiteBase(sbBase, "off", d) },
	} {
		after := run(setup)
		for k, want := range before {
			if got := after[k]; got != want {
				t.Errorf("%s: %s: %+v, was %+v", name, k, got, want)
			}
		}
	}
}

var nonceRe = regexp.MustCompile(`nonce="[^"]*"`)

// TestMoveSiteBase: the data rewrite (dry run counts, apply, idempotent) never
// touches custom domains, reserved names or multi-label names, and lookups
// find a name in either form before and after.
func TestMoveSiteBase(t *testing.T) {
	a, _, _ := newBaseApp(t, "serve")
	ctx := context.Background()
	// Domains of this run only, so other tests' names are never moved.
	n0 := fmt.Sprint(time.Now().UnixNano() % 1e9)
	from, to := "from"+n0+".test", "to"+n0+".test"
	db.SetPlatformDomains(to, from)
	olive := a.newPerson(t, "olive")
	for _, s := range []string{"one", "two", "three", "four"} {
		a.deploy(t, olive, s)
	}
	uid, _ := a.userID(t, olive)
	n := fmt.Sprint(time.Now().UnixNano() % 1e9)
	set := func(site, cur, prev string) {
		t.Helper()
		var p sql.NullString
		if prev != "" {
			p = sql.NullString{String: prev, Valid: true}
		}
		if _, err := a.database.Exec(`UPDATE sites SET custom_domain = $2, previous_domain = $3, domain_verified_at = now() WHERE user_id = $1 AND name = $4`, uid, cur, p, site); err != nil {
			t.Fatal(err)
		}
	}
	set("one", "mv"+n+"."+from, "old"+n+"."+from)
	set("two", "shop"+n+".example.com", "")
	set("three", "a.b"+n+"."+from, "")
	set("four", "cname."+from, "")
	if _, err := a.database.Exec(`INSERT INTO legacy_hostnames (hostname, site_id, user_id) VALUES ($1, NULL, $2)`, "gone"+n+"."+from, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSiteByCustomDomain(ctx, a.database, "mv"+n+"."+to); err != nil {
		t.Fatalf("lookup of the new form before the move: %v", err)
	}
	dry, err := db.MoveSiteBase(ctx, a.database, from, to, LabelReserved, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.CustomDomains < 1 || dry.PreviousDomains < 1 || dry.RetiredNames < 1 {
		t.Fatalf("dry run: %+v", dry)
	}
	var cur string
	a.database.QueryRow(`SELECT custom_domain FROM sites WHERE user_id = $1 AND name = 'one'`, uid).Scan(&cur)
	if cur != "mv"+n+"."+from {
		t.Fatalf("dry run wrote: %s", cur)
	}
	got, err := db.MoveSiteBase(ctx, a.database, from, to, LabelReserved, true)
	if err != nil || got.CustomDomains != dry.CustomDomains || got.RetiredNames != dry.RetiredNames {
		t.Fatalf("apply: %+v %v (dry %+v)", got, err, dry)
	}
	want := map[string]string{"one": "mv" + n + "." + to, "two": "shop" + n + ".example.com", "three": "a.b" + n + "." + from, "four": "cname." + from}
	for site, w := range want {
		var c string
		a.database.QueryRow(`SELECT custom_domain FROM sites WHERE user_id = $1 AND name = $2`, uid, site).Scan(&c)
		if c != w {
			t.Errorf("%s: %s, want %s", site, c, w)
		}
	}
	var prev string
	a.database.QueryRow(`SELECT previous_domain FROM sites WHERE user_id = $1 AND name = 'one'`, uid).Scan(&prev)
	if prev != "old"+n+"."+to {
		t.Errorf("previous: %s", prev)
	}
	if _, err := db.GetRetiredName(ctx, a.database, "gone"+n+"."+from); err != nil {
		t.Errorf("retired name under the old form: %v", err)
	}
	if _, err := db.GetSiteByCustomDomain(ctx, a.database, "mv"+n+"."+from); err != nil {
		t.Errorf("lookup of the old form after the move: %v", err)
	}
	again, err := db.MoveSiteBase(ctx, a.database, from, to, LabelReserved, true)
	if err != nil || again.CustomDomains+again.PreviousDomains+again.RetiredNames != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	if _, err := db.MoveSiteBase(ctx, a.database, from, from, nil, false); err == nil {
		t.Fatal("same domain accepted")
	}
}
