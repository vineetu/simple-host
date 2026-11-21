package sitestest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	operations "github.com/vsriram/simple-host/host/ops"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/web"
)

// withSiteLimit sets MAX_ARCHIVE_MB for one test and puts it back.
func withSiteLimit(t *testing.T, mb int) {
	t.Helper()
	restore := sites.Upload.Snapshot()
	sites.Upload.Configure(int64(mb) << 20)
	t.Cleanup(restore)
}

func withKeepVersions(t *testing.T, n int) {
	before := web.DefaultKeepVersions
	web.DefaultKeepVersions = n
	t.Cleanup(func() { web.DefaultKeepVersions = before })
}

// zipOf is a zip of files.
func zipOf(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// MAX_ARCHIVE_MB_OVERRIDES gives the listed accounts their own per-site cap
// on the archive and JSON paths, is what the admin list reports, and never
// takes down a site that is already larger (a rollback to it is allowed).
func TestArchiveOverridesEnforced(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	withSiteLimit(t, 1)
	a.SetLimits(t, map[string]string{"MAX_ARCHIVE_MB_OVERRIDES": olive.Handle + ":3"})
	const mb = 1 << 20
	send := func(p rt.Person, method, path, body string) rt.Resp {
		return a.At(t, method, rt.SiteDomain, path, body, p.Header())
	}
	jsonFiles := func(n int) string {
		return `{"files":{"index.html":"<h1>big</h1>","big.txt":"` + strings.Repeat("a", n) + `"}}`
	}
	ok := func(r rt.Resp) bool { return r.Status == http.StatusCreated || r.Status == http.StatusOK }
	tooLarge := func(r rt.Resp) bool {
		return r.Status == http.StatusRequestEntityTooLarge && r.Code() == "site_too_large"
	}
	for _, u := range []struct {
		site, method, path string
		body               func(n int) string
	}{
		{"sa", "POST", "/v1/sites/sa", func(n int) string { return tarGzOf(t, n) }},
		{"sb", "PUT", "/v1/sites/sb/files?create=1", jsonFiles},
	} {
		if r := send(oscar, u.method, u.path, u.body(3*mb/2)); !tooLarge(r) {
			t.Errorf("%s: oscar 1.5 MB = %d %s", u.path, r.Status, r.Body)
		}
		if r := send(olive, u.method, u.path, u.body(3*mb/2)); !ok(r) {
			t.Errorf("%s: olive 1.5 MB = %d %s", u.path, r.Status, r.Body)
		}
		if r := send(olive, "PUT", strings.TrimSuffix(u.path, "?create=1"), u.body(7*mb/2)); !tooLarge(r) {
			t.Errorf("%s: olive update 3.5 MB = %d %s", u.path, r.Status, r.Body)
		}
	}
	users := a.admin(t, "GET", "/v1/admin/users", nil)
	seen := map[string]any{}
	for _, u := range users.JSON(t)["users"].([]any) {
		m := u.(map[string]any)
		seen[m["id"].(string)] = m["max_site_mb"]
	}
	if seen[olive.ID] != float64(3) || seen[oscar.ID] != float64(1) {
		t.Errorf("admin list max_site_mb: olive %v oscar %v", seen[olive.ID], seen[oscar.ID])
	}
	if got := sites.LargestSiteLimit(a.Sites.Limits); got != 3*mb {
		t.Errorf("largestSiteLimit = %d, want %d", got, 3*mb)
	}

	// Lowered below a live site: the site stays up, only a new upload over
	// the new limit is refused, and rolling back adds no bytes.
	a.Sites.Limits = config.DefaultLimits()
	a.MarkReady(t, olive.Handle)
	host := olive.SiteHost("sa")
	if r := a.At(t, "GET", host, "/big.bin", nil, nil); r.Status != http.StatusOK || len(r.Body) != 3*mb/2 {
		t.Errorf("larger site after the limit fell: %d, %d bytes", r.Status, len(r.Body))
	}
	if r := send(olive, "PUT", "/v1/sites/sa", tarGzOf(t, 3*mb/2)); !tooLarge(r) {
		t.Errorf("redeploy 1.5 MB without the override = %d %s", r.Status, r.Body)
	}
	if r := send(olive, "PUT", "/v1/sites/sa", tarGzOf(t, mb/2)); !ok(r) {
		t.Errorf("redeploy 0.5 MB = %d %s", r.Status, r.Body)
	}
	if r := send(olive, "PUT", "/v1/sites/sa/active-version", `{"version_number":1}`); r.Status != http.StatusOK {
		t.Errorf("rollback to the larger version: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/big.bin", nil, nil); r.Status != http.StatusOK || len(r.Body) != 3*mb/2 {
		t.Errorf("larger version after rollback: %d, %d bytes", r.Status, len(r.Body))
	}
	// The admin sizes: the live copy apart from the total, read fresh after a change.
	usage := func() map[string]any {
		r := a.admin(t, "GET", "/v1/admin/usage?sizes=1", nil)
		for _, x := range r.JSON(t)["site_sizes"].([]any) {
			if m := x.(map[string]any); m["user_id"] == olive.ID && m["name"] == "sa" {
				return m
			}
		}
		t.Fatalf("sa not in site_sizes: %d", r.Status)
		return nil
	}
	page := float64(len("<h1>big</h1>"))
	if sa := usage(); sa["live_bytes"] != float64(3*mb/2)+page || sa["bytes"] != float64(3*mb/2)*2+float64(mb/2)+3*page {
		t.Errorf("sa sizes = live %v, total %v", sa["live_bytes"], sa["bytes"])
	}
	if r := send(olive, "PUT", "/v1/sites/sa", tarGzOf(t, mb/4)); !ok(r) {
		t.Fatalf("redeploy 0.25 MB = %d %s", r.Status, r.Body)
	}
	if sa := usage(); sa["live_bytes"] != float64(mb/4)+page {
		t.Errorf("sa live after a deploy = %v", sa["live_bytes"])
	}
}

// Without an override the unpacked files stay under the instance's own
// extractor caps; only an override narrows them.
func TestArchiveNoOverrideKeepsInstanceUnpackCaps(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	restore := sites.Upload.Snapshot()
	sites.Upload.Body = 1 << 20 // the body cap alone, as if Upload.Configure never ran
	t.Cleanup(restore)
	a.SetLimits(t, map[string]string{"MAX_ARCHIVE_MB_OVERRIDES": olive.Handle + ":1"})
	body := tarGzOf(t, 3<<20/2) // a few KB compressed, 1.5 MB unpacked
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/wide", body, oscar.Header()); r.Status != http.StatusCreated {
		t.Fatalf("no override: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/wide", body, olive.Header()); r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("override of 1 MB: %d %s", r.Status, r.Body)
	}
	if sites.StatedSiteMB(900<<20) != 500 {
		t.Fatal("a limit above the extractor's ceiling is reported as it is")
	}
}

// MAX_SITE_TOTAL_MB counts the live copy and kept versions on every deploy
// path, published or not.
func TestStorageCapsDeployPaths(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "capper")
	withKeepVersions(t, 4)
	a.SetLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10"})
	payload := strings.Repeat("x", 1100<<10) // 2.2 MB with the live copy
	zipped := zipOf(t, map[string][]byte{"index.html": []byte(payload)})
	jsonBody := map[string]any{"files": map[string]string{"index.html": payload}}
	totalTooLarge := func(r rt.Resp) bool { return r.Status == 413 && r.Code() == "site_total_too_large" }
	for _, tc := range []struct {
		method, path string
		body         any
	}{{"POST", "/v1/sites/json-post/files", jsonBody}, {"PUT", "/v1/sites/archive-put?create=1", zipped}} {
		if r := a.At(t, tc.method, rt.SiteDomain, tc.path, tc.body, p.Header()); !totalTooLarge(r) {
			t.Fatalf("%s: %d %s", tc.path, r.Status, r.Body)
		}
	}
	a.Deploy(t, p, "small")
	if r := a.api(t, p, "PUT", "/v1/sites/small/files", jsonBody); !totalTooLarge(r) {
		t.Fatalf("update: %d %s", r.Status, r.Body)
	}
	// Unpublished: the tiny live copy plus 1.1 MB fits once, not twice.
	if r := a.api(t, p, "PUT", "/v1/sites/small/files?publish=false", jsonBody); r.Status != 200 {
		t.Fatalf("first unpublished: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/small?publish=false", zipped, p.Header()); !totalTooLarge(r) {
		t.Fatalf("second unpublished: %d %s", r.Status, r.Body)
	}
}

// Retention keeps the newest versions (and removes old folders); only
// allowed accounts may set it; sites older than the cap date have no total
// cap; an old handle still grants the allowlist and overrides.
func TestStorageCapsRetentionAndGrandfathering(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "caps")
	withKeepVersions(t, 4)
	a.SetLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10"})
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 400<<10)}}
	for i := 0; i < 8; i++ {
		method := "PUT"
		if i == 0 {
			method = "POST"
		}
		if r := a.api(t, p, method, "/v1/sites/roll/files", body); r.Status/100 != 2 {
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
	if r := a.api(t, p, "PUT", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 10}); r.Status != 403 {
		t.Fatalf("fixed: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, p, "PUT", "/v1/sites/roll/files", body); r.Status != 200 || countVersions() != 4 {
		t.Fatalf("unauthorized persisted count honored: %d %d", r.Status, countVersions())
	}
	// Old sites have no total cap.
	if _, err := a.Database.Exec(`UPDATE sites SET created_at='2025-01-01' WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	big := map[string]any{"files": map[string]string{"index.html": strings.Repeat("b", 1200<<10)}}
	if r := a.api(t, p, "PUT", "/v1/sites/roll/files", big); r.Status != 200 {
		t.Fatalf("grandfather: %d %s", r.Status, r.Body)
	}
	// An old handle grants the allowlist and the overrides.
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ('allowed',$1)`, p.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM handle_aliases WHERE handle='allowed'`) })
	a.SetLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "1", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "1", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10", "MAX_SITE_TOTAL_OVERRIDES": "allowed:20", "MAX_ACCOUNT_MB_OVERRIDES": "allowed:30"})
	if r := a.api(t, p, "PUT", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 0}); r.Status != 200 || r.JSON(t)["effective_keep_versions"] != float64(10) {
		t.Fatalf("alias retention: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, p, "POST", "/v1/sites/alias/files", big); r.Status != 201 {
		t.Fatalf("alias caps: %d %s", r.Status, r.Body)
	}
}

// MAX_ACCOUNT_MB counts every site's deployed files (text and binary, and
// Recently deleted ones) but not backend storage; a refusal moves nothing.
func TestAccountFileCapAndUsage(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "accountcap")
	withKeepVersions(t, 1)
	a.SetLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 300<<10)}}
	if r := a.api(t, p, "POST", "/v1/sites/first/files", body); r.Status != 201 {
		t.Fatalf("first %s", r.Body)
	}
	for i := 0; i < 3; i++ {
		if r := a.api(t, p, "PUT", "/v1/sites/first/files", body); r.Status != 200 {
			t.Fatalf("replace after pruning %s", r.Body)
		}
	}
	full := func(r rt.Resp) bool { return r.Status == 413 && r.Code() == "account_storage_full" }
	if r := a.api(t, p, "POST", "/v1/sites/second/files", body); !full(r) {
		t.Fatalf("account cap %d %s", r.Status, r.Body)
	}
	if b, err := os.ReadFile(filepath.Join(a.siteDir(p, "first"), "current", "index.html")); err != nil || len(b) != 300<<10 {
		t.Fatalf("live changed: %v %d", err, len(b))
	}
	storage := filepath.Join(a.siteDir(p, "first"), "storage")
	if err := os.MkdirAll(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, "resource"), make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := a.api(t, p, "PUT", "/v1/sites/first/files", body); r.Status != 200 {
		t.Fatalf("resource excluded %s", r.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(a.api(t, p, "GET", "/v1/sites", nil).Body, &list); err != nil || list[0]["file_bytes"] != float64(600<<10) {
		t.Fatalf("sizes %v %v", list, err)
	}
	if r := a.api(t, p, "DELETE", "/v1/sites/first", nil); r.Status != 204 {
		t.Fatalf("delete %d %s", r.Status, r.Body)
	}
	if r := a.api(t, p, "POST", "/v1/sites/second/files", body); !full(r) {
		t.Fatalf("trash not counted %d %s", r.Status, r.Body)
	}
	if r := a.api(t, p, "POST", "/v1/sites/binary/files", map[string]any{"files": map[string]string{"index.html": "x"}, "files_base64": map[string]string{"large.pdf": base64.StdEncoding.EncodeToString(make([]byte, 400<<10))}}); !full(r) {
		t.Fatalf("binary not counted %d %s", r.Status, r.Body)
	}
}

// The operator's prune resets fixed accounts to the default retention but
// keeps the live version, Recently deleted sites and exempt accounts (by an
// old handle too); a dry run changes nothing.
func TestPruneFixedAccountsProtectsLiveAndDeleted(t *testing.T) {
	a := NewApp(t)
	fixed, exempt := a.NewPerson(t, "fixed"), a.NewPerson(t, "exempt")
	withKeepVersions(t, 0)
	for _, p := range []rt.Person{fixed, exempt} {
		a.Deploy(t, p, "history")
		for i := 0; i < 6; i++ {
			if r := a.files(t, p, "PUT", "history", map[string]string{"index.html": "new"}); r.Status != 200 {
				t.Fatalf("history %s", r.Body)
			}
		}
	}
	if r := a.api(t, fixed, "PUT", "/v1/sites/history/active-version", map[string]int{"version_number": 1}); r.Status != 200 {
		t.Fatalf("rollback %s", r.Body)
	}
	deletedID := a.Deploy(t, fixed, "deleted")
	if r := a.api(t, fixed, "DELETE", "/v1/sites/deleted", nil); r.Status != 204 {
		t.Fatalf("delete %d", r.Status)
	}
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ($1,$2)`, exempt.Handle+"-old", exempt.ID); err != nil {
		t.Fatal(err)
	}
	web.DefaultKeepVersions = 4
	l := limits(t, map[string]string{"KEEP_VERSIONS_SELF_SET": exempt.Handle + "-old"})
	a.Sites.Limits = l
	if _, err := a.Database.Exec(`UPDATE sites SET keep_versions=100 WHERE user_id=$1 AND name='history'`, fixed.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	versions := func(p rt.Person) (sitesrepo.Site, int) {
		site, _ := sitesrepo.GetSiteByUser(ctx, a.Database, p.ID, "history")
		v, _ := sitesrepo.ListVersionsBySite(ctx, a.Database, site.ID)
		return site, len(v)
	}
	var out bytes.Buffer
	if err := operations.PruneFixedAccounts(ctx, a.Database, a.Disk, edition.Default(l), web.DefaultKeepVersions, false, &out); err != nil {
		t.Fatal(err)
	}
	if _, n := versions(fixed); n != 7 {
		t.Fatal("dry run removed rows")
	}
	if err := operations.PruneFixedAccounts(ctx, a.Database, a.Disk, edition.Default(l), web.DefaultKeepVersions, true, &out); err != nil {
		t.Fatal(err)
	}
	if site, n := versions(fixed); site.KeepVersions != 0 || n != 5 {
		t.Fatalf("want newest 4 plus old live, setting reset; got %d versions, keep %d", n, site.KeepVersions)
	}
	if f, err := a.Disk.SiteFootprint(fixed.ID, "history"); err != nil || f.Versions[1] == 0 || len(f.Versions) != 5 {
		t.Fatalf("live protected %v %v", f, err)
	}
	if _, n := versions(exempt); n != 7 {
		t.Fatal("exempt versions touched")
	}
	if _, err := os.Stat(filepath.Join(a.Disk.TrashDir(fixed.ID, deletedID), "v1", "index.html")); err != nil {
		t.Fatalf("deleted touched %v", err)
	}
}

// The account cap is checked under the account's lock: two different sites
// deployed at once cannot both pass it (site locks are per account).
func TestAccountCapSerializesDifferentSites(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "concurrentcap")
	a.SetLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			<-start
			results <- a.files(t, p, "POST", name, map[string]string{"index.html": strings.Repeat("x", 300<<10)}).Status
		}(name)
	}
	close(start)
	got := map[int]int{}
	for i := 0; i < 2; i++ {
		got[<-results]++
	}
	if got[201] != 1 || got[413] != 1 {
		t.Fatalf("concurrent deploys: %v", got)
	}
}
