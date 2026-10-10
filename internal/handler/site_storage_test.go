package handler

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

func TestSiteStoragePoliciesAndIsolation(t *testing.T) {
	a, certDir := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "storage-ann")
	bob := a.newPerson(t, "storage-bob")
	a.deploy(t, ann, "notes")
	a.deploy(t, bob, "notes")
	annID, ah := a.userID(t, ann)
	markReady(t, certDir, ah)
	annHost := "notes." + ah + "." + pcSiteDomain
	base := "/v1/sites/notes/storage"
	owner := map[string]string{"X-API-Key": ann.key}
	put := func(name, kind, read, write string) {
		t.Helper()
		if legacyStorageRules(kind, read, write, "full") {
			insertLegacyStorage(t, a.sites.database, a.siteID(t, ann, "notes"), name, kind, read, write, "full", "inherit")
			return
		}
		r := a.at(t, "PUT", pcSiteDomain, base+"/resources/"+name, map[string]string{"kind": kind, "read": read, "write": write, "site_passcode": "inherit"}, owner)
		if r.status != 201 {
			t.Fatalf("resource %s: %d %s", name, r.status, r.body)
		}
	}
	put("board", "kv", "anyone", "anyone")
	put("private", "kv", "owner", "owner")
	put("members", "kv", "signed-in", "signed-in")
	put("sql", "sqlite", "anyone", "anyone")
	put("writeonly", "sqlite", "owner", "anyone")
	put("assets", "files", "anyone", "anyone")
	insertLegacyStorage(t, a.sites.database, a.siteID(t, bob, "notes"), "bob-only", "kv", "anyone", "anyone", "full", "")
	if r := a.at(t, "GET", annHost, base+"/kv/bob-only/keys/k", nil, nil); r.status != 404 {
		t.Fatalf("other site resource %d", r.status)
	}
	if r := a.at(t, "GET", pcSiteDomain, base+"/resources", nil, map[string]string{"X-API-Key": bob.key}); r.status != 200 || strings.Contains(string(r.body), `"name":"board"`) {
		t.Fatalf("different owner saw resources: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", annHost, base+"/kv/board/keys/hello", `{"value":{"answer":42}}`, browser(annHost, "")); r.status != 200 {
		t.Fatalf("anonymous open write %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", annHost, base+"/kv/board/keys/hello", nil, nil); r.status != 200 || !strings.Contains(string(r.body), `"answer":42`) {
		t.Fatalf("anonymous open read %d %s", r.status, r.body)
	}
	unusedDB := filepath.Join(a.sites.disk.SiteDir(annID, "notes"), "runtime", "sqlite", "writeonly.sqlite")
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/writeonly/query", `{"sql":"SELECT 1"}`, owner); r.status != 200 {
		t.Fatalf("new DB read %d %s", r.status, r.body)
	}
	if _, e := os.Stat(unusedDB); !os.IsNotExist(e) {
		t.Fatalf("read-only query created DB: %v", e)
	}
	if r := a.at(t, "GET", annHost, base+"/kv/private/keys/hello", nil, nil); r.status != 403 {
		t.Fatalf("owner-private read %d", r.status)
	}
	if r := a.at(t, "PUT", annHost, base+"/kv/members/keys/hello", `{"value":1}`, browser(annHost, "")); r.status != 401 || r.json(t)["code"] != "sign_in_required" {
		t.Fatalf("signed-in refused? %d %s", r.status, r.body)
	}
	visitorCookie := a.session(t, bob, a.siteID(t, ann, "notes"), annHost)
	if r := a.at(t, "PUT", annHost, base+"/kv/members/keys/hello", `{"value":1}`, browser(annHost, visitorCookie)); r.status != 200 {
		t.Fatalf("signed-in write %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, base+"/kv/board/keys/hello", nil, nil); r.status == 200 {
		t.Fatal("apex anonymous read accepted")
	}
	if r := a.at(t, "GET", annHost, base+"/kv/board/keys/hello", nil, map[string]string{"Origin": "https://evil.example"}); r.status == 200 {
		t.Fatal("cross-site origin accepted")
	}
	for _, method := range []string{"GET", "OPTIONS"} {
		r := a.at(t, method, annHost, base+"/kv/board/keys/hello", nil, map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"})
		if r.header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s gave cross-site CORS %q", method, r.header.Get("Access-Control-Allow-Origin"))
		}
	}
	if r := a.at(t, "POST", annHost, base+"/sqlite/sql/schema", `{"sql":"CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT)"}`, browser(annHost, "")); r.status == 200 {
		t.Fatal("visitor schema accepted")
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/sql/schema", `{"sql":"CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT)"}`, owner); r.status != 200 {
		t.Fatalf("owner schema %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", annHost, base+"/sqlite/sql/execute", `{"sql":"INSERT INTO notes(text) VALUES(?)","params":["one"]}`, browser(annHost, "")); r.status != 200 {
		t.Fatalf("open SQL write %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", annHost, base+"/sqlite/sql/query", `{"sql":"SELECT text FROM notes"}`, browser(annHost, "")); r.status != 200 || !strings.Contains(string(r.body), "one") {
		t.Fatalf("open SQL read %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/writeonly/schema", `{"sql":"CREATE TABLE items (n INTEGER)"}`, owner); r.status != 200 {
		t.Fatalf("write-only schema %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", annHost, base+"/sqlite/writeonly/execute", `{"sql":"INSERT INTO items(n) VALUES(1)"}`, browser(annHost, "")); r.status != 200 {
		t.Fatalf("write-only insert %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", annHost, base+"/sqlite/writeonly/execute", `{"sql":"INSERT INTO items(n) SELECT n FROM items"}`, browser(annHost, "")); r.status == 200 {
		t.Fatal("write-only SQL read existing rows")
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/sql/query", `{"sql":"ATTACH DATABASE '/etc/passwd' AS other"}`, owner); r.status == 200 {
		t.Fatal("attach accepted")
	}
	if r := a.at(t, "PUT", annHost, base+"/files/assets/objects/photo.txt", "hello", browser(annHost, "", "Content-Type", "text/plain")); r.status != 200 {
		t.Fatalf("file write %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", annHost, base+"/files/assets/objects/photo.txt", nil, nil); r.status != 200 || string(r.body) != "hello" || r.header.Get("Content-Disposition") == "" {
		t.Fatalf("file read %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", annHost, base+"/files/assets/download-link", `{"path":"photo.txt"}`, browser(annHost, "")); r.status == 200 {
		t.Fatal("anonymous file link minted")
	}
	link := a.at(t, "POST", pcSiteDomain, base+"/files/assets/download-link", `{"path":"photo.txt"}`, owner)
	if link.status != 200 {
		t.Fatalf("file link %d %s", link.status, link.body)
	}
	u, err := url.Parse(link.json(t)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", pcSiteDomain, u.RequestURI(), nil, nil); r.status != 200 || string(r.body) != "hello" {
		t.Fatalf("linked bytes %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/notes/export.tar.gz", nil, owner); r.status != 200 {
		t.Fatalf("export %d %s", r.status, r.body)
	} else {
		names := tarNames(t, r.body)
		for _, n := range []string{"notes/storage/resources.json", "notes/storage/kv.json", "notes/storage/runtime/sqlite/sql.sqlite", "notes/storage/runtime/files/assets/photo.txt"} {
			if !names[n] {
				t.Errorf("export missing %s", n)
			}
		}
	}
	if r := a.at(t, "GET", annHost, base+"/files/assets/objects/../secret", nil, nil); r.status == http.StatusOK {
		t.Fatal("traversal accepted")
	}
	if r := a.at(t, "DELETE", pcSiteDomain, base+"/resources/assets", nil, owner); r.status != 200 {
		t.Fatalf("resource delete %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, u.RequestURI(), nil, nil); r.status != 404 {
		t.Fatalf("deleted resource link alive %d", r.status)
	}
}

func TestSiteStorageInheritsPasscodeAndExplicitOff(t *testing.T) {
	p := newPasscodeApp(t)
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true })
	base := "/v1/sites/trip/storage"
	for _, x := range []struct{ name, mode string }{{"locked", "inherit"}, {"open", "off"}} {
		r := p.at(t, "PUT", pcSiteDomain, base+"/resources/"+x.name, map[string]string{"kind": "kv", "preset": "public", "add": "anyone", "site_passcode": x.mode}, p.okey)
		if r.status != 201 {
			t.Fatalf("resource %s: %d %s", x.name, r.status, r.body)
		}
	}
	if r := p.lock(t, "123456"); r.status != 200 {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	for _, m := range []string{"GET", "PUT"} {
		var body any
		if m == "PUT" {
			body = `{"value":1}`
		}
		if r := p.at(t, m, p.host, base+"/kv/locked/keys/k", body, browser(p.host, "")); r.status != 403 || r.json(t)["code"] != "site_locked" {
			t.Fatalf("locked %s: %d %s", m, r.status, r.body)
		}
	}
	if r := p.at(t, "PUT", p.host, base+"/kv/open/keys/k", `{"value":1}`, browser(p.host, "")); r.status != 200 {
		t.Fatalf("off open: %d %s", r.status, r.body)
	}
	if r := p.at(t, "PUT", pcSiteDomain, base+"/kv/locked/keys/k", `{"value":2}`, p.okey); r.status != 200 {
		t.Fatalf("owner bypass: %d %s", r.status, r.body)
	}
	unlocked := p.unlock(t, p.host, "123456", "/", nil)
	if unlocked.status != 303 {
		t.Fatalf("unlock: %d %s", unlocked.status, unlocked.body)
	}
	if r := p.at(t, "PUT", p.host, base+"/kv/locked/keys/k2", `{"value":3}`, browser(p.host, "", "Cookie", cookieHeader(t, unlocked))); r.status != 200 {
		t.Fatalf("unlocked write: %d %s", r.status, r.body)
	}
}

func TestSiteStorageUsageAndExactFileBoundary(t *testing.T) {
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "1000000")
	t.Setenv("SITE_STORAGE_MAX_BYTES", "1000000")
	a := newPrivateApp(t)
	owner := a.newPerson(t, "storage-cap")
	a.deploy(t, owner, "media")
	base := "/v1/sites/media/storage"
	key := map[string]string{"X-API-Key": owner.key}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/resources/photos", map[string]string{"kind": "files"}, key); r.status != 201 {
		t.Fatalf("resource %d %s", r.status, r.body)
	}
	usage := func() (float64, float64, float64) {
		t.Helper()
		r := a.at(t, "GET", pcSiteDomain, base+"/usage", nil, key)
		if r.status != 200 {
			t.Fatalf("usage %d %s", r.status, r.body)
		}
		j := r.json(t)
		b := j["breakdown"].(map[string]any)
		return j["used_bytes"].(float64), j["remaining_bytes"].(float64), b["files_bytes"].(float64)
	}
	other := a.newPerson(t, "storage-cap-other")
	if r := a.at(t, "GET", pcSiteDomain, base+"/usage", nil, map[string]string{"X-API-Key": other.key}); r.status != 403 {
		t.Fatalf("other owner usage %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, base+"/usage", nil, nil); r.status == 200 {
		t.Fatal("anonymous owner usage")
	}
	if used, left, files := usage(); used != 0 || left != 1000000 || files != 0 {
		t.Fatalf("initial usage %v %v %v", used, left, files)
	}
	full := strings.Repeat("a", 1000000)
	if r := a.at(t, "PUT", pcSiteDomain, base+"/files/photos/objects/a.bin", full, key); r.status != 200 {
		t.Fatalf("exact cap %d %s", r.status, r.body)
	}
	if used, left, files := usage(); used != 0 || left != 1000000 || files != 1000000 {
		t.Fatalf("full usage %v %v %v", used, left, files)
	}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/files/photos/objects/a.bin", strings.Repeat("b", 1000000), key); r.status != 200 {
		t.Fatalf("same-size replacement %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/files/photos/objects/b.bin", "x", key); r.status != 507 || r.json(t)["code"] != "site_full" {
		t.Fatalf("growth beyond cap %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, base+"/files/photos/objects/a.bin", nil, key); r.status != 200 || len(r.body) != 1000000 || r.body[0] != 'b' {
		t.Fatalf("replacement changed on rejection %d len=%d", r.status, len(r.body))
	}
	if r := a.at(t, "DELETE", pcSiteDomain, base+"/files/photos/objects/a.bin", nil, key); r.status != 200 {
		t.Fatalf("delete %d %s", r.status, r.body)
	}
	if used, left, files := usage(); used != 0 || left != 1000000 || files != 0 {
		t.Fatalf("after delete %v %v %v", used, left, files)
	}
}

func TestSiteStorageKVNormalizedQuotaAndSQLiteFull(t *testing.T) {
	a := newPrivateApp(t)
	owner := a.newPerson(t, "storage-small")
	a.deploy(t, owner, "small")
	base := "/v1/sites/small/storage"
	key := map[string]string{"X-API-Key": owner.key}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/resources/kv", map[string]string{"kind": "kv"}, key); r.status != 201 {
		t.Fatalf("kv resource %d %s", r.status, r.body)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "3")
	if r := a.at(t, "PUT", pcSiteDomain, base+"/kv/kv/keys/n", `{"value":1e3}`, key); r.status != 507 {
		t.Fatalf("normalized number over cap %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, base+"/kv/kv/keys/n", nil, key); r.status != 404 {
		t.Fatalf("rejected KV changed data %d", r.status)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "32768")
	if r := a.at(t, "PUT", pcSiteDomain, base+"/resources/sql", map[string]string{"kind": "sqlite"}, key); r.status != 201 {
		t.Fatalf("sql resource %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/sql/schema", `{"sql":"CREATE TABLE items (v BLOB)"}`, key); r.status != 200 {
		t.Fatalf("schema %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/sql/execute", `{"sql":"INSERT INTO items(v) VALUES(randomblob(50000))"}`, key); r.status != 507 || r.json(t)["code"] != "sqlite_full" {
		t.Fatalf("SQLite full %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/sql/query", `{"sql":"SELECT COUNT(*) FROM items"}`, key); r.status != 200 || !strings.Contains(string(r.body), `[[0]]`) {
		t.Fatalf("SQL changed on rejection %d %s", r.status, r.body)
	}
	r := a.at(t, "GET", pcSiteDomain, base+"/usage", nil, key)
	if r.status != 200 {
		t.Fatalf("usage %d %s", r.status, r.body)
	}
	j := r.json(t)
	if j["used_bytes"].(float64) > 32768 {
		t.Fatalf("SQL usage exceeded cap %s", r.body)
	}
}

func TestSiteStorageSurvivesDeployRenameAndRestore(t *testing.T) {
	a := newPrivateApp(t)
	owner := a.newPerson(t, "storage-lifecycle")
	a.deploy(t, owner, "first")
	key := map[string]string{"X-API-Key": owner.key}
	base := "/v1/sites/first/storage"
	for _, resource := range []struct{ name, kind string }{{"facts", "kv"}, {"records", "sqlite"}, {"photos", "files"}} {
		if r := a.at(t, "PUT", pcSiteDomain, base+"/resources/"+resource.name, map[string]string{"kind": resource.kind}, key); r.status != 201 {
			t.Fatalf("declare %s: %d %s", resource.name, r.status, r.body)
		}
	}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/kv/facts/keys/k", `{"value":"kept"}`, key); r.status != 200 {
		t.Fatalf("KV write: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/records/schema", `{"sql":"CREATE TABLE entries (v TEXT)"}`, key); r.status != 200 {
		t.Fatalf("schema: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/records/execute", `{"sql":"INSERT INTO entries(v) VALUES('kept')"}`, key); r.status != 200 {
		t.Fatalf("SQL write: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/files/photos/objects/a.bin", "kept", key); r.status != 200 {
		t.Fatalf("file write: %d %s", r.status, r.body)
	}
	// A new deployment changes published assets but must leave runtime data in place.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/first/files", map[string]any{"files": map[string]string{"index.html": "<h1>new version</h1>"}}, key); r.status != 200 {
		t.Fatalf("redeploy: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/sites/first", map[string]string{"name": "second"}, key); r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/second", nil, key); r.status != http.StatusNoContent {
		t.Fatalf("trash: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/second/restore", nil, key); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	base = "/v1/sites/second/storage"
	for _, tc := range []struct{ method, path, body, want string }{
		{"GET", "/kv/facts/keys/k", "", "kept"},
		{"POST", "/sqlite/records/query", `{"sql":"SELECT v FROM entries"}`, "kept"},
		{"GET", "/files/photos/objects/a.bin", "", "kept"},
	} {
		r := a.at(t, tc.method, pcSiteDomain, base+tc.path, tc.body, key)
		if r.status != 200 || !strings.Contains(string(r.body), tc.want) {
			t.Fatalf("%s after lifecycle: %d %s", tc.path, r.status, r.body)
		}
	}
}
