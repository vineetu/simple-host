package versions_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"

	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// ?author= on deploys (trimmed, cleaned, capped), ?pending=1, and DELETE
// /v1/sites/{sitename}/versions/{version}: only a never-live version goes,
// its folder with it, its number never handed out again; the admin mirror
// runs the same handler; retention reports real versions only.
func TestDiscardVersion(t *testing.T) {
	a := newApp(t)
	owner := a.NewPerson(t, "discard-owner")
	const site = "discard-site"
	deploy := func(method, query string, want int) {
		t.Helper()
		if r := a.API(t, owner, method, "/v1/sites/"+site+"/files"+query, map[string]any{"files": map[string]string{"index.html": "<h1>" + query + "</h1>"}}); r.Status != want {
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
		r := a.API(t, owner, "GET", "/v1/sites/"+site+"/versions"+query, nil)
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
		if r := a.API(t, owner, "PUT", "/v1/sites/"+site+"/active-version", map[string]any{"version_number": n}); r.Status != 200 {
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
		if r := a.API(t, owner, "DELETE", path, nil); r.Status != want.status || r.Code() != want.code {
			t.Errorf("DELETE %s: %d %s, want %d %s", path, r.Status, r.Body, want.status, want.code)
		}
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, base+"2", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}
	v2dir := a.Disk.VersionDir(owner.ID, site, 2)
	if r := a.API(t, owner, "DELETE", base+"2", nil); r.Status != 200 || r.JSON(t)["discarded_version"] != float64(2) {
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
		a.API(t, owner, "POST", base+"2/preview-link", nil),
		a.API(t, owner, "PUT", "/v1/sites/"+site+"/active-version", map[string]any{"version_number": 2}),
	} {
		if r.Status < 400 {
			t.Fatalf("a discarded version was used: %d %s", r.Status, r.Body)
		}
	}
	if r := a.AsAdmin(t, "DELETE", "/v1/admin/sites/"+siteID+"/versions/5", nil); r.Status != 200 || r.JSON(t)["discarded_version"] != float64(5) {
		t.Fatalf("admin discard v5: %d %s", r.Status, r.Body)
	}
	// v5 was the newest: the next draft is v6, not v5 again.
	deploy("PUT", "?publish=false", 200)
	if got := numbers(list("?pending=1")); len(got) != 1 || got[0] != 6 {
		t.Fatalf("next draft = %v, want 6", got)
	}
	// Retention drops tombstones but reports only real versions (v1 and v3).
	r := a.API(t, owner, "PUT", "/v1/sites/"+site+"/keep-versions", map[string]any{"keep_versions": 1})
	var kept struct {
		Removed []int `json:"removed_versions"`
	}
	if err := json.Unmarshal(r.Body, &kept); r.Status != 200 || err != nil || len(kept.Removed) != 2 || kept.Removed[0]+kept.Removed[1] != 4 {
		t.Fatalf("removed_versions = %v: %d %s", kept.Removed, r.Status, r.Body)
	}
}

// A server that gives sites no address of their own (every site on one shared
// origin) makes no preview link: the stored version is held but unshown, and
// the mint route refuses with preview_unavailable and says why.
func TestNoPreviewOnSharedOrigin(t *testing.T) {
	a := newApp(t)
	a.Hosts.SetPersonHosts("off")
	a.Hosts.SetSiteHosts("off", "")
	if !a.H.SharedOrigin() {
		t.Fatal("the app still gives sites their own addresses")
	}
	ann := a.NewPerson(t, "ann")
	a.Deploy(t, ann, "blog")
	r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/blog/files?publish=false", map[string]any{"files": map[string]string{"index.html": "v2"}}, ann.Header())
	if r.Status != 200 || r.JSON(t)["preview_url"] != nil {
		t.Fatalf("publish=false: %d %s", r.Status, r.Body)
	}
	p := a.At(t, "POST", rt.SiteDomain, "/v1/sites/blog/versions/2/preview-link", nil, ann.Header())
	if p.Status != 409 || p.JSON(t)["code"] != "preview_unavailable" {
		t.Fatalf("preview link: %d %s", p.Status, p.Body)
	}
}

// ?keep=1 merges the upload onto the live version: unnamed files stay,
// remove_files go, and a removal path may not leave the site.
func TestKeepOtherFiles(t *testing.T) {
	a := newApp(t)
	owner := a.NewPerson(t, "keep-files")
	image := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	r := a.API(t, owner, "POST", "/v1/sites/big/files", map[string]any{"files": map[string]string{"index.html": "home", "about.html": "old", "remove.html": "drop"}, "files_base64": map[string]string{"photo.png": base64.StdEncoding.EncodeToString(image)}})
	if r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	r = a.API(t, owner, "PUT", "/v1/sites/big/files?keep=1", map[string]any{"files": map[string]string{"about.html": "new"}, "remove_files": []string{"remove.html"}})
	if r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	for _, file := range []struct{ path, want string }{{"index.html", "home"}, {"about.html", "new"}, {"photo.png", string(image)}} {
		r = a.API(t, owner, "GET", "/v1/sites/big/versions/2/files/"+file.path, nil)
		if r.Status != 200 || string(r.Body) != file.want {
			t.Fatal(file.path, r.Status, string(r.Body))
		}
	}
	if r = a.API(t, owner, "GET", "/v1/sites/big/versions/2/files/remove.html", nil); r.Status != 404 {
		t.Fatal("removed file kept")
	}
	for _, tc := range []struct {
		path   string
		remove []string
	}{{"/v1/sites/big/files", []string{"about.html"}}, {"/v1/sites/big/files?keep=1", []string{"../outside"}}, {"/v1/sites/big/files?keep=1", []string{`a\..\outside`}}} {
		if r = a.API(t, owner, "PUT", tc.path, map[string]any{"files": map[string]string{"index.html": "new"}, "remove_files": tc.remove}); r.Status != 400 {
			t.Fatal(tc.path, r.Status, string(r.Body))
		}
	}
	if r = a.API(t, owner, "PUT", "/v1/sites/missing/files?keep=1", map[string]any{"files": map[string]string{"about.html": "new"}}); r.Status != 404 {
		t.Fatal(r.Status, string(r.Body))
	}
	// A site without a live version accepts an ordinary complete first version.
	if _, err := a.Database.Exec(`UPDATE sites SET active_version = 0 WHERE name = 'big' AND user_id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if r = a.API(t, owner, "PUT", "/v1/sites/big/files?keep=1", map[string]any{"files": map[string]string{"index.html": "fresh"}}); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
}

// Retention keeps the newest versions (and removes old folders); only
// allowed accounts may set it; sites older than the cap date have no total
// cap; an old handle still grants the allowlist and overrides.
func TestStorageCapsRetentionAndGrandfathering(t *testing.T) {
	a := newApp(t)
	p := a.NewPerson(t, "caps")
	fixture.WithKeepVersions(t, 4)
	a.SetLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10"})
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 400<<10)}}
	for i := 0; i < 8; i++ {
		method := "PUT"
		if i == 0 {
			method = "POST"
		}
		if r := a.API(t, p, method, "/v1/sites/roll/files", body); r.Status/100 != 2 {
			t.Fatalf("roll %d: %d %s", i, r.Status, r.Body)
		}
	}
	ctx := context.Background()
	site, err := sitesrepo.GetSiteByUser(ctx, a.Database, p.ID, "roll")
	if err != nil {
		t.Fatal(err)
	}
	countVersions := func() int {
		v, err := sitesrepo.ListVersionsBySite(ctx, a.Database, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(v)
	}
	if n := countVersions(); n != 4 {
		t.Fatalf("versions %d", n)
	}
	if _, err := os.Stat(a.Disk.VersionDir(p.ID, "roll", 1)); !os.IsNotExist(err) {
		t.Fatalf("old folder remains: %v", err)
	}
	// Not allowed to set it: refused, and a stored count is ignored.
	if _, err := a.Database.Exec(`UPDATE sites SET keep_versions=100 WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if r := a.API(t, p, "PUT", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 10}); r.Status != 403 {
		t.Fatalf("fixed: %d %s", r.Status, r.Body)
	}
	if r := a.API(t, p, "PUT", "/v1/sites/roll/files", body); r.Status != 200 || countVersions() != 4 {
		t.Fatalf("unauthorized persisted count honored: %d %d", r.Status, countVersions())
	}
	// Old sites have no total cap.
	if _, err := a.Database.Exec(`UPDATE sites SET created_at='2025-01-01' WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	big := map[string]any{"files": map[string]string{"index.html": strings.Repeat("b", 1200<<10)}}
	if r := a.API(t, p, "PUT", "/v1/sites/roll/files", big); r.Status != 200 {
		t.Fatalf("grandfather: %d %s", r.Status, r.Body)
	}
	// An old handle grants the allowlist and the overrides.
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ('allowed',$1)`, p.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM handle_aliases WHERE handle='allowed'`) })
	a.SetLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "1", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "1", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10", "MAX_SITE_TOTAL_OVERRIDES": "allowed:20", "MAX_ACCOUNT_MB_OVERRIDES": "allowed:30"})
	if r := a.API(t, p, "PUT", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 0}); r.Status != 200 || r.JSON(t)["effective_keep_versions"] != float64(10) {
		t.Fatalf("alias retention: %d %s", r.Status, r.Body)
	}
	if r := a.API(t, p, "POST", "/v1/sites/alias/files", big); r.Status != 201 {
		t.Fatalf("alias caps: %d %s", r.Status, r.Body)
	}
}
