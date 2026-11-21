package sitestest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/web"
)

func TestStorageCapsDeployPaths(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	p := a.NewPerson(t, "capper")
	headers := map[string]string{"X-API-Key": p.Key}
	before := web.DefaultKeepVersions
	web.DefaultKeepVersions = 4
	t.Cleanup(func() { web.DefaultKeepVersions = before })
	servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10", "KEEP_VERSIONS_SELF_SET": "chhotabreak"}))
	// 1.1 MB expands to 2.2 MB with the live copy, despite compressing well.
	payload := strings.Repeat("x", 1100<<10)
	for _, tc := range []struct{ method, path, format string }{
		{"POST", "/v1/sites/json-post/files", "json"},
		{"PUT", "/v1/sites/json-put/files?create=1", "json"},
		{"POST", "/v1/sites/archive-post", "zip"},
		{"PUT", "/v1/sites/archive-put?create=1", "zip"},
	} {
		t.Run(tc.format+tc.method, func(t *testing.T) {
			var body any = map[string]any{"files": map[string]string{"index.html": payload}}
			if tc.format == "zip" {
				var buf bytes.Buffer
				zw := zip.NewWriter(&buf)
				f, _ := zw.Create("index.html")
				_, _ = f.Write([]byte(payload))
				_ = zw.Close()
				body = buf.String()
			}
			r := a.At(t, tc.method, "simple-host.test", tc.path, body, headers)
			if r.Status != 413 || r.JSON(t)["code"] != "site_total_too_large" {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
		})
	}
	a.Deploy(t, p, "small")
	for _, suffix := range []string{"/files", "/files?publish=false", "", "?publish=false"} {
		var body any = map[string]any{"files": map[string]string{"index.html": payload}}
		if !strings.Contains(suffix, "/files") {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			f, _ := zw.Create("index.html")
			_, _ = f.Write([]byte(payload))
			_ = zw.Close()
			body = buf.String()
		}
		r := a.At(t, "PUT", "simple-host.test", "/v1/sites/small"+suffix, body, headers)
		// unpublished: existing tiny live copy + incoming 1.1 MB fits initially,
		// so pre-fill history before checking it.
		if strings.Contains(suffix, "publish=false") && r.Status == 200 {
			r = a.At(t, "PUT", "simple-host.test", "/v1/sites/small"+suffix, body, headers)
		}
		if r.Status != 413 || r.JSON(t)["code"] != "site_total_too_large" {
			t.Fatalf("update %s: %d %s", suffix, r.Status, r.Body)
		}
	}
	// The connector goes through the same JSON commit, not its own upload path.
	token := a.Connect(t, p, a.RegisterClient(t, servertest.TestRedirect), servertest.TestRedirect)["access_token"].(string)
	fixed := a.Rpc(t, token, "tools/call", map[string]any{"name": "set_keep_versions", "arguments": map[string]any{"site": "small", "keep_versions": 10}})
	if !strings.Contains(string(fixed.Body), "Simple Host keeps your 4 latest versions") {
		t.Fatalf("MCP retention refusal: %s", fixed.Body)
	}

	r := a.Rpc(t, token, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{"site": "connector-big", "files": map[string]string{"index.html": payload}}})
	if !strings.Contains(string(r.Body), "site_total_too_large") {
		t.Fatalf("connector refusal: %s", r.Body)
	}
}

func TestStorageCapsRetentionAndGrandfathering(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	p := a.NewPerson(t, "caps")
	uid, hd := a.UserID(t, p)
	before := web.DefaultKeepVersions
	web.DefaultKeepVersions = 4
	t.Cleanup(func() { web.DefaultKeepVersions = before })
	servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10"}))
	headers := map[string]string{"X-API-Key": p.Key}
	// Five copies (4 retained + current) at 400 KB stay under 2 MB forever.
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 400<<10)}}
	for i := 0; i < 8; i++ {
		method := "PUT"
		if i == 0 {
			method = "POST"
		}
		r := a.At(t, method, "simple-host.test", "/v1/sites/roll/files", body, headers)
		if r.Status != 200 && r.Status != 201 {
			t.Fatalf("roll %d: %d %s", i, r.Status, r.Body)
		}
	}
	site, err := sitesrepo.GetSiteByUser(context.Background(), a.Database, uid, "roll")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := sitesrepo.ListVersionsBySite(context.Background(), a.Database, site.ID)
	if err != nil || len(versions) != 4 {
		t.Fatalf("versions %v %v", versions, err)
	}
	if _, err := os.Stat(a.Sites.Disk.VersionDir(uid, "roll", 1)); !os.IsNotExist(err) {
		t.Fatalf("old folder remains: %v", err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET keep_versions=100 WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "PUT", "simple-host.test", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 10}, headers)
	if r.Status != 403 || r.JSON(t)["error"] != "Simple Host keeps your 4 latest versions" {
		t.Fatalf("fixed: %d %s", r.Status, r.Body)
	}
	if r = a.At(t, "PUT", "simple-host.test", "/v1/sites/roll/files", body, headers); r.Status != 200 {
		t.Fatalf("ignored setting: %d %s", r.Status, r.Body)
	}
	versions, _ = sitesrepo.ListVersionsBySite(context.Background(), a.Database, site.ID)
	if len(versions) != 4 {
		t.Fatalf("unauthorized persisted count honored: %d", len(versions))
	}
	// Old sites have no total cap, but still retain 4.
	if _, err := a.Database.Exec(`UPDATE sites SET created_at='2025-01-01' WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	big := map[string]any{"files": map[string]string{"index.html": strings.Repeat("b", 1200<<10)}}
	if r = a.At(t, "PUT", "simple-host.test", "/v1/sites/roll/files", big, headers); r.Status != 200 {
		t.Fatalf("grandfather: %d %s", r.Status, r.Body)
	}
	// An old handle grants the allowlist and all three overrides.
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ('allowed',$1)`, uid); err != nil {
		t.Fatal(err)
	}
	servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "1", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "1", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10", "MAX_SITE_TOTAL_OVERRIDES": "allowed:20", "MAX_ACCOUNT_MB_OVERRIDES": "allowed:30"}))
	r = a.At(t, "PUT", "simple-host.test", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 0}, headers)
	if r.Status != 200 || r.JSON(t)["effective_keep_versions"] != float64(10) {
		t.Fatalf("alias retention %s/%s: %d %s", hd, uid, r.Status, r.Body)
	}
	if r = a.At(t, "POST", "simple-host.test", "/v1/sites/alias/files", big, headers); r.Status != 201 {
		t.Fatalf("alias caps: %d %s", r.Status, r.Body)
	}
	me := a.At(t, "GET", "simple-host.test", "/v1/me", nil, headers).JSON(t)
	usage := me["file_usage"].(map[string]any)
	if usage["limit_mb"] != float64(30) || me["can_set_keep_versions"] != true {
		t.Fatalf("usage %v", me)
	}
	if tips, _ := usage["tips"].(string); tips == "" {
		t.Fatal("missing tips")
	}
}

func TestAccountFileCapAndUsage(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	p := a.NewPerson(t, "accountcap")
	uid, _ := a.UserID(t, p)
	before := web.DefaultKeepVersions
	web.DefaultKeepVersions = 1
	t.Cleanup(func() { web.DefaultKeepVersions = before })
	servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"}))
	headers := map[string]string{"X-API-Key": p.Key}
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 300<<10)}}
	r := a.At(t, "POST", "simple-host.test", "/v1/sites/first/files", body, headers)
	if r.Status != 201 {
		t.Fatalf("first %s", r.Body)
	}
	for i := 0; i < 5; i++ {
		r = a.At(t, "PUT", "simple-host.test", "/v1/sites/first/files", body, headers)
		if r.Status != 200 {
			t.Fatalf("replace after pruning %s", r.Body)
		}
	}
	r = a.At(t, "POST", "simple-host.test", "/v1/sites/second/files", body, headers)
	if r.Status != 413 || r.JSON(t)["code"] != "account_storage_full" || !strings.Contains(string(r.Body), "Delete old sites") {
		t.Fatalf("account cap %d %s", r.Status, r.Body)
	}
	// Existing files never move on refusal.
	b, err := os.ReadFile(filepath.Join(a.Sites.Disk.SiteDir(uid, "first"), "current", "index.html"))
	if err != nil || len(b) != 300<<10 {
		t.Fatalf("live changed: %v %d", err, len(b))
	}
	// Backend files do not count; every binary asset in deploy files does.
	if err := os.MkdirAll(filepath.Join(a.Sites.Disk.SiteDir(uid, "first"), "storage"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Sites.Disk.SiteDir(uid, "first"), "storage", "resource"), make([]byte, 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	r = a.At(t, "PUT", "simple-host.test", "/v1/sites/first/files", body, headers)
	if r.Status != 200 {
		t.Fatalf("resource excluded %s", r.Body)
	}
	me := a.At(t, "GET", "simple-host.test", "/v1/me", nil, headers).JSON(t)
	usage := me["file_usage"].(map[string]any)
	if usage["used_bytes"] != float64(600<<10) {
		t.Fatalf("file usage %v", usage)
	}
	sites := a.At(t, "GET", "simple-host.test", "/v1/sites", nil, headers)
	var list []map[string]any
	if err := json.Unmarshal(sites.Body, &list); err != nil {
		t.Fatal(err)
	}
	if list[0]["file_bytes"] != float64(600<<10) {
		t.Fatalf("sizes %v", list)
	}
	// Deleting keeps the bytes until normal Recently deleted expiry.
	r = a.At(t, "DELETE", "simple-host.test", "/v1/sites/first", nil, headers)
	if r.Status != 204 {
		t.Fatalf("delete %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "simple-host.test", "/v1/sites/second/files", body, headers)
	if r.Status != 413 {
		t.Fatalf("trash counted %d %s", r.Status, r.Body)
	}
	// Binary files use the same footprint as text.
	r = a.At(t, "POST", "simple-host.test", "/v1/sites/binary/files", map[string]any{"files": map[string]string{"index.html": "x"}, "files_base64": map[string]string{"large.pdf": base64.StdEncoding.EncodeToString(make([]byte, 400<<10))}}, headers)
	if r.Status != 413 || r.JSON(t)["code"] != "account_storage_full" {
		t.Fatalf("binary counted %d %s", r.Status, r.Body)
	}
}

func TestPruneFixedAccountsProtectsLiveAndDeleted(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	fixed := a.NewPerson(t, "fixed")
	exempt := a.NewPerson(t, "exempt")
	uid, _ := a.UserID(t, fixed)
	exID, exHD := a.UserID(t, exempt)
	before := web.DefaultKeepVersions
	web.DefaultKeepVersions = 0
	t.Cleanup(func() { web.DefaultKeepVersions = before })
	for _, p := range []servertest.Person{fixed, exempt} {
		a.Deploy(t, p, "history")
		for i := 0; i < 6; i++ {
			r := a.At(t, "PUT", "simple-host.test", "/v1/sites/history/files", map[string]any{"files": map[string]string{"index.html": "new"}}, map[string]string{"X-API-Key": p.Key})
			if r.Status != 200 {
				t.Fatalf("history %s", r.Body)
			}
		}
	}
	headers := map[string]string{"X-API-Key": fixed.Key}
	r := a.At(t, "PUT", "simple-host.test", "/v1/sites/history/active-version", map[string]int{"version_number": 1}, headers)
	if r.Status != 200 {
		t.Fatalf("rollback %s", r.Body)
	}
	a.Deploy(t, fixed, "deleted")
	r = a.At(t, "DELETE", "simple-host.test", "/v1/sites/deleted", nil, headers)
	if r.Status != 204 {
		t.Fatalf("delete %d", r.Status)
	}
	// Make an old-handle exemption prove the operator path also matches aliases.
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ($1,$2)`, exHD+"-old", exID); err != nil {
		t.Fatal(err)
	}
	web.DefaultKeepVersions = 4
	l := servertest.WithLimits(t, map[string]string{"KEEP_VERSIONS_SELF_SET": exHD + "-old"})
	servertest.SetAppLimits(a, l)
	if _, err := a.Database.Exec(`UPDATE sites SET keep_versions=100 WHERE user_id=$1 AND name='history'`, uid); err != nil {
		t.Fatal(err)
	}
	var dry bytes.Buffer
	if err := operations.PruneFixedAccounts(context.Background(), a.Database, a.Sites.Disk, edition.Default(l), web.KeepVersions(), false, &dry); err != nil {
		t.Fatal(err)
	}
	site, _ := sitesrepo.GetSiteByUser(context.Background(), a.Database, uid, "history")
	versions, _ := sitesrepo.ListVersionsBySite(context.Background(), a.Database, site.ID)
	if len(versions) != 7 {
		t.Fatal("dry run removed rows")
	}
	var applied bytes.Buffer
	if err := operations.PruneFixedAccounts(context.Background(), a.Database, a.Sites.Disk, edition.Default(l), web.KeepVersions(), true, &applied); err != nil {
		t.Fatal(err)
	}
	site, _ = sitesrepo.GetSiteByUser(context.Background(), a.Database, uid, "history")
	if site.KeepVersions != 0 {
		t.Fatalf("fixed setting not reset: %d", site.KeepVersions)
	}
	versions, _ = sitesrepo.ListVersionsBySite(context.Background(), a.Database, site.ID)
	if len(versions) != 5 {
		t.Fatalf("want newest 4 plus old live; got %v", versions)
	}
	f, err := a.Sites.Disk.SiteFootprint(uid, "history")
	if err != nil || f.Versions[1] == 0 || len(f.Versions) != 5 {
		t.Fatalf("live protected %v %v", f, err)
	}
	site, _ = sitesrepo.GetSiteByUser(context.Background(), a.Database, exID, "history")
	versions, _ = sitesrepo.ListVersionsBySite(context.Background(), a.Database, site.ID)
	if len(versions) != 7 {
		t.Fatal("exempt versions touched")
	}
	var deletedID string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id=$1 AND name='deleted'`, uid).Scan(&deletedID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Sites.Disk.TrashDir(uid, deletedID), "v1", "index.html")); err != nil {
		t.Fatalf("deleted touched %v", err)
	}
}

func TestAccountCapSerializesDifferentSites(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	p := a.NewPerson(t, "concurrentcap")
	servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"}))
	start := make(chan struct{})
	results := make(chan servertest.Resp, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			<-start
			results <- a.At(t, "POST", "simple-host.test", "/v1/sites/"+name+"/files", map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 300<<10)}}, map[string]string{"X-API-Key": p.Key})
		}(name)
	}
	close(start)
	created, refused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		switch r.Status {
		case 201:
			created++
		case 413:
			refused++
		default:
			t.Fatalf("concurrent deploy %d %s", r.Status, r.Body)
		}
	}
	if created != 1 || refused != 1 {
		t.Fatalf("created=%d refused=%d", created, refused)
	}
}
