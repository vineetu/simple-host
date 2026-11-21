package datatest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/server/servertest"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func TestSiteStoragePoliciesAndIsolation(t *testing.T) {
	a, certDir := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "storage-ann")
	bob := a.NewPerson(t, "storage-bob")
	a.Deploy(t, ann, "notes")
	a.Deploy(t, bob, "notes")
	annID, ah := a.UserID(t, ann)
	servertest.MarkReady(t, certDir, ah)
	annHost := "notes." + ah + "." + servertest.PcSiteDomain
	base := "/v1/sites/notes/storage"
	owner := map[string]string{"X-API-Key": ann.Key}
	put := func(name, kind, rules string) {
		t.Helper()
		body := `{"kind":"` + kind + `","site_passcode":"inherit"}`
		if rules != "" {
			body = `{"kind":"` + kind + `","site_passcode":"inherit","rules":` + rules + `}`
		}
		r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/"+name, body, owner)
		if r.Status != 201 {
			t.Fatalf("resource %s: %d %s", name, r.Status, r.Body)
		}
	}
	anyone := servertest.RulesOpenAdd
	put("board", "kv", anyone)
	put("private", "kv", "")
	put("members", "kv", `{"version":1,"policies":[{"name":"read","command":"read","using":"auth.signed_in"},{"name":"add","command":"add","check":"auth.signed_in"},{"name":"edit","command":"edit","using":"auth.signed_in"},{"name":"delete","command":"delete","using":"auth.signed_in"}]}`)
	put("sql", "sqlite", anyone)
	put("writeonly", "sqlite", servertest.RulesInbox)
	put("assets", "files", anyone)
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/bob-only", servertest.StorageRules("kv", anyone), map[string]string{"X-API-Key": bob.Key}); r.Status != 201 {
		t.Fatalf("resource bob-only: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", annHost, base+"/kv/bob-only/keys/k", nil, nil); r.Status != 404 {
		t.Fatalf("other site resource %d", r.Status)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, base+"/resources", nil, map[string]string{"X-API-Key": bob.Key}); r.Status != 200 || strings.Contains(string(r.Body), `"name":"board"`) {
		t.Fatalf("different owner saw resources: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", annHost, base+"/kv/board/keys/hello", `{"value":{"answer":42}}`, servertest.Browser(annHost, "")); r.Status != 200 {
		t.Fatalf("anonymous open write %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", annHost, base+"/kv/board/keys/hello", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), `"answer":42`) {
		t.Fatalf("anonymous open read %d %s", r.Status, r.Body)
	}
	unusedDB := filepath.Join(a.Sites.Disk.SiteDir(annID, "notes"), "runtime", "sqlite", "writeonly.sqlite")
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/writeonly/query", `{"sql":"SELECT 1"}`, owner); r.Status != 200 {
		t.Fatalf("new DB read %d %s", r.Status, r.Body)
	}
	if _, e := os.Stat(unusedDB); !os.IsNotExist(e) {
		t.Fatalf("read-only query created DB: %v", e)
	}
	if r := a.At(t, "GET", annHost, base+"/kv/private/keys/hello", nil, nil); r.Status != 403 {
		t.Fatalf("owner-private read %d", r.Status)
	}
	if r := a.At(t, "PUT", annHost, base+"/kv/members/keys/hello", `{"value":1}`, servertest.Browser(annHost, "")); r.Status != 401 || r.JSON(t)["code"] != "sign_in_required" {
		t.Fatalf("signed-in refused? %d %s", r.Status, r.Body)
	}
	visitorCookie := a.Session(t, bob, a.SiteID(t, ann, "notes"), annHost)
	if r := a.At(t, "PUT", annHost, base+"/kv/members/keys/hello", `{"value":1}`, servertest.Browser(annHost, visitorCookie)); r.Status != 200 {
		t.Fatalf("signed-in write %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, base+"/kv/board/keys/hello", nil, nil); r.Status == 200 {
		t.Fatal("apex anonymous read accepted")
	}
	if r := a.At(t, "GET", annHost, base+"/kv/board/keys/hello", nil, map[string]string{"Origin": "https://evil.example"}); r.Status == 200 {
		t.Fatal("cross-site origin accepted")
	}
	for _, method := range []string{"GET", "OPTIONS"} {
		r := a.At(t, method, annHost, base+"/kv/board/keys/hello", nil, map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"})
		if r.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s gave cross-site CORS %q", method, r.Header.Get("Access-Control-Allow-Origin"))
		}
	}
	if r := a.At(t, "POST", annHost, base+"/sqlite/sql/schema", `{"sql":"CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT)"}`, servertest.Browser(annHost, "")); r.Status == 200 {
		t.Fatal("visitor schema accepted")
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/sql/schema", `{"sql":"CREATE TABLE notes (id INTEGER PRIMARY KEY, text TEXT)"}`, owner); r.Status != 200 {
		t.Fatalf("owner schema %d %s", r.Status, r.Body)
	}
	// Pages write and read through the table routes; page SQL is refused.
	if r := a.At(t, "POST", annHost, base+"/sqlite/sql/execute", `{"sql":"INSERT INTO notes(text) VALUES(?)","params":["one"]}`, servertest.Browser(annHost, "")); r.Status != 403 || r.JSON(t)["code"] != "fixed_routes_required" {
		t.Fatalf("page SQL write %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", annHost, base+"/sqlite/sql/query", `{"sql":"SELECT text FROM notes"}`, servertest.Browser(annHost, "")); r.Status != 403 || r.JSON(t)["code"] != "fixed_routes_required" {
		t.Fatalf("page SQL read %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", annHost, base+"/sqlite/sql/tables/notes/rows", `{"text":"one"}`, servertest.Browser(annHost, "")); r.Status != 200 {
		t.Fatalf("open row write %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", annHost, base+"/sqlite/sql/tables/notes/rows", nil, servertest.Browser(annHost, "")); r.Status != 200 || !strings.Contains(string(r.Body), "one") {
		t.Fatalf("open row read %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/writeonly/schema", `{"sql":"CREATE TABLE items (n INTEGER)"}`, owner); r.Status != 200 {
		t.Fatalf("write-only schema %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", annHost, base+"/sqlite/writeonly/tables/items/rows", `{"n":1}`, servertest.Browser(annHost, "")); r.Status != 200 {
		t.Fatalf("write-only insert %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", annHost, base+"/sqlite/writeonly/tables/items/rows", nil, servertest.Browser(annHost, "")); r.Status == 200 {
		t.Fatal("write-only table read existing rows")
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/sql/query", `{"sql":"ATTACH DATABASE '/etc/passwd' AS other"}`, owner); r.Status == 200 {
		t.Fatal("attach accepted")
	}
	if r := a.At(t, "PUT", annHost, base+"/files/assets/objects/photo.txt", "hello", servertest.Browser(annHost, "", "Content-Type", "text/plain")); r.Status != 200 {
		t.Fatalf("file write %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", annHost, base+"/files/assets/objects/photo.txt", nil, nil); r.Status != 200 || string(r.Body) != "hello" || r.Header.Get("Content-Disposition") == "" {
		t.Fatalf("file read %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", annHost, base+"/files/assets/download-link", `{"path":"photo.txt"}`, servertest.Browser(annHost, "")); r.Status == 200 {
		t.Fatal("anonymous file link minted")
	}
	link := a.At(t, "POST", servertest.PcSiteDomain, base+"/files/assets/download-link", `{"path":"photo.txt"}`, owner)
	if link.Status != 200 {
		t.Fatalf("file link %d %s", link.Status, link.Body)
	}
	u, err := url.Parse(link.JSON(t)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, u.RequestURI(), nil, nil); r.Status != 200 || string(r.Body) != "hello" {
		t.Fatalf("linked bytes %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/notes/export.tar.gz", nil, owner); r.Status != 200 {
		t.Fatalf("export %d %s", r.Status, r.Body)
	} else {
		names := servertest.TarNames(t, r.Body)
		for _, n := range []string{"notes/storage/resources.json", "notes/storage/kv.json", "notes/storage/runtime/sqlite/sql.sqlite", "notes/storage/runtime/files/assets/photo.txt"} {
			if !names[n] {
				t.Errorf("export missing %s", n)
			}
		}
	}
	if r := a.At(t, "GET", annHost, base+"/files/assets/objects/../secret", nil, nil); r.Status == http.StatusOK {
		t.Fatal("traversal accepted")
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, base+"/resources/assets", nil, owner); r.Status != 200 {
		t.Fatalf("resource delete %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, u.RequestURI(), nil, nil); r.Status != 404 {
		t.Fatalf("deleted resource link alive %d", r.Status)
	}
}

func TestSiteStorageInheritsPasscodeAndExplicitOff(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &p.Sites.Limits, &p.Access.Limits, &p.Data.Limits)
	base := "/v1/sites/trip/storage"
	for _, x := range []struct{ name, mode string }{{"locked", "inherit"}, {"open", "off"}} {
		r := p.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/"+x.name, `{"kind":"kv","site_passcode":"`+x.mode+`","rules":`+servertest.RulesOpenAdd+`}`, p.Okey)
		if r.Status != 201 {
			t.Fatalf("resource %s: %d %s", x.name, r.Status, r.Body)
		}
	}
	if r := p.Lock(t, "123456"); r.Status != 200 {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	for _, m := range []string{"GET", "PUT"} {
		var body any
		if m == "PUT" {
			body = `{"value":1}`
		}
		if r := p.At(t, m, p.Host, base+"/kv/locked/keys/k", body, servertest.Browser(p.Host, "")); r.Status != 403 || r.JSON(t)["code"] != "site_locked" {
			t.Fatalf("locked %s: %d %s", m, r.Status, r.Body)
		}
	}
	if r := p.At(t, "PUT", p.Host, base+"/kv/open/keys/k", `{"value":1}`, servertest.Browser(p.Host, "")); r.Status != 200 {
		t.Fatalf("off open: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, base+"/kv/locked/keys/k", `{"value":2}`, p.Okey); r.Status != 200 {
		t.Fatalf("owner bypass: %d %s", r.Status, r.Body)
	}
	unlocked := p.Unlock(t, p.Host, "123456", "/", nil)
	if unlocked.Status != 303 {
		t.Fatalf("unlock: %d %s", unlocked.Status, unlocked.Body)
	}
	if r := p.At(t, "PUT", p.Host, base+"/kv/locked/keys/k2", `{"value":3}`, servertest.Browser(p.Host, "", "Cookie", servertest.CookieHeader(t, unlocked))); r.Status != 200 {
		t.Fatalf("unlocked write: %d %s", r.Status, r.Body)
	}
}

func TestSiteStorageUsageAndExactFileBoundary(t *testing.T) {
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "1000000")
	t.Setenv("SITE_STORAGE_MAX_BYTES", "1000000")
	a := servertest.NewPrivateApp(t)
	owner := a.NewPerson(t, "storage-cap")
	a.Deploy(t, owner, "media")
	base := "/v1/sites/media/storage"
	key := map[string]string{"X-API-Key": owner.Key}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/photos", map[string]string{"kind": "files"}, key); r.Status != 201 {
		t.Fatalf("resource %d %s", r.Status, r.Body)
	}
	usage := func() (float64, float64, float64) {
		t.Helper()
		r := a.At(t, "GET", servertest.PcSiteDomain, base+"/usage", nil, key)
		if r.Status != 200 {
			t.Fatalf("usage %d %s", r.Status, r.Body)
		}
		j := r.JSON(t)
		b := j["breakdown"].(map[string]any)
		return j["used_bytes"].(float64), j["remaining_bytes"].(float64), b["files_bytes"].(float64)
	}
	other := a.NewPerson(t, "storage-cap-other")
	if r := a.At(t, "GET", servertest.PcSiteDomain, base+"/usage", nil, map[string]string{"X-API-Key": other.Key}); r.Status != 403 {
		t.Fatalf("other owner usage %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, base+"/usage", nil, nil); r.Status == 200 {
		t.Fatal("anonymous owner usage")
	}
	if used, left, files := usage(); used != 0 || left != 1000000 || files != 0 {
		t.Fatalf("initial usage %v %v %v", used, left, files)
	}
	full := strings.Repeat("a", 1000000)
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/files/photos/objects/a.bin", full, key); r.Status != 200 {
		t.Fatalf("exact cap %d %s", r.Status, r.Body)
	}
	if used, left, files := usage(); used != 0 || left != 1000000 || files != 1000000 {
		t.Fatalf("full usage %v %v %v", used, left, files)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/files/photos/objects/a.bin", strings.Repeat("b", 1000000), key); r.Status != 200 {
		t.Fatalf("same-size replacement %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/files/photos/objects/b.bin", "x", key); r.Status != 507 || r.JSON(t)["code"] != "site_full" {
		t.Fatalf("growth beyond cap %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, base+"/files/photos/objects/a.bin", nil, key); r.Status != 200 || len(r.Body) != 1000000 || r.Body[0] != 'b' {
		t.Fatalf("replacement changed on rejection %d len=%d", r.Status, len(r.Body))
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, base+"/files/photos/objects/a.bin", nil, key); r.Status != 200 {
		t.Fatalf("delete %d %s", r.Status, r.Body)
	}
	if used, left, files := usage(); used != 0 || left != 1000000 || files != 0 {
		t.Fatalf("after delete %v %v %v", used, left, files)
	}
}

func TestSiteStorageKVNormalizedQuotaAndSQLiteFull(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	owner := a.NewPerson(t, "storage-small")
	a.Deploy(t, owner, "small")
	base := "/v1/sites/small/storage"
	key := map[string]string{"X-API-Key": owner.Key}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/kv", map[string]string{"kind": "kv"}, key); r.Status != 201 {
		t.Fatalf("kv resource %d %s", r.Status, r.Body)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "3")
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/kv/kv/keys/n", `{"value":1e3}`, key); r.Status != 507 {
		t.Fatalf("normalized number over cap %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", servertest.PcSiteDomain, base+"/kv/kv/keys/n", nil, key); r.Status != 404 {
		t.Fatalf("rejected KV changed data %d", r.Status)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "32768")
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/sql", map[string]string{"kind": "sqlite"}, key); r.Status != 201 {
		t.Fatalf("sql resource %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/sql/schema", `{"sql":"CREATE TABLE items (v BLOB)"}`, key); r.Status != 200 {
		t.Fatalf("schema %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/sql/execute", `{"sql":"INSERT INTO items(v) VALUES(randomblob(50000))"}`, key); r.Status != 507 || r.JSON(t)["code"] != "sqlite_full" {
		t.Fatalf("SQLite full %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/sql/query", `{"sql":"SELECT COUNT(*) FROM items"}`, key); r.Status != 200 || !strings.Contains(string(r.Body), `[[0]]`) {
		t.Fatalf("SQL changed on rejection %d %s", r.Status, r.Body)
	}
	r := a.At(t, "GET", servertest.PcSiteDomain, base+"/usage", nil, key)
	if r.Status != 200 {
		t.Fatalf("usage %d %s", r.Status, r.Body)
	}
	j := r.JSON(t)
	if j["used_bytes"].(float64) > 32768 {
		t.Fatalf("SQL usage exceeded cap %s", r.Body)
	}
}

func TestSiteStorageSurvivesDeployRenameAndRestore(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	owner := a.NewPerson(t, "storage-lifecycle")
	a.Deploy(t, owner, "first")
	key := map[string]string{"X-API-Key": owner.Key}
	base := "/v1/sites/first/storage"
	for _, resource := range []struct{ name, kind string }{{"facts", "kv"}, {"records", "sqlite"}, {"photos", "files"}} {
		if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/resources/"+resource.name, map[string]string{"kind": resource.kind}, key); r.Status != 201 {
			t.Fatalf("declare %s: %d %s", resource.name, r.Status, r.Body)
		}
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/kv/facts/keys/k", `{"value":"kept"}`, key); r.Status != 200 {
		t.Fatalf("KV write: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/records/schema", `{"sql":"CREATE TABLE entries (v TEXT)"}`, key); r.Status != 200 {
		t.Fatalf("schema: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, base+"/sqlite/records/execute", `{"sql":"INSERT INTO entries(v) VALUES('kept')"}`, key); r.Status != 200 {
		t.Fatalf("SQL write: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.PcSiteDomain, base+"/files/photos/objects/a.bin", "kept", key); r.Status != 200 {
		t.Fatalf("file write: %d %s", r.Status, r.Body)
	}
	// A new deployment changes published assets but must leave runtime data in place.
	if r := a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/first/files", map[string]any{"files": map[string]string{"index.html": "<h1>new version</h1>"}}, key); r.Status != 200 {
		t.Fatalf("redeploy: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/sites/first", map[string]string{"name": "second"}, key); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/sites/second", nil, key); r.Status != http.StatusNoContent {
		t.Fatalf("trash: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/second/restore", nil, key); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	base = "/v1/sites/second/storage"
	for _, tc := range []struct{ method, path, body, want string }{
		{"GET", "/kv/facts/keys/k", "", "kept"},
		{"POST", "/sqlite/records/query", `{"sql":"SELECT v FROM entries"}`, "kept"},
		{"GET", "/files/photos/objects/a.bin", "", "kept"},
	} {
		r := a.At(t, tc.method, servertest.PcSiteDomain, base+tc.path, tc.body, key)
		if r.Status != 200 || !strings.Contains(string(r.Body), tc.want) {
			t.Fatalf("%s after lifecycle: %d %s", tc.path, r.Status, r.Body)
		}
	}
}

func insertStorageKVRace(t *testing.T, h *datahttp.DataHandler, c datahttp.StorageCall, key, value, writer string) (bool, error) {
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.StorageRuntimeDir(c), c.ResourceName), true, false)
	if err != nil {
		return false, err
	}
	defer done()
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return false, err
	}
	defer conn.Exec("ROLLBACK")
	if err = runtime.RuntimeKVPut(conn, key, value, writer, false, 0); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT) {
			return false, nil
		}
		return false, err
	}
	return true, conn.Exec("COMMIT")
}
