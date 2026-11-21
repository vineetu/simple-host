package sitestest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// ?author= on deploys (trimmed, cleaned, capped), ?pending=1, and DELETE
// /v1/sites/{sitename}/versions/{version}: only a never-live version goes,
// its folder with it, its number never handed out again; the admin mirror
// runs the same handler; retention reports real versions only.
func TestDiscardVersion(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "discard-owner")
	const site = "discard-site"
	deploy := func(method, query string, want int) {
		t.Helper()
		if r := a.api(t, owner, method, "/v1/sites/"+site+"/files"+query, map[string]any{"files": map[string]string{"index.html": "<h1>" + query + "</h1>"}}); r.Status != want {
			t.Fatalf("%s %s: %d %s", method, query, r.Status, r.Body)
		}
	}
	type row struct {
		VersionNumber int    `json:"version_number"`
		Status        string `json:"status"`
		Author        string `json:"author"`
	}
	list := func(query string) []row {
		t.Helper()
		r := a.api(t, owner, "GET", "/v1/sites/"+site+"/versions"+query, nil)
		var out []row
		if err := json.Unmarshal(r.Body, &out); r.Status != http.StatusOK || err != nil {
			t.Fatalf("list %s: %d %s", query, r.Status, r.Body)
		}
		return out
	}
	numbers := func(rows []row) (out []int) {
		for _, v := range rows {
			out = append(out, v.VersionNumber)
		}
		return out
	}
	deploy("POST", "?author=%20Alice%20", http.StatusCreated)                              // v1, live
	deploy("PUT", "?publish=false&author="+url.QueryEscape(strings.Repeat("é", 100)), 200) // v2
	deploy("PUT", "?publish=false&author="+url.QueryEscape("Bo\x01b\x7f"), 200)            // v3
	deploy("PUT", "", 200)                                                                 // v4, live
	deploy("PUT", "?create=1&publish=false", 200)                                          // v5
	authors := map[int]string{}
	for _, v := range list("") {
		authors[v.VersionNumber] = v.Author
	}
	if authors[1] != "Alice" || authors[2] != strings.Repeat("é", 80) || authors[3] != "Bob" || authors[4] != "" {
		t.Errorf("authors: %v", authors)
	}
	if got := numbers(list("?pending=1")); len(got) != 3 || got[0] != 5 || got[1] != 3 || got[2] != 2 {
		t.Fatalf("pending = %v, want 5 3 2", got)
	}
	// Live once: never pending again.
	for _, n := range []int{3, 4} {
		if r := a.api(t, owner, "PUT", "/v1/sites/"+site+"/active-version", map[string]any{"version_number": n}); r.Status != 200 {
			t.Fatalf("make v%d live: %d %s", n, r.Status, r.Body)
		}
	}
	if got := numbers(list("?pending=true")); len(got) != 2 || got[0] != 5 || got[1] != 2 {
		t.Fatalf("pending after v3 was live = %v", got)
	}

	siteID := a.SiteID(t, owner, site)
	base := "/v1/sites/" + site + "/versions/"
	for path, want := range map[string]struct {
		status int
		code   string
	}{
		base + "4": {409, "version_live"}, base + "3": {409, "version_published"},
		base + "99": {404, "version_not_found"}, base + "two": {404, "version_not_found"},
		"/v1/sites/no-such-site/versions/2": {404, "site_not_found"},
	} {
		if r := a.api(t, owner, "DELETE", path, nil); r.Status != want.status || r.Code() != want.code {
			t.Errorf("DELETE %s: %d %s, want %d %s", path, r.Status, r.Body, want.status, want.code)
		}
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, base+"2", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}
	v2dir := a.Disk.VersionDir(owner.ID, site, 2)
	if r := a.api(t, owner, "DELETE", base+"2", nil); r.Status != 200 || r.JSON(t)["discarded_version"] != float64(2) {
		t.Fatalf("discard v2: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(v2dir); !os.IsNotExist(err) {
		t.Fatalf("v2 folder after discard: %v", err)
	}
	if _, err := os.Stat(a.Disk.VersionDir(owner.ID, site, 1)); err != nil {
		t.Fatalf("v1 folder must stay: %v", err)
	}
	var status string
	if err := a.Database.QueryRow(`SELECT status FROM versions WHERE site_id = $1 AND version_number = 2`, siteID).Scan(&status); err != nil || status != "discarded" {
		t.Fatalf("v2 row after discard: %q %v", status, err)
	}
	for _, r := range []rt.Resp{
		a.api(t, owner, "POST", base+"2/preview-link", nil),
		a.api(t, owner, "PUT", "/v1/sites/"+site+"/active-version", map[string]any{"version_number": 2}),
	} {
		if r.Status < 400 {
			t.Fatalf("a discarded version was used: %d %s", r.Status, r.Body)
		}
	}
	if r := a.admin(t, "DELETE", "/v1/admin/sites/"+siteID+"/versions/5", nil); r.Status != 200 || r.JSON(t)["discarded_version"] != float64(5) {
		t.Fatalf("admin discard v5: %d %s", r.Status, r.Body)
	}
	// v5 was the newest: the next draft is v6, not v5 again.
	deploy("PUT", "?publish=false", 200)
	if got := numbers(list("?pending=1")); len(got) != 1 || got[0] != 6 {
		t.Fatalf("next draft = %v, want 6", got)
	}
	// Retention drops tombstones but reports only real versions (v1 and v3).
	r := a.api(t, owner, "PUT", "/v1/sites/"+site+"/keep-versions", map[string]any{"keep_versions": 1})
	var kept struct {
		Removed []int `json:"removed_versions"`
	}
	if err := json.Unmarshal(r.Body, &kept); r.Status != 200 || err != nil || len(kept.Removed) != 2 || kept.Removed[0]+kept.Removed[1] != 4 {
		t.Fatalf("removed_versions = %v: %d %s", kept.Removed, r.Status, r.Body)
	}
}

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
