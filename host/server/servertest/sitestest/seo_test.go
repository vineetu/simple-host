package sitestest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/host/web"
)

func hostedService(t *testing.T) {
	old := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = old })
}

func (a *App) sitemap(method string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.Sites.ServeSitemap(w, httptest.NewRequest(method, "https://"+rt.SiteDomain+"/sitemap.xml", nil))
	return w
}

// A failing product sitemap filter answers 503 and is not cached.
func TestSitemapFilterErrorIsNotCached(t *testing.T) {
	a := NewApp(t)
	hostedService(t)
	a.Deploy(t, a.NewPerson(t, "sitemap-filter"), "open")
	calls, fail := 0, true
	a.Sites.SetSitemapFilter(func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error) {
		calls++
		if fail {
			return sites, errors.New("test sitemap filter failure")
		}
		return sites, nil
	})
	if w := a.sitemap("GET"); w.Code != http.StatusServiceUnavailable || calls != 1 || len(a.Sites.Sitemaps.ByBrand) != 0 {
		t.Fatalf("filter error: status=%d calls=%d cached=%d", w.Code, calls, len(a.Sites.Sitemaps.ByBrand))
	}
	fail = false
	for range 2 {
		if w := a.sitemap("GET"); w.Code != http.StatusOK || calls != 2 || len(a.Sites.Sitemaps.ByBrand) != 1 {
			t.Fatalf("recovered filter: status=%d calls=%d cached=%d", w.Code, calls, len(a.Sites.Sitemaps.ByBrand))
		}
	}
}

// The platform's sitemap query never touches a product's tables.
func TestPlatformSitemapNoProductQueries(t *testing.T) {
	a := NewApp(t)
	a.Deploy(t, a.NewPerson(t, "sitemap-platform"), "open")
	db, recorder := testdb.OpenRecording(os.Getenv("DB_DSN"))
	t.Cleanup(func() { db.Close() })
	a.Sites.Database = db
	sites, err := a.Sites.SitesSvc().ListSitemapSites(t.Context())
	if err != nil || len(sites) == 0 {
		t.Fatalf("platform sitemap: %d sites, %v", len(sites), err)
	}
	if len(recorder.Statements()) != 1 || !recorder.Names("sites") {
		t.Fatalf("expected one platform query, got %v", recorder.Statements())
	}
	objects, err := os.ReadFile("../../../../scripts/split/hack-objects.txt")
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

// The sitemap lists each servable, indexable site at its home address and
// leaves out anything restricted, by the database or by a disk marker.
func TestHostedSitemapSites(t *testing.T) {
	a := NewApp(t)
	siteshttp.RegisterUIRoutes(a.Mux, a.Srv.URL, a.Sites)
	hostedService(t)
	owner := a.NewPerson(t, "sitemap")
	for _, name := range []string{"open", "listed", "locked", "viewers", "offline", "down", "deleted", "robots", "meta", "marker-lock", "marker-off", "marker-down", "missing", "custom", "claimed", "pending"} {
		a.Deploy(t, owner, name)
	}
	exec := func(query string) {
		t.Helper()
		if _, err := a.Database.Exec(query, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE sites SET visibility='public' WHERE user_id=$1 AND name='listed'`)
	exec(`UPDATE sites SET passcode_enc='x'::bytea WHERE user_id=$1 AND name='locked'`)
	exec(`UPDATE sites SET access='specific' WHERE user_id=$1 AND name='viewers'`)
	exec(`UPDATE sites SET offline_at=now() WHERE user_id=$1 AND name='offline'`)
	exec(`UPDATE sites SET suspended_at=now() WHERE user_id=$1 AND name='down'`)
	exec(`UPDATE sites SET deleted_at=now() WHERE user_id=$1 AND name='deleted'`)
	exec(`UPDATE sites SET custom_domain='outside.example.org',domain_verified_at=now(),domain_status='active' WHERE user_id=$1 AND name='custom'`)
	exec(`UPDATE sites SET custom_domain='claimed.simple-host.app',domain_verified_at=now(),domain_status='active' WHERE user_id=$1 AND name='claimed'`)
	exec(`UPDATE sites SET custom_domain='pending.example.org',previous_domain='previous.example.org',domain_status='pending' WHERE user_id=$1 AND name='pending'`)
	a.writeLive(t, owner, "robots", "robots.txt", "User-agent: *\nDisallow: /\n")
	a.writeLive(t, owner, "meta", "index.html", `<meta name="robots" content="noindex">`)
	for name, marker := range map[string]string{"marker-lock": "passcode", "marker-off": "offline", "marker-down": "suspended"} {
		if err := os.WriteFile(filepath.Join(a.siteDir(owner, name), marker), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(a.siteDir(owner, "missing"), "current/index.html")); err != nil {
		t.Fatal(err)
	}
	suspended := a.NewPerson(t, "sitemap-suspended")
	a.Deploy(t, suspended, "owner-down")
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, suspended.ID); err != nil {
		t.Fatal(err)
	}

	result := a.sitemap("GET")
	body := result.Body.String()
	if result.Code != 200 || !strings.Contains(body, "<lastmod>") {
		t.Fatalf("sitemap: %d %s", result.Code, body)
	}
	for _, url := range []string{"https://" + owner.SiteHost("open") + "/", "https://" + owner.SiteHost("listed") + "/", "https://outside.example.org/", "https://claimed.simple-host.app/", "https://previous.example.org/"} {
		if !strings.Contains(body, "<loc>"+url+"</loc>") {
			t.Errorf("missing %s", url)
		}
	}
	for _, name := range []string{"locked", "viewers", "offline", "down", "deleted", "robots", "meta", "marker-lock", "marker-off", "marker-down", "missing", "custom", "claimed", "pending"} {
		if strings.Contains(body, "https://"+owner.SiteHost(name)) {
			t.Errorf("included %s", name)
		}
	}
	if strings.Contains(body, suspended.SiteHost("owner-down")) {
		t.Error("included a suspended owner's site")
	}
	for _, path := range []string{"/sitemap-pages.xml", "/sitemap-sites-1.xml"} {
		if child := a.At(t, "GET", rt.SiteDomain, path, nil, nil); child.Status != 200 || !strings.Contains(string(child.Body), "<urlset") {
			t.Fatalf("child %s: %d %s", path, child.Status, child.Body)
		}
	}
	// A warm HEAD uses only cached bytes, even if the database is unavailable.
	saved := a.Sites.Database
	a.Sites.Database = nil
	head := a.sitemap("HEAD")
	a.Sites.Database = saved
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("HEAD cache miss")
	}
	for _, cache := range a.Sites.Sitemaps.ByBrand {
		if left := time.Until(cache.Expires); left > siteshttp.SitemapTTL || left < siteshttp.SitemapTTL-time.Minute {
			t.Fatalf("cache expiry %v, want about %v", left, siteshttp.SitemapTTL)
		}
	}
	// Self-hosted uses the same generator but never enumerates sites.
	web.InstanceIsHostedService = false
	a.Sites.Sitemaps.ByBrand = nil
	if r := a.sitemap("GET"); r.Code != 200 || strings.Contains(r.Body.String(), owner.SiteHost("open")) {
		t.Fatalf("self-hosted enumeration: %d %s", r.Code, r.Body)
	}
}

// Profile listing (public or unlisted) never changes search indexing, and a
// site's own robots.txt is served as supplied.
func TestProfileListingIndexableEveryAddress(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	if r := a.files(t, olive, "POST", "blog", map[string]string{"index.html": "<h1>blog</h1>", "robots.txt": "User-agent: *\nAllow: /own\n"}); r.Status != http.StatusCreated {
		t.Fatalf("deploy blog: %d %s", r.Status, r.Body)
	}
	// A leftover marker from an older binary has no serving effect.
	if err := os.WriteFile(filepath.Join(a.siteDir(olive, "shop"), "unlisted"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	person := olive.Handle + "." + rt.SiteDomain
	check := func(when string) {
		t.Helper()
		for _, x := range []struct{ host, path string }{{person, "/shop/"}, {rt.ContentHost, "/internal/site-redirect/" + olive.Handle + "/shop/"}, {olive.SiteHost("shop"), "/"}, {olive.SiteHost("shop"), "/robots.txt"}} {
			r := a.At(t, "GET", x.host, x.path, nil, nil)
			if r.Header.Get("X-Robots-Tag") != "" || (x.path == "/robots.txt") != (r.Status == 404) || r.Status >= 500 {
				t.Errorf("%s: %s%s: %d X-Robots-Tag %q", when, x.host, x.path, r.Status, r.Header.Get("X-Robots-Tag"))
			}
		}
	}
	check("unlisted")
	for _, vis := range []string{"public", "unlisted"} {
		if r := a.api(t, olive, "PUT", "/v1/sites/shop/visibility", map[string]string{"visibility": vis}); r.Status != 200 {
			t.Fatalf("%s: %d %s", vis, r.Status, r.Body)
		}
		check(vis)
	}
	if r := a.At(t, "GET", olive.SiteHost("blog"), "/robots.txt", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "Allow: /own") || r.Header.Get("X-Robots-Tag") != "" {
		t.Fatalf("own robots.txt: %d %q %q", r.Status, r.Body, r.Header.Get("X-Robots-Tag"))
	}
}
