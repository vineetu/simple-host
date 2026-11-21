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

	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/host/web"
)

func TestSitemapFilterErrorIsNotCached(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	old := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = old })
	owner := a.NewPerson(t, "sitemap-filter")
	a.Deploy(t, owner, "open")
	calls := 0
	fail := true
	a.Sites.SetSitemapFilter(func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error) {
		calls++
		if q != a.Database || len(sites) == 0 {
			t.Fatal("filter did not receive the database and candidate sites")
		}
		if fail {
			return sites, errors.New("test sitemap filter failure")
		}
		return sites, nil
	})
	for i, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		a.Sites.ServeSitemap(w, httptest.NewRequest(method, "https://simple-host.app/sitemap.xml", nil))
		if w.Code != http.StatusServiceUnavailable || w.Body.String() != "sitemap unavailable\n" || calls != i+1 || len(a.Sites.Sitemaps.ByBrand) != 0 {
			t.Fatalf("filter error: status=%d body=%q calls=%d cached=%d", w.Code, w.Body.String(), calls, len(a.Sites.Sitemaps.ByBrand))
		}
	}
	fail = false
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		a.Sites.ServeSitemap(w, httptest.NewRequest(method, "https://simple-host.app/sitemap.xml", nil))
		if w.Code != http.StatusOK || calls != 3 || len(a.Sites.Sitemaps.ByBrand) != 1 {
			t.Fatalf("recovered filter: status=%d calls=%d cached=%d", w.Code, calls, len(a.Sites.Sitemaps.ByBrand))
		}
	}
}

func TestPlatformSitemapNoProductQueries(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	owner := a.NewPerson(t, "sitemap-platform")
	a.Deploy(t, owner, "open")
	db, recorder := testdb.OpenRecording(os.Getenv("DB_DSN"))
	t.Cleanup(func() { db.Close() })
	a.Sites.Database = db
	a.Access.Database, a.Data.Database = a.Sites.Database, a.Sites.Database
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

// Database filters and disk markers both exclude restricted sites. Uses the
// same fixture and routes as the public showcase tests; DB_DSN is optional.
func TestHostedSitemapSites(t *testing.T) {
	a, certDir, famDir, dns := newFamilyApp(t)
	siteshttp.RegisterUIRoutes(a.Mux, a.Srv.URL, a.Sites)
	old := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = old })
	owner := a.NewPerson(t, "sitemap")
	uid, handle := a.UserID(t, owner)
	servertest.MarkReady(t, certDir, handle)
	names := []string{"open", "listed", "locked", "viewers", "offline", "down", "deleted", "robots", "meta", "marker-lock", "marker-off", "marker-down", "missing", "custom", "claimed", "pending", "family"}
	for _, name := range names {
		a.Deploy(t, owner, name)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := a.Database.Exec(query, args...); err != nil {
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
		if err := os.WriteFile(filepath.Join(a.Sites.Disk.SiteDir(uid, name), path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("robots", "current/robots.txt", "User-agent: *\nDisallow: /\n")
	write("meta", "current/index.html", `<meta name="robots" content="noindex">`)
	write("open", "unlisted", "") // old markers have no effect, even before restart
	for name, marker := range map[string]string{"marker-lock": "passcode", "marker-off": "offline", "marker-down": "suspended"} {
		write(name, marker, "")
	}
	if err := os.Remove(filepath.Join(a.Sites.Disk.SiteDir(uid, "missing"), "current/index.html")); err != nil {
		t.Fatal(err)
	}
	suspended := a.NewPerson(t, "sitemap-suspended")
	a.Deploy(t, suspended, "owner-down")
	sid, sh := a.UserID(t, suspended)
	servertest.MarkReady(t, certDir, sh)
	exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, sid)
	a.Access.SyncSuspendMarkers(t.Context())
	// Disk-only restrictions can precede a database update; honour them too.
	for name, marker := range map[string]string{"marker-lock": "passcode", "marker-off": "offline", "marker-down": "suspended"} {
		write(name, marker, "")
	}
	a.LiveFamily(t, famDir, dns, owner, "sitemap.fam.test", "fam", true, 1)
	read := func(method string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		a.Sites.ServeSitemap(r, httptest.NewRequest(method, "https://"+servertest.PcSiteDomain+"/sitemap.xml", nil))
		return r
	}
	result := read("GET")
	if result.Code != 200 {
		t.Fatalf("sitemap: %d %s", result.Code, result.Body)
	}
	body := result.Body.String()
	for _, url := range []string{"https://open." + handle + "." + servertest.PcSiteDomain + "/", "https://listed." + handle + "." + servertest.PcSiteDomain + "/", "https://outside.example.org/", "https://claimed.simple-host.app/", "https://previous.example.org/", "https://ily.sitemap.fam.test/"} {
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
		r := a.At(t, "GET", name+"."+handle+"."+servertest.PcSiteDomain, "/", nil, nil)
		if !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") || (name == "deleted" && r.Status != 404) {
			t.Errorf("%s: %d %q", name, r.Status, r.Header.Get("X-Robots-Tag"))
		}
	}
	r := a.At(t, "GET", "owner-down."+sh+"."+servertest.PcSiteDomain, "/", nil, nil)
	if !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("suspended owner: %d %q", r.Status, r.Header.Get("X-Robots-Tag"))
	}
	r = a.At(t, "GET", "robots."+handle+"."+servertest.PcSiteDomain, "/robots.txt", nil, nil)
	if r.Status != 200 || string(r.Body) != "User-agent: *\nDisallow: /\n" {
		t.Fatalf("own robots: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", "meta."+handle+"."+servertest.PcSiteDomain, "/", nil, nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), `content="noindex"`) || r.Header.Get("X-Robots-Tag") != "" {
		t.Fatalf("own meta: %d %q", r.Status, r.Body)
	}
	// Child sitemap routes also work through the registered HTTP mux in single mode.
	for _, path := range []string{"/sitemap-pages.xml", "/sitemap-sites-1.xml"} {
		child := a.At(t, "GET", servertest.PcSiteDomain, path, nil, nil)
		if child.Status != 200 || !strings.Contains(string(child.Body), "<urlset") {
			t.Fatalf("child %s: %d %s", path, child.Status, child.Body)
		}
	}

	// Restricted redirects keep noindex even before a site's certificate is ready.
	if err := os.Remove(filepath.Join(certDir, "ready", handle)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"locked", "viewers", "offline", "down"} {
		for _, address := range []struct{ host, path string }{{name + "." + handle + "." + servertest.PcSiteDomain, "/"}, {handle + "." + servertest.PcSiteDomain, "/" + name + "/"}} {
			r := a.At(t, "GET", address.host, address.path, nil, nil)
			if !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") {
				t.Errorf("restricted redirect %s%s: %d %q", address.host, address.path, r.Status, r.Header.Get("X-Robots-Tag"))
			}
		}
	}
	// A warm HEAD uses only cached bytes, even if the database is unavailable.
	savedDB := a.Sites.Database
	a.Sites.Database = nil
	a.Access.Database, a.Data.Database = a.Sites.Database, a.Sites.Database
	head := read("HEAD")
	a.Sites.Database = savedDB
	a.Access.Database, a.Data.Database = a.Sites.Database, a.Sites.Database
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("HEAD cache miss")
	}
	// Self-hosted uses the same sitemap generator but never enumerates sites.
	web.InstanceIsHostedService = false
	a.Sites.Sitemaps.ByBrand = nil
	if r := read("GET"); r.Code != 200 || strings.Contains(r.Body.String(), "https://open."+handle) {
		t.Fatalf("self-hosted enumeration: %d %s", r.Code, r.Body)
	}
	// Expiry is SitemapTTL (short, so a newly gated site leaves soon), with
	// one cache per brand.
	for _, cache := range a.Sites.Sitemaps.ByBrand {
		if left := time.Until(cache.Expires); left > siteshttp.SitemapTTL || left < siteshttp.SitemapTTL-time.Minute {
			t.Fatalf("cache expiry %v, want about %v", left, siteshttp.SitemapTTL)
		}
	}
}
