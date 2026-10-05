package handler

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

	"github.com/vsriram/simple-host/internal/db"
)

func TestStorageCapsDeployPaths(t *testing.T) {
	a := newPersonApp(t, "serve")
	p := a.newPerson(t, "capper")
	headers := map[string]string{"X-API-Key": p.key}
	before := keepVersions
	keepVersions = 4
	t.Cleanup(func() { keepVersions = before })
	withLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10", "KEEP_VERSIONS_SELF_SET": "chhotabreak"})
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
			r := a.at(t, tc.method, "simple-host.test", tc.path, body, headers)
			if r.status != 413 || r.json(t)["code"] != "site_total_too_large" || !strings.Contains(string(r.body), "1600 px") || !strings.Contains(string(r.body), "currently uses") {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
	a.deploy(t, p, "small")
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
		r := a.at(t, "PUT", "simple-host.test", "/v1/sites/small"+suffix, body, headers)
		// unpublished: existing tiny live copy + incoming 1.1 MB fits initially,
		// so pre-fill history before checking it.
		if strings.Contains(suffix, "publish=false") && r.status == 200 {
			r = a.at(t, "PUT", "simple-host.test", "/v1/sites/small"+suffix, body, headers)
		}
		if r.status != 413 || r.json(t)["code"] != "site_total_too_large" {
			t.Fatalf("update %s: %d %s", suffix, r.status, r.body)
		}
	}
	// The connector goes through the same JSON commit, not its own upload path.
	token := a.connect(t, p, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	fixed := a.rpc(t, token, "tools/call", map[string]any{"name": "set_keep_versions", "arguments": map[string]any{"site": "small", "keep_versions": 10}})
	if !strings.Contains(string(fixed.body), "Simple Host keeps your 4 latest versions") {
		t.Fatalf("MCP retention refusal: %s", fixed.body)
	}

	r := a.rpc(t, token, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{"site": "connector-big", "files": map[string]string{"index.html": payload}}})
	if !strings.Contains(string(r.body), "site_total_too_large") || !strings.Contains(string(r.body), "1600 px") {
		t.Fatalf("connector refusal: %s", r.body)
	}
}

func TestStorageCapsRetentionAndGrandfathering(t *testing.T) {
	a := newPersonApp(t, "serve")
	p := a.newPerson(t, "caps")
	uid, hd := a.userID(t, p)
	before := keepVersions
	keepVersions = 4
	t.Cleanup(func() { keepVersions = before })
	withLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10"})
	headers := map[string]string{"X-API-Key": p.key}
	// Five copies (4 retained + current) at 400 KB stay under 2 MB forever.
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 400<<10)}}
	for i := 0; i < 8; i++ {
		method := "PUT"
		if i == 0 {
			method = "POST"
		}
		r := a.at(t, method, "simple-host.test", "/v1/sites/roll/files", body, headers)
		if r.status != 200 && r.status != 201 {
			t.Fatalf("roll %d: %d %s", i, r.status, r.body)
		}
	}
	site, err := db.GetSiteByUser(context.Background(), a.database, uid, "roll")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := db.ListVersionsBySite(context.Background(), a.database, site.ID)
	if err != nil || len(versions) != 4 {
		t.Fatalf("versions %v %v", versions, err)
	}
	if _, err := os.Stat(a.sites.disk.VersionDir(uid, "roll", 1)); !os.IsNotExist(err) {
		t.Fatalf("old folder remains: %v", err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET keep_versions=100 WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "PUT", "simple-host.test", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 10}, headers)
	if r.status != 403 || r.json(t)["error"] != "Simple Host keeps your 4 latest versions" {
		t.Fatalf("fixed: %d %s", r.status, r.body)
	}
	if r = a.at(t, "PUT", "simple-host.test", "/v1/sites/roll/files", body, headers); r.status != 200 {
		t.Fatalf("ignored setting: %d %s", r.status, r.body)
	}
	versions, _ = db.ListVersionsBySite(context.Background(), a.database, site.ID)
	if len(versions) != 4 {
		t.Fatalf("unauthorized persisted count honored: %d", len(versions))
	}
	// Old sites have no total cap, but still retain 4.
	if _, err := a.database.Exec(`UPDATE sites SET created_at='2025-01-01' WHERE id=$1`, site.ID); err != nil {
		t.Fatal(err)
	}
	big := map[string]any{"files": map[string]string{"index.html": strings.Repeat("b", 1200<<10)}}
	if r = a.at(t, "PUT", "simple-host.test", "/v1/sites/roll/files", big, headers); r.status != 200 {
		t.Fatalf("grandfather: %d %s", r.status, r.body)
	}
	// An old handle grants the allowlist and all three overrides.
	if _, err := a.database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ('allowed',$1)`, uid); err != nil {
		t.Fatal(err)
	}
	withLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "1", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "1", "KEEP_VERSIONS_SELF_SET": "allowed", "KEEP_VERSIONS_OVERRIDES": "allowed:10", "MAX_SITE_TOTAL_OVERRIDES": "allowed:20", "MAX_ACCOUNT_MB_OVERRIDES": "allowed:30"})
	r = a.at(t, "PUT", "simple-host.test", "/v1/sites/roll/keep-versions", map[string]int{"keep_versions": 0}, headers)
	if r.status != 200 || r.json(t)["effective_keep_versions"] != float64(10) {
		t.Fatalf("alias retention %s/%s: %d %s", hd, uid, r.status, r.body)
	}
	if r = a.at(t, "POST", "simple-host.test", "/v1/sites/alias/files", big, headers); r.status != 201 {
		t.Fatalf("alias caps: %d %s", r.status, r.body)
	}
	me := a.at(t, "GET", "simple-host.test", "/v1/me", nil, headers).json(t)
	usage := me["file_usage"].(map[string]any)
	if usage["limit_mb"] != float64(30) || me["can_set_keep_versions"] != true {
		t.Fatalf("usage %v", me)
	}
	if !strings.Contains(usage["tips"].(string), "80%") {
		t.Fatal("missing tips")
	}
}

func TestAccountFileCapAndUsage(t *testing.T) {
	a := newPersonApp(t, "serve")
	p := a.newPerson(t, "accountcap")
	uid, _ := a.userID(t, p)
	before := keepVersions
	keepVersions = 1
	t.Cleanup(func() { keepVersions = before })
	withLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	headers := map[string]string{"X-API-Key": p.key}
	body := map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 300<<10)}}
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/first/files", body, headers)
	if r.status != 201 {
		t.Fatalf("first %s", r.body)
	}
	for i := 0; i < 5; i++ {
		r = a.at(t, "PUT", "simple-host.test", "/v1/sites/first/files", body, headers)
		if r.status != 200 {
			t.Fatalf("replace after pruning %s", r.body)
		}
	}
	r = a.at(t, "POST", "simple-host.test", "/v1/sites/second/files", body, headers)
	if r.status != 413 || r.json(t)["code"] != "account_storage_full" || !strings.Contains(string(r.body), "Delete old sites") {
		t.Fatalf("account cap %d %s", r.status, r.body)
	}
	// Existing files never move on refusal.
	b, err := os.ReadFile(filepath.Join(a.sites.disk.SiteDir(uid, "first"), "current", "index.html"))
	if err != nil || len(b) != 300<<10 {
		t.Fatalf("live changed: %v %d", err, len(b))
	}
	// Backend files do not count; every binary asset in deploy files does.
	if err := os.MkdirAll(filepath.Join(a.sites.disk.SiteDir(uid, "first"), "storage"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.sites.disk.SiteDir(uid, "first"), "storage", "resource"), make([]byte, 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	r = a.at(t, "PUT", "simple-host.test", "/v1/sites/first/files", body, headers)
	if r.status != 200 {
		t.Fatalf("resource excluded %s", r.body)
	}
	me := a.at(t, "GET", "simple-host.test", "/v1/me", nil, headers).json(t)
	usage := me["file_usage"].(map[string]any)
	if usage["used_bytes"] != float64(600<<10) {
		t.Fatalf("file usage %v", usage)
	}
	sites := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, headers)
	var list []map[string]any
	if err := json.Unmarshal(sites.body, &list); err != nil {
		t.Fatal(err)
	}
	if list[0]["file_bytes"] != float64(600<<10) {
		t.Fatalf("sizes %v", list)
	}
	// Deleting keeps the bytes until normal Recently deleted expiry.
	r = a.at(t, "DELETE", "simple-host.test", "/v1/sites/first", nil, headers)
	if r.status != 204 {
		t.Fatalf("delete %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "simple-host.test", "/v1/sites/second/files", body, headers)
	if r.status != 413 {
		t.Fatalf("trash counted %d %s", r.status, r.body)
	}
	// Binary files use the same footprint as text.
	r = a.at(t, "POST", "simple-host.test", "/v1/sites/binary/files", map[string]any{"files": map[string]string{"index.html": "x"}, "files_base64": map[string]string{"large.pdf": base64.StdEncoding.EncodeToString(make([]byte, 400<<10))}}, headers)
	if r.status != 413 || r.json(t)["code"] != "account_storage_full" {
		t.Fatalf("binary counted %d %s", r.status, r.body)
	}
}

func TestPruneFixedAccountsProtectsLiveAndDeleted(t *testing.T) {
	a := newPersonApp(t, "serve")
	fixed := a.newPerson(t, "fixed")
	exempt := a.newPerson(t, "exempt")
	uid, _ := a.userID(t, fixed)
	exID, exHD := a.userID(t, exempt)
	before := keepVersions
	keepVersions = 0
	t.Cleanup(func() { keepVersions = before })
	for _, p := range []person{fixed, exempt} {
		a.deploy(t, p, "history")
		for i := 0; i < 6; i++ {
			r := a.at(t, "PUT", "simple-host.test", "/v1/sites/history/files", map[string]any{"files": map[string]string{"index.html": "new"}}, map[string]string{"X-API-Key": p.key})
			if r.status != 200 {
				t.Fatalf("history %s", r.body)
			}
		}
	}
	headers := map[string]string{"X-API-Key": fixed.key}
	r := a.at(t, "PUT", "simple-host.test", "/v1/sites/history/active-version", map[string]int{"version_number": 1}, headers)
	if r.status != 200 {
		t.Fatalf("rollback %s", r.body)
	}
	a.deploy(t, fixed, "deleted")
	r = a.at(t, "DELETE", "simple-host.test", "/v1/sites/deleted", nil, headers)
	if r.status != 204 {
		t.Fatalf("delete %d", r.status)
	}
	// Make an old-handle exemption prove the operator path also matches aliases.
	if _, err := a.database.Exec(`INSERT INTO handle_aliases (handle,user_id) VALUES ($1,$2)`, exHD+"-old", exID); err != nil {
		t.Fatal(err)
	}
	keepVersions = 4
	withLimits(t, map[string]string{"KEEP_VERSIONS_SELF_SET": exHD + "-old"})
	if _, err := a.database.Exec(`UPDATE sites SET keep_versions=100 WHERE user_id=$1 AND name='history'`, uid); err != nil {
		t.Fatal(err)
	}
	var dry bytes.Buffer
	if err := PruneFixedAccounts(context.Background(), a.database, a.sites.disk, false, &dry); err != nil {
		t.Fatal(err)
	}
	site, _ := db.GetSiteByUser(context.Background(), a.database, uid, "history")
	versions, _ := db.ListVersionsBySite(context.Background(), a.database, site.ID)
	if len(versions) != 7 {
		t.Fatal("dry run removed rows")
	}
	var applied bytes.Buffer
	if err := PruneFixedAccounts(context.Background(), a.database, a.sites.disk, true, &applied); err != nil {
		t.Fatal(err)
	}
	site, _ = db.GetSiteByUser(context.Background(), a.database, uid, "history")
	if site.KeepVersions != 0 {
		t.Fatalf("fixed setting not reset: %d", site.KeepVersions)
	}
	versions, _ = db.ListVersionsBySite(context.Background(), a.database, site.ID)
	if len(versions) != 5 {
		t.Fatalf("want newest 4 plus old live; got %v", versions)
	}
	f, err := a.sites.disk.SiteFootprint(uid, "history")
	if err != nil || f.Versions[1] == 0 || len(f.Versions) != 5 {
		t.Fatalf("live protected %v %v", f, err)
	}
	site, _ = db.GetSiteByUser(context.Background(), a.database, exID, "history")
	versions, _ = db.ListVersionsBySite(context.Background(), a.database, site.ID)
	if len(versions) != 7 {
		t.Fatal("exempt versions touched")
	}
	var deletedID string
	if err := a.database.QueryRow(`SELECT id FROM sites WHERE user_id=$1 AND name='deleted'`, uid).Scan(&deletedID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.TrashDir(uid, deletedID), "v1", "index.html")); err != nil {
		t.Fatalf("deleted touched %v", err)
	}
}

func TestAccountCapSerializesDifferentSites(t *testing.T) {
	a := newPersonApp(t, "serve")
	p := a.newPerson(t, "concurrentcap")
	withLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	start := make(chan struct{})
	results := make(chan resp, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			<-start
			results <- a.at(t, "POST", "simple-host.test", "/v1/sites/"+name+"/files", map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 300<<10)}}, map[string]string{"X-API-Key": p.key})
		}(name)
	}
	close(start)
	created, refused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		switch r.status {
		case 201:
			created++
		case 413:
			refused++
		default:
			t.Fatalf("concurrent deploy %d %s", r.status, r.body)
		}
	}
	if created != 1 || refused != 1 {
		t.Fatalf("created=%d refused=%d", created, refused)
	}
}
