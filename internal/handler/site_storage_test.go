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
	if r := a.at(t, "PUT", pcSiteDomain, base+"/resources/bob-only", map[string]string{"kind": "kv", "read": "anyone", "write": "anyone"}, map[string]string{"X-API-Key": bob.key}); r.status != 201 {
		t.Fatalf("bob resource %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", annHost, base+"/kv/bob-only/keys/k", nil, nil); r.status != 404 {
		t.Fatalf("other site resource %d", r.status)
	}
	if r := a.at(t, "GET", pcSiteDomain, base+"/resources", nil, map[string]string{"X-API-Key": bob.key}); r.status != 200 || strings.Contains(string(r.body), "board") {
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
		r := p.at(t, "PUT", pcSiteDomain, base+"/resources/"+x.name, map[string]string{"kind": "kv", "read": "anyone", "write": "anyone", "site_passcode": x.mode}, p.okey)
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
	if r := p.at(t, "PUT", p.host, base+"/kv/locked/keys/k", `{"value":3}`, browser(p.host, "", "Cookie", cookieHeader(t, unlocked))); r.status != 200 {
		t.Fatalf("unlocked write: %d %s", r.status, r.body)
	}
}
