package servertest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/handler"
)

func rdTarFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a gzip (%d bytes): %v", len(b), err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[hdr.Name] = string(body)
	}
	return out
}

// Redeploying, renaming, deleting and restoring a site keep the bytes of all
// three kinds of saved data.
func TestRDStorageSurvivesDeployRenameDeleteRestore(t *testing.T) {
	a := NewPrivateApp(t)
	owner := a.NewPerson(t, "storage-lifecycle")
	a.Deploy(t, owner, "first")
	key := map[string]string{"X-API-Key": owner.Key}
	do := func(method, path string, body any, status int) Resp {
		t.Helper()
		r := a.At(t, method, PcSiteDomain, path, body, key)
		if r.Status != status {
			t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.Status, r.Body)
		}
		return r
	}
	base := "/v1/sites/first/storage"
	for _, resource := range []struct{ name, kind string }{{"facts", "kv"}, {"records", "sqlite"}, {"photos", "files"}} {
		do("PUT", base+"/resources/"+resource.name, map[string]string{"kind": resource.kind}, 201)
	}
	do("PUT", base+"/kv/facts/keys/k", `{"value":"kept"}`, 200)
	do("POST", base+"/sqlite/records/schema", `{"sql":"CREATE TABLE entries (v TEXT)"}`, 200)
	do("POST", base+"/sqlite/records/execute", `{"sql":"INSERT INTO entries(v) VALUES('kept')"}`, 200)
	do("PUT", base+"/files/photos/objects/a.bin", "kept", 200)

	check := func(stage, base string) {
		t.Helper()
		for _, tc := range []struct{ method, path, body string }{
			{"GET", "/kv/facts/keys/k", ""},
			{"POST", "/sqlite/records/query", `{"sql":"SELECT v FROM entries"}`},
			{"GET", "/files/photos/objects/a.bin", ""},
		} {
			var body any
			if tc.body != "" {
				body = tc.body
			}
			r := do(tc.method, base+tc.path, body, 200)
			if !strings.Contains(string(r.Body), "kept") {
				t.Fatalf("%s: %s lost its data: %s", stage, tc.path, r.Body)
			}
		}
	}
	do("PUT", "/v1/sites/first/files", map[string]any{"files": map[string]string{"index.html": "<h1>new version</h1>"}}, 200)
	check("redeploy", base)
	do("PATCH", "/v1/sites/first", map[string]string{"name": "second"}, 200)
	base = "/v1/sites/second/storage"
	check("rename", base)
	do("DELETE", "/v1/sites/second", nil, http.StatusNoContent)
	do("POST", "/v1/sites/second/restore", nil, 200)
	check("restore", base)
}

// The site export and the account export carry every resource's rules
// (owner-only stays owner-only), KV as portable JSON, and the SQLite and
// file bytes.
func TestRDRecoveryExportsKeepPoliciesAndBytes(t *testing.T) {
	s := NewStorageTestSite(t)
	private := s.Must(t, "PUT", "/resources/private", `{"kind":"sqlite"}`, s.Key, 201).JSON(t)
	delete(private, "warnings")
	saved := s.Must(t, "PUT", "/resources/rules", `{"kind":"sqlite","rules":{"policies":[{"name":"read","command":"read","using":"true"}]},"tables":{"notes":{"rules":{"policies":[{"name":"owner","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}},"closed":{"rules":{"policies":[]}}}}`, s.Key, 201).JSON(t)
	s.Must(t, "POST", "/sqlite/rules/schema", map[string]any{"sql": "CREATE TABLE notes(v TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/rules/execute", map[string]any{"sql": "INSERT INTO notes(v) VALUES('sqlite-bytes-kept')"}, s.Key, 200)
	s.Must(t, "PUT", "/resources/facts", map[string]string{"kind": "kv"}, s.Key, 201)
	for _, k := range []string{"a", "é", "中"} {
		s.Must(t, "PUT", "/kv/facts/keys/"+url.PathEscape(k), `{"value":{"text":"é","n":1.0e2}}`, s.Key, 200)
	}
	s.Must(t, "PUT", "/resources/uploads", `{"kind":"files"}`, s.Key, 201)
	s.Must(t, "PUT", "/files/uploads/objects/keep.txt", "file-bytes-kept", s.Key, 200)

	for _, tc := range []struct {
		name, path string
		files      func(*testing.T, []byte) map[string]string
	}{
		{"site", "/v1/sites/shop/export.tar.gz", rdTarFiles},
		{"account", "/v1/me/export.zip", ZipFiles},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.A.At(t, "GET", PcSiteDomain, tc.path, nil, s.Key)
			if r.Status != 200 {
				t.Fatalf("export: %d %s", r.Status, r.Body)
			}
			files := tc.files(t, r.Body)
			var archive struct {
				Resources []map[string]any `json:"resources"`
			}
			if err := json.Unmarshal([]byte(FileUnder(t, files, "shop/storage/resources.json")), &archive); err != nil {
				t.Fatal(err)
			}
			if len(archive.Resources) != 4 {
				t.Fatalf("exported resources: %#v", archive.Resources)
			}
			for _, resource := range archive.Resources {
				switch resource["name"] {
				case "private":
					if !reflect.DeepEqual(resource, private) {
						t.Fatalf("owner-only export changed: %#v; want %#v", resource, private)
					}
				case "rules":
					if resource["rules_status"] != "rules" || !reflect.DeepEqual(resource["rules"], saved["rules"]) || !reflect.DeepEqual(resource["tables"], saved["tables"]) {
						t.Fatalf("export lost rules: %#v; saved %#v", resource, saved)
					}
				}
			}
			if got := FileUnder(t, files, "shop/storage/runtime/files/uploads/keep.txt"); got != "file-bytes-kept" {
				t.Fatalf("file bytes: %q", got)
			}
			var sqliteFile string
			for name, body := range files {
				if strings.Contains(name, "/runtime/sqlite/rules") {
					sqliteFile = body
				}
				if strings.Contains(name, "/runtime/kv/") {
					t.Fatalf("raw KV file exported: %s", name)
				}
			}
			if !strings.HasPrefix(sqliteFile, "SQLite format 3") || !strings.Contains(sqliteFile, "sqlite-bytes-kept") {
				t.Fatalf("sqlite bytes not exported (%d bytes)", len(sqliteFile))
			}
			kv := FileUnder(t, files, "shop/storage/kv.json")
			for _, want := range []string{`"facts"`, `"é"`, `"中"`} {
				if !strings.Contains(kv, want) {
					t.Fatalf("kv.json lacks %s: %s", want, kv)
				}
			}
			if !json.Valid([]byte(kv)) {
				t.Fatalf("kv.json is not JSON: %s", kv)
			}
		})
	}
}

// What a visitor's tools show per table is that caller's own rights; a table
// whose rules grant nothing is not listed.
func TestRDRulesVisitorRightsList(t *testing.T) {
	s := NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.role == \"owner\""},{"command":"delete","using":"auth.role == \"owner\""}]},"tables":{"closed":{"rules":{"policies":[]}}}}`, s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE closed(secret TEXT)"}, s.Key, 200)
	for _, who := range []string{"anon", "alice", "owner-site"} {
		r := httptest.NewRequest("GET", "https://"+s.Host+"/", nil)
		r.SetPathValue("sitename", "shop")
		for k, v := range s.Callers[who] {
			r.Header.Set(k, v)
		}
		out, err := handler.VisitorTools{Access: s.A.Access, Data: s.A.Data}.Read(r, s.Sid, s.OwnerID, "shop", map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", who, err)
		}
		list := out["resources"].([]map[string]any)
		if len(list) != 1 || list[0]["table"] != "entries" {
			t.Fatalf("%s: %v", who, list)
		}
		want := map[string]any{"read": true, "add": who != "anon", "edit_own": who == "owner-site", "delete_own": who == "owner-site", "read_own_only": false}
		if !reflect.DeepEqual(list[0]["rights"], want) {
			t.Fatalf("%s: got %v want %v", who, list[0]["rights"], want)
		}
	}
}

// Only the site's owner resolves visitor ids to addresses, and only ids that
// signed in or wrote on that site: not an id the owner planted in a table,
// not someone who signed in only elsewhere, not another account's key.
func TestRDStorageVisitorEmailLookup(t *testing.T) {
	s := NewStorageTestSite(t)
	a := s.A
	stranger := a.NewPerson(t, "rd-stranger")
	strangerID, _ := a.UserID(t, stranger)
	s.Must(t, "PUT", "/resources/notes", StorageRules("kv", RulesSignedInAdd), s.Key, 201)
	s.Must(t, "PUT", "/kv/notes/keys/hello", map[string]any{"value": "hi"}, s.Callers["alice"], 200)
	lookup := func(headers map[string]string, ids string) Resp {
		return s.Do(t, "GET", "/visitors?id="+ids, nil, headers)
	}

	var alice string
	if err := a.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, s.AliceID).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	r := lookup(s.Key, s.AliceID+"&id="+strangerID)
	if r.Status != 200 || !strings.Contains(string(r.Body), alice) || strings.Contains(string(r.Body), stranger.Email) {
		t.Fatalf("owner lookup: %d %s", r.Status, r.Body)
	}
	// An id the owner wrote into their own table, or of someone who signed in
	// only on another site, never resolves.
	s.Must(t, "PUT", "/resources/box", map[string]any{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/box/schema", map[string]any{"sql": "CREATE TABLE t (visitor_id TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/box/execute", map[string]any{"sql": "INSERT INTO t(visitor_id) VALUES(?)", "params": []any{strangerID}}, s.Key, 200)
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/other/files", map[string]any{"files": map[string]string{"index.html": "o"}}, map[string]string{"X-API-Key": stranger.Key}); r.Status != http.StatusCreated {
		t.Fatalf("stranger site: %d", r.Status)
	}
	a.Session(t, stranger, a.SiteIDAny(t, strangerID, "other"), "other."+PcSiteDomain)
	if r := lookup(s.Key, strangerID); r.Status != 200 || strings.Contains(string(r.Body), stranger.Email) || !strings.Contains(string(r.Body), `"found":false`) {
		t.Fatalf("planted or other-site id resolved: %d %s", r.Status, r.Body)
	}
	// Never a visitor, never another account, even one with a site of the same name.
	if r := lookup(s.Callers["alice"], s.AliceID); r.Status != 403 || strings.Contains(string(r.Body), alice) {
		t.Fatalf("visitor lookup: %d %s", r.Status, r.Body)
	}
	if r := lookup(map[string]string{"X-API-Key": stranger.Key}, s.AliceID); strings.Contains(string(r.Body), alice) {
		t.Fatalf("foreign account lookup: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "mine"}}, map[string]string{"X-API-Key": stranger.Key}); r.Status/100 != 2 {
		t.Fatalf("stranger's own shop: %d %s", r.Status, r.Body)
	}
	if r := lookup(map[string]string{"X-API-Key": stranger.Key}, s.AliceID); strings.Contains(string(r.Body), alice) || strings.Contains(string(r.Body), `"found":true`) {
		t.Fatalf("other owner's same-named site resolved a visitor: %d %s", r.Status, r.Body)
	}
	if r := lookup(s.Key, "not-an-id"); r.Status != 400 {
		t.Fatalf("bad id: %d", r.Status)
	}
}
