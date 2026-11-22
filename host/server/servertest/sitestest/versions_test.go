package sitestest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// publish=false stores a version without making it live; an owner-only
// preview link opens any kept version on the site's own host (not cached,
// noindex); a forged, expired or foreign link opens nothing; "make live" is
// the rollback; a taken-down site previews nothing.
func TestPreviewBeforeLive(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shopID := a.Deploy(t, olive, "shop")
	blogID := a.Deploy(t, oscar, "blog")
	if err := os.Remove(filepath.Join(a.CertDir, "ready", oscar.Handle)); err != nil {
		t.Fatal(err)
	}
	shop := olive.SiteHost("shop")
	v2 := map[string]any{"files": map[string]string{"index.html": "v2", "sub/index.html": "sub2"}}
	if r := a.api(t, olive, "POST", "/v1/sites/fresh/files?publish=false", v2); r.Status != 400 {
		t.Fatalf("create unpublished: %d %s", r.Status, r.Body)
	}
	r := a.api(t, olive, "PUT", "/v1/sites/shop/files?publish=false", v2)
	body := r.JSON(t)
	link, _ := body["preview_url"].(string)
	if r.Status != 200 || body["unpublished_version"] != float64(2) || body["active_version"] != float64(1) || !strings.HasPrefix(link, "https://"+shop+"/__preview/2/") {
		t.Fatalf("unpublished deploy: %d %s", r.Status, r.Body)
	}
	path := strings.TrimPrefix(link, "https://"+shop)
	if r := a.At(t, "GET", shop, "/", nil, nil); string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("live changed: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", shop, path, nil, nil)
	if r.Status != 200 || string(r.Body) != "v2" || r.Header.Get("Cache-Control") != "no-store" || !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("preview: %d %s %v", r.Status, r.Body, r.Header)
	}
	checkPageRedirect(t, a, shop, path+"index.html/", path+"index.html", "no-store")
	if r := a.At(t, "GET", shop, path+"sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub2" {
		t.Fatalf("preview sub: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{
		strings.Replace(path, "/__preview/2/", "/__preview/1/", 1),
		path[:len(path)-3] + "x/",
		"/__preview/2/" + a.Sites.SignPreviewToken(shopID, 2, time.Now().Add(-time.Minute)) + "/",
		"/__preview/1/" + a.Sites.SignPreviewToken(blogID, 1, time.Now().Add(time.Minute)) + "/",
	} {
		if r := a.At(t, "GET", shop, p, nil, nil); r.Status != 404 {
			t.Errorf("GET %s: %d %s", p, r.Status, r.Body)
		}
	}
	if r := a.api(t, olive, "POST", "/v1/sites/shop/versions/9/preview-link", nil); r.Status != 404 {
		t.Fatalf("missing version: %d", r.Status)
	}
	if r := a.api(t, oscar, "POST", "/v1/sites/shop/versions/1/preview-link", nil); r.Status != 404 {
		t.Fatalf("someone else's site: %d", r.Status)
	}
	// A person without a certificate yet previews on the person path.
	r = a.api(t, oscar, "POST", "/v1/sites/blog/versions/1/preview-link", nil)
	oscarPerson := oscar.Handle + "." + rt.SiteDomain
	plink, _ := r.JSON(t)["url"].(string)
	if r.Status != 200 || !strings.HasPrefix(plink, "https://"+oscarPerson+"/blog/__preview/1/") {
		t.Fatalf("person-path link: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", oscarPerson, strings.TrimPrefix(plink, "https://"+oscarPerson), nil, nil); r.Status != 200 || string(r.Body) != "<h1>blog</h1>" {
		t.Fatalf("person-path preview: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "PUT", "/v1/sites/shop/active-version", map[string]int{"version_number": 2}); r.Status != 200 {
		t.Fatalf("make live: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/", nil, nil); string(r.Body) != "v2" {
		t.Fatalf("live after make live: %d %s", r.Status, r.Body)
	}
	if _, err := a.Sites.LifecycleSvc().SuspendSite(context.Background(), shopID, "test", "", true); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", shop, path, nil, nil); r.Status != http.StatusGone {
		t.Fatalf("preview of a taken-down site: %d", r.Status)
	}
}

// After a handle change the old handle stays an alias: every old address
// redirects (to the person path until the new handle's certificate is
// ready), the owner app follows, and the disk keeps both handle links.
func TestHandleChangeAfterPublishing(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	oh, nh := olive.Handle, olive.Handle+"-new"
	if _, err := accountsrepo.RenameHandle(context.Background(), a.Database, olive.ID, nh); err != nil {
		t.Fatal(err)
	}
	if err := a.Disk.EnsureHandleLink(nh, olive.ID); err != nil {
		t.Fatal(err)
	}
	newPerson := nh + "." + rt.SiteDomain
	if r := a.api(t, olive, "GET", "/v1/sites", nil); !strings.Contains(string(r.Body), "https://"+newPerson+"/shop/") {
		t.Fatalf("site_url before cert: %s", r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("shop"), "/a?b=1", nil, nil); r.Status != http.StatusFound || location(r) != "https://"+newPerson+"/shop/a?b=1" {
		t.Fatalf("old site host before cert: %d %q", r.Status, location(r))
	}
	a.MarkReady(t, nh)
	for _, tc := range []struct{ host, path, want string }{
		{olive.SiteHost("shop"), "/a?b=1", "https://shop." + newPerson + "/a?b=1"},
		{oh + "." + rt.SiteDomain, "/", "https://" + newPerson + "/"},
		{newPerson, "/shop/a", "https://shop." + newPerson + "/a"},
		{rt.ContentHost, "/internal/site-redirect/" + oh + "/shop/a", "https://shop." + newPerson + "/a"},
		{rt.ContentHost, "/internal/showcase/" + oh, "/" + nh},
	} {
		if r := a.At(t, "GET", tc.host, tc.path, nil, nil); r.Status/100 != 3 || location(r) != tc.want {
			t.Errorf("%s%s: %d %q want %q", tc.host, tc.path, r.Status, location(r), tc.want)
		}
	}
	if r := a.At(t, "GET", "shop."+newPerson, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("new site host: %d %s", r.Status, r.Body)
	}
	rec := httptest.NewRecorder()
	a.Sites.OwnerAppOrStatic(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "https://"+rt.SiteDomain+"/"+oh, nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/"+nh {
		t.Fatalf("owner app alias: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, h := range []string{nh, oh} {
		if _, err := os.Stat(filepath.Join(a.Disk.DataDir(), "handles", h, "shop", "current", "index.html")); err != nil {
			t.Fatalf("handle link %s: %v", h, err)
		}
	}
}
