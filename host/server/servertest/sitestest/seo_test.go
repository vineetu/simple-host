package sitestest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/sitemap"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"
)

func hostedService(t *testing.T) {
	old := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = old })
}

func (a *App) sitemap(method string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	sitemap.Serve(a.Sites.Handler, &a.Sites.Sitemaps)(w, httptest.NewRequest(method, "https://"+rt.SiteDomain+"/sitemap.xml", nil))
	return w
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
		if left := time.Until(cache.Expires); left > sitemap.SitemapTTL || left < sitemap.SitemapTTL-time.Minute {
			t.Fatalf("cache expiry %v, want about %v", left, sitemap.SitemapTTL)
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
