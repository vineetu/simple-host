package handler

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestSitemapShards(t *testing.T) {
	old := sitemapURLLimit
	sitemapURLLimit = 2
	t.Cleanup(func() { sitemapURLLimit = old })
	pages := []sitemapURL{{Loc: "https://example.org/?x=1&y=2"}}
	sites := []sitemapURL{{Loc: "https://one.example.org/", Lastmod: "2026-11-12T00:00:00Z"}, {Loc: "https://two.example.org/"}, {Loc: "https://three.example.org/"}}
	files := buildSitemaps("https://example.org", pages, sites)
	var index struct {
		Maps []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal(files["/sitemap.xml"], &index); err != nil || len(index.Maps) != 3 {
		t.Fatalf("index: %s (%v)", files["/sitemap.xml"], err)
	}
	for path, want := range map[string]int{"/sitemap-pages.xml": 1, "/sitemap-sites-1.xml": 2, "/sitemap-sites-2.xml": 1} {
		var set struct {
			URLs []sitemapURL `xml:"url"`
		}
		if err := xml.Unmarshal(files[path], &set); err != nil || len(set.URLs) != want {
			t.Fatalf("%s: %s (%v)", path, files[path], err)
		}
		r := httptest.NewRecorder()
		serveSitemapFile(r, httptest.NewRequest("HEAD", path, nil), files)
		if r.Code != 200 || r.Body.Len() != 0 || r.Header().Get("Content-Length") == "" {
			t.Fatalf("HEAD %s: %+v", path, r)
		}
	}
	files = buildSitemaps("https://example.org", pages, sites[:1])
	if !strings.Contains(string(files["/sitemap.xml"]), "<urlset") || !strings.Contains(string(files["/sitemap-pages.xml"]), "&amp;") || !strings.Contains(string(files["/sitemap-sites-1.xml"]), "<lastmod>") {
		t.Fatal("single mode lost child maps, escaping or dates")
	}
	r := httptest.NewRecorder()
	serveSitemapFile(r, httptest.NewRequest("GET", "/sitemap-sites-99.xml", nil), files)
	if r.Code != 404 {
		t.Fatal(r.Code)
	}
}

func TestSitemapFilterErrorIsNotCached(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	old := instanceIsHostedService
	instanceIsHostedService = true
	t.Cleanup(func() { instanceIsHostedService = old })
	owner := a.newPerson(t, "sitemap-filter")
	a.deploy(t, owner, "open")
	calls := 0
	fail := true
	a.sites.SetSitemapFilter(func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error) {
		calls++
		if q != a.database || len(sites) == 0 {
			t.Fatal("filter did not receive the database and candidate sites")
		}
		if fail {
			return sites, errors.New("test sitemap filter failure")
		}
		return sites, nil
	})
	for i, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		a.sites.serveSitemap(w, httptest.NewRequest(method, "https://simple-host.app/sitemap.xml", nil))
		if w.Code != http.StatusServiceUnavailable || w.Body.String() != "sitemap unavailable\n" || calls != i+1 || len(a.sites.sitemaps.byBrand) != 0 {
			t.Fatalf("filter error: status=%d body=%q calls=%d cached=%d", w.Code, w.Body.String(), calls, len(a.sites.sitemaps.byBrand))
		}
	}
	fail = false
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		a.sites.serveSitemap(w, httptest.NewRequest(method, "https://simple-host.app/sitemap.xml", nil))
		if w.Code != http.StatusOK || calls != 3 || len(a.sites.sitemaps.byBrand) != 1 {
			t.Fatalf("recovered filter: status=%d calls=%d cached=%d", w.Code, calls, len(a.sites.sitemaps.byBrand))
		}
	}
}

func TestPlatformSitemapNoProductQueries(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "sitemap-platform")
	a.deploy(t, owner, "open")
	db, recorder := testdb.OpenRecording(os.Getenv("DB_DSN"))
	t.Cleanup(func() { db.Close() })
	a.sites.database = db
	sites, err := a.sites.sitesSvc().ListSitemapSites(t.Context())
	if err != nil || len(sites) == 0 {
		t.Fatalf("platform sitemap: %d sites, %v", len(sites), err)
	}
	if len(recorder.Statements()) != 1 || !recorder.Names("sites") {
		t.Fatalf("expected one platform query, got %v", recorder.Statements())
	}
	objects, err := os.ReadFile("../../scripts/split/hack-objects.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(objects), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) != "table" {
			continue
		}
		if name := strings.TrimSpace(fields[1]); recorder.Names(name) {
			t.Errorf("platform sitemap queried product table %s", name)
		}
	}
}

func TestSiteIndexingPreferences(t *testing.T) {
	for _, tc := range []struct {
		name, robots, html string
		want               bool
	}{
		{"missing index", "", "", false},
		{"open", "", "<h1>hello</h1>", true},
		{"robots root", "User-agent: *\nDisallow: /\n", "hello", false},
		{"multiple agents", "User-agent: Googlebot\nUser-agent: *\nDisallow: / # all\n", "hello", false},
		{"other group", "User-agent: other\nDisallow: /\nUser-agent: *\nAllow: /\n", "hello", true},
		{"following group", "User-agent: *\nDisallow: /private\nUser-agent: other\nDisallow: /\n", "hello", true},
		{"meta case", "", `<META CONTENT="NOINDEX, follow" NAME="RoBoTs">`, false},
		{"googlebot", "", `<meta content='noindex' name='googlebot'/>`, false},
		{"other meta", "", `<meta name='description' content='noindex'>`, true},
		{"googlebot group", "User-agent: Googlebot\nDisallow: /\n", "hello", false},
		{"robots wildcard", "User-agent: *\nDisallow: /*\n", "hello", false},
		{"meta none", "", `<meta name="robots" content="none">`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.html != "" {
				if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(tc.html), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.robots != "" {
				if err := os.WriteFile(filepath.Join(dir, "robots.txt"), []byte(tc.robots), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := siteAllowsIndexing(dir); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// Database filters and disk markers both exclude restricted sites. Uses the
// same fixture and routes as the public showcase tests; DB_DSN is optional.
func TestHostedSitemapSites(t *testing.T) {
	a, certDir, famDir, dns := newFamilyApp(t)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	old := instanceIsHostedService
	instanceIsHostedService = true
	t.Cleanup(func() { instanceIsHostedService = old })
	owner := a.newPerson(t, "sitemap")
	uid, handle := a.userID(t, owner)
	markReady(t, certDir, handle)
	names := []string{"open", "listed", "locked", "viewers", "offline", "down", "deleted", "robots", "meta", "marker-lock", "marker-off", "marker-down", "missing", "custom", "claimed", "pending", "family"}
	for _, name := range names {
		a.deploy(t, owner, name)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := a.database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE sites SET visibility='public' WHERE user_id=$1 AND name='listed'`, uid)
	exec(`UPDATE sites SET passcode_enc='x'::bytea WHERE user_id=$1 AND name='locked'`, uid)
	exec(`UPDATE sites SET access='specific' WHERE user_id=$1 AND name='viewers'`, uid)
	exec(`UPDATE sites SET offline_at=now() WHERE user_id=$1 AND name='offline'`, uid)
	exec(`UPDATE sites SET suspended_at=now() WHERE user_id=$1 AND name='down'`, uid)
	exec(`UPDATE sites SET deleted_at=now() WHERE user_id=$1 AND name='deleted'`, uid)
	exec(`UPDATE sites SET custom_domain='outside.example.org',domain_verified_at=now(),domain_status='active' WHERE user_id=$1 AND name='custom'`, uid)
	exec(`UPDATE sites SET custom_domain='claimed.simple-host.app',domain_verified_at=now(),domain_status='active' WHERE user_id=$1 AND name='claimed'`, uid)
	exec(`UPDATE sites SET custom_domain='pending.example.org',previous_domain='previous.example.org',domain_status='pending' WHERE user_id=$1 AND name='pending'`, uid)
	write := func(name, path, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(a.sites.disk.SiteDir(uid, name), path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("robots", "current/robots.txt", "User-agent: *\nDisallow: /\n")
	write("meta", "current/index.html", `<meta name="robots" content="noindex">`)
	write("open", "unlisted", "") // old markers have no effect, even before restart
	for name, marker := range map[string]string{"marker-lock": "passcode", "marker-off": "offline", "marker-down": "suspended"} {
		write(name, marker, "")
	}
	if err := os.Remove(filepath.Join(a.sites.disk.SiteDir(uid, "missing"), "current/index.html")); err != nil {
		t.Fatal(err)
	}
	suspended := a.newPerson(t, "sitemap-suspended")
	a.deploy(t, suspended, "owner-down")
	sid, sh := a.userID(t, suspended)
	markReady(t, certDir, sh)
	exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, sid)
	a.sites.SyncSuspendMarkers(t.Context())
	// Disk-only restrictions can precede a database update; honour them too.
	for name, marker := range map[string]string{"marker-lock": "passcode", "marker-off": "offline", "marker-down": "suspended"} {
		write(name, marker, "")
	}
	a.liveFamily(t, famDir, dns, owner, "sitemap.fam.test", "fam", true, 1)
	read := func(method string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		a.sites.serveSitemap(r, httptest.NewRequest(method, "https://"+pcSiteDomain+"/sitemap.xml", nil))
		return r
	}
	result := read("GET")
	if result.Code != 200 {
		t.Fatalf("sitemap: %d %s", result.Code, result.Body)
	}
	body := result.Body.String()
	for _, url := range []string{"https://open." + handle + "." + pcSiteDomain + "/", "https://listed." + handle + "." + pcSiteDomain + "/", "https://outside.example.org/", "https://claimed.simple-host.app/", "https://previous.example.org/", "https://ily.sitemap.fam.test/"} {
		if !strings.Contains(body, "<loc>"+url+"</loc>") {
			t.Errorf("missing %s", url)
		}
	}
	if !strings.Contains(body, "<lastmod>") {
		t.Error("missing deploy time")
	}
	for _, name := range []string{"locked", "viewers", "offline", "down", "deleted", "robots", "meta", "marker-lock", "marker-off", "marker-down", "missing", "owner-down", "custom", "claimed", "pending", "family"} {
		if strings.Contains(body, "https://"+name+"."+handle+".") || (name == "owner-down" && strings.Contains(body, "https://owner-down."+sh+".")) {
			t.Errorf("included %s", name)
		}
	}
	for _, name := range []string{"locked", "viewers", "offline", "down", "deleted"} {
		r := a.at(t, "GET", name+"."+handle+"."+pcSiteDomain, "/", nil, nil)
		if !strings.Contains(r.header.Get("X-Robots-Tag"), "noindex") || (name == "deleted" && r.status != 404) {
			t.Errorf("%s: %d %q", name, r.status, r.header.Get("X-Robots-Tag"))
		}
	}
	r := a.at(t, "GET", "owner-down."+sh+"."+pcSiteDomain, "/", nil, nil)
	if !strings.Contains(r.header.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("suspended owner: %d %q", r.status, r.header.Get("X-Robots-Tag"))
	}
	r = a.at(t, "GET", "robots."+handle+"."+pcSiteDomain, "/robots.txt", nil, nil)
	if r.status != 200 || string(r.body) != "User-agent: *\nDisallow: /\n" {
		t.Fatalf("own robots: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", "meta."+handle+"."+pcSiteDomain, "/", nil, nil)
	if r.status != 200 || !strings.Contains(string(r.body), `content="noindex"`) || r.header.Get("X-Robots-Tag") != "" {
		t.Fatalf("own meta: %d %q", r.status, r.body)
	}
	// Child sitemap routes also work through the registered HTTP mux in single mode.
	for _, path := range []string{"/sitemap-pages.xml", "/sitemap-sites-1.xml"} {
		child := a.at(t, "GET", pcSiteDomain, path, nil, nil)
		if child.status != 200 || !strings.Contains(string(child.body), "<urlset") {
			t.Fatalf("child %s: %d %s", path, child.status, child.body)
		}
	}

	// Restricted redirects keep noindex even before a site's certificate is ready.
	if err := os.Remove(filepath.Join(certDir, "ready", handle)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"locked", "viewers", "offline", "down"} {
		for _, address := range []struct{ host, path string }{{name + "." + handle + "." + pcSiteDomain, "/"}, {handle + "." + pcSiteDomain, "/" + name + "/"}} {
			r := a.at(t, "GET", address.host, address.path, nil, nil)
			if !strings.Contains(r.header.Get("X-Robots-Tag"), "noindex") {
				t.Errorf("restricted redirect %s%s: %d %q", address.host, address.path, r.status, r.header.Get("X-Robots-Tag"))
			}
		}
	}
	// A warm HEAD uses only cached bytes, even if the database is unavailable.
	savedDB := a.sites.database
	a.sites.database = nil
	head := read("HEAD")
	a.sites.database = savedDB
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("HEAD cache miss")
	}
	// Self-hosted uses the same sitemap generator but never enumerates sites.
	instanceIsHostedService = false
	a.sites.sitemaps.byBrand = nil
	if r := read("GET"); r.Code != 200 || strings.Contains(r.Body.String(), "https://open."+handle) {
		t.Fatalf("self-hosted enumeration: %d %s", r.Code, r.Body)
	}
	// Expiry is sitemapTTL (short, so a newly gated site leaves soon), with
	// one cache per brand.
	for _, cache := range a.sites.sitemaps.byBrand {
		if left := time.Until(cache.expires); left > sitemapTTL || left < sitemapTTL-time.Minute {
			t.Fatalf("cache expiry %v, want about %v", left, sitemapTTL)
		}
	}
}
