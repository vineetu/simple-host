package datatest

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestSiteStoragePoliciesAndIsolation(t *testing.T) {
	s := NewShop(t)
	anon, alice := s.Callers["anon"], s.Callers["alice"]
	put := func(name, kind, rules string) {
		t.Helper()
		body := `{"kind":"` + kind + `"}`
		if rules != "" {
			body = StorageRules(kind, rules)
		}
		s.Must(t, "PUT", "/resources/"+name, body, s.Key, 201)
	}
	put("board", "kv", RulesOpenAdd)
	put("private", "kv", "")
	put("members", "kv", RulesBoard)
	put("writeonly", "sqlite", RulesInbox)
	put("assets", "files", RulesOpenAdd)

	// Another owner's site of the same name is another site.
	bob := s.A.NewPerson(t, "other-owner")
	s.A.Deploy(t, bob, "shop")
	if r := s.A.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/storage/resources/bob-only", StorageRules("kv", RulesOpenAdd), bob.Header()); r.Status != 201 {
		t.Fatalf("bob resource: %d %s", r.Status, r.Body)
	}
	s.Must(t, "GET", "/kv/bob-only/keys/k", nil, anon, 404)
	if r := s.A.At(t, "GET", rt.SiteDomain, "/v1/sites/shop/storage/resources", nil, bob.Header()); strings.Contains(string(r.Body), `"name":"board"`) {
		t.Fatalf("another owner saw resources: %s", r.Body)
	}

	s.Must(t, "PUT", "/kv/board/keys/hello", `{"value":{"answer":42}}`, anon, 200)
	if r := s.Must(t, "GET", "/kv/board/keys/hello", nil, nil, 200); !strings.Contains(string(r.Body), `"answer":42`) {
		t.Fatalf("open read: %s", r.Body)
	}
	s.Must(t, "GET", "/kv/private/keys/hello", nil, nil, 403)
	if r := s.Must(t, "PUT", "/kv/members/keys/hello", `{"value":1}`, anon, 401); r.Code() != "sign_in_required" {
		t.Fatalf("signed-in refusal: %s", r.Body)
	}
	s.Must(t, "PUT", "/kv/members/keys/hello", `{"value":1}`, alice, 200)

	// A read-only query never creates the database file.
	db := filepath.Join(s.A.Disk.SiteDir(s.OwnerID, "shop"), "runtime", "sqlite", "writeonly.sqlite")
	s.Must(t, "POST", "/sqlite/writeonly/query", `{"sql":"SELECT 1"}`, s.Key, 200)
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("read-only query created DB: %v", err)
	}
	// An inbox table takes visitors' rows but never shows them back.
	s.Must(t, "POST", "/sqlite/writeonly/schema", `{"sql":"CREATE TABLE items (n INTEGER)"}`, s.Key, 200)
	if r := s.Do(t, "POST", "/sqlite/writeonly/schema", `{"sql":"CREATE TABLE x (n INTEGER)"}`, anon); r.Status == 200 {
		t.Fatal("visitor schema accepted")
	}
	s.Must(t, "POST", "/sqlite/writeonly/tables/items/rows", `{"n":1}`, anon, 200)
	s.Must(t, "GET", "/sqlite/writeonly/tables/items/rows", nil, anon, 403)

	// Files: bytes back as a download; links only from the owner, dead once
	// the resource goes; no path traversal.
	s.Must(t, "PUT", "/files/assets/objects/photo.txt", "hello", rt.Browser(s.Host, "", "Content-Type", "text/plain"), 200)
	if r := s.Must(t, "GET", "/files/assets/objects/photo.txt", nil, nil, 200); string(r.Body) != "hello" || r.Header.Get("Content-Disposition") == "" {
		t.Fatalf("file read: %s %v", r.Body, r.Header)
	}
	if r := s.Do(t, "GET", "/files/assets/objects/../secret", nil, nil); r.Status == 200 {
		t.Fatal("traversal accepted")
	}
	if r := s.Do(t, "POST", "/files/assets/download-link", `{"path":"photo.txt"}`, anon); r.Status == 200 {
		t.Fatal("anonymous file link minted")
	}
	u, err := url.Parse(s.Must(t, "POST", "/files/assets/download-link", `{"path":"photo.txt"}`, s.Key, 200).JSON(t)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if r := s.A.At(t, "GET", rt.SiteDomain, u.RequestURI(), nil, nil); r.Status != 200 || string(r.Body) != "hello" {
		t.Fatalf("linked bytes %d %s", r.Status, r.Body)
	}
	s.Must(t, "DELETE", "/resources/assets", nil, s.Key, 200)
	if r := s.A.At(t, "GET", rt.SiteDomain, u.RequestURI(), nil, nil); r.Status != 404 {
		t.Fatalf("deleted resource link alive %d", r.Status)
	}
}

// A resource follows the site's passcode unless set to off; the owner is
// never locked out.
func TestSiteStorageInheritsPasscodeAndExplicitOff(t *testing.T) {
	s := NewShop(t)
	for name, mode := range map[string]string{"locked": "inherit", "open": "off"} {
		s.Must(t, "PUT", "/resources/"+name, `{"kind":"kv","site_passcode":"`+mode+`","rules":`+RulesOpenAdd+`}`, s.Key, 201)
	}
	rt.SetPasscode(t, s.A.Database, s.Sid)
	anon := s.Callers["anon"]
	s.Must(t, "GET", "/kv/locked/keys/k", nil, anon, 403)
	if r := s.Must(t, "PUT", "/kv/locked/keys/k", `{"value":1}`, anon, 403); r.Code() != "site_locked" {
		t.Fatalf("locked: %s", r.Body)
	}
	s.Must(t, "PUT", "/kv/open/keys/k", `{"value":1}`, anon, 200)
	s.Must(t, "PUT", "/kv/locked/keys/k", `{"value":2}`, s.Key, 200)
	s.Must(t, "PUT", "/kv/locked/keys/k2", `{"value":3}`, rtCopy(anon, Unlocked, "1"), 200)
}

// Usage is the owner's; a file exactly at the cap fits, one byte more does
// not, and a refused write changes nothing.
func TestSiteStorageUsageAndExactFileBoundary(t *testing.T) {
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "1000000")
	t.Setenv("SITE_STORAGE_MAX_BYTES", "1000000")
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/photos", map[string]string{"kind": "files"}, s.Key, 201)
	usage := func() (float64, float64) {
		t.Helper()
		j := s.Must(t, "GET", "/usage", nil, s.Key, 200).JSON(t)
		return j["remaining_bytes"].(float64), j["breakdown"].(map[string]any)["files_bytes"].(float64)
	}
	other := s.A.NewPerson(t, "other")
	s.Must(t, "GET", "/usage", nil, other.Header(), 403)
	s.Must(t, "GET", "/usage", nil, s.Callers["alice"], 403)
	s.Must(t, "PUT", "/files/photos/objects/a.bin", strings.Repeat("a", 1000000), s.Key, 200)
	if left, files := usage(); left != 1000000 || files != 1000000 {
		t.Fatalf("full usage %v %v", left, files)
	}
	s.Must(t, "PUT", "/files/photos/objects/a.bin", strings.Repeat("b", 1000000), s.Key, 200)
	if r := s.Must(t, "PUT", "/files/photos/objects/b.bin", "x", s.Key, 507); r.Code() != "site_full" {
		t.Fatalf("growth beyond cap: %s", r.Body)
	}
	if r := s.Must(t, "GET", "/files/photos/objects/a.bin", nil, s.Key, 200); len(r.Body) != 1000000 || r.Body[0] != 'b' {
		t.Fatal("file changed on rejection")
	}
	s.Must(t, "DELETE", "/files/photos/objects/a.bin", nil, s.Key, 200)
	if _, files := usage(); files != 0 {
		t.Fatalf("after delete %v", files)
	}
}

func TestSiteStorageKVNormalizedQuotaAndSQLiteFull(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/kv", map[string]string{"kind": "kv"}, s.Key, 201)
	t.Setenv("SITE_STORAGE_MAX_BYTES", "3")
	s.Must(t, "PUT", "/kv/kv/keys/n", `{"value":1e3}`, s.Key, 507) // counted as stored, "1000"
	s.Must(t, "GET", "/kv/kv/keys/n", nil, s.Key, 404)
	t.Setenv("SITE_STORAGE_MAX_BYTES", "32768")
	s.Must(t, "PUT", "/resources/sql", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/sql/schema", `{"sql":"CREATE TABLE items (v BLOB)"}`, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/sql/execute", `{"sql":"INSERT INTO items(v) VALUES(randomblob(50000))"}`, s.Key, 507); r.Code() != "sqlite_full" {
		t.Fatalf("SQLite full: %s", r.Body)
	}
	if r := s.Must(t, "POST", "/sqlite/sql/query", `{"sql":"SELECT COUNT(*) FROM items"}`, s.Key, 200); !strings.Contains(string(r.Body), `[[0]]`) {
		t.Fatalf("SQL changed on rejection: %s", r.Body)
	}
	if j := s.Must(t, "GET", "/usage", nil, s.Key, 200).JSON(t); j["used_bytes"].(float64) > 32768 {
		t.Fatalf("SQL usage exceeded cap: %v", j)
	}
}

// A missing database answers as a bad row request to whoever may read,
// and the read refusal comes first.
func TestMissingSQLiteReadResponse(t *testing.T) {
	s := NewShop(t)
	for preset, rules := range map[string]string{"wall": RulesWall, "private": string(model.OwnerOnlyRules)} {
		s.Do(t, "PUT", "/resources/"+preset, StorageRules("sqlite", rules), s.Key)
		for _, path := range []string{"/tables/posts/rows/1", "/tables/posts/rows"} {
			for _, h := range []map[string]string{s.Key, s.Callers["anon"]} {
				status, code := 400, "invalid_rows"
				if preset == "private" && h["X-API-Key"] == "" {
					status, code = 403, "forbidden"
				}
				if r := s.Must(t, "GET", "/sqlite/"+preset+path, nil, h, status); r.Code() != code {
					t.Fatalf("%s %s: %s", preset, path, r.Body)
				}
			}
		}
	}
}

func TestStorageKVRules(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/data", `{"kind":"kv","rules":{"policies":[{"command":"read","using":"key contains \"public\""},{"command":"add","check":"value == \"1\""},{"command":"edit","using":"auth.signed_in","check":"value == \"2\""},{"command":"delete","using":"auth.signed_in"}]}}`, s.Key, 201)
	s.Must(t, "PUT", "/kv/data/keys/public", `{"value":1}`, s.Callers["alice"], 200)
	for _, tc := range []struct {
		method, path, body, who, code string
		status                        int
	}{
		{"GET", "/kv/data/keys/public", "", "anon", "", 200},
		{"PUT", "/kv/data/keys/new", `{"value":9}`, "alice", "forbidden", 403},
		{"PUT", "/kv/data/keys/public", `{"value":9}`, "alice", "key_exists", 409},
		{"DELETE", "/kv/data/keys/public", "", "anon", "sign_in_required", 401},
		{"GET", "/kv/data/keys/missing", "", "anon", "key_not_found", 404},
	} {
		var body any
		if tc.body != "" {
			body = tc.body
		}
		if r := s.Must(t, tc.method, tc.path, body, s.Callers[tc.who], tc.status); r.Code() != tc.code {
			t.Fatalf("%s %s: %s", tc.method, tc.path, r.Body)
		}
	}
	s.Must(t, "PUT", "/kv/data/keys/public", `{"value":2}`, s.Callers["alice"], 200)
	s.Must(t, "DELETE", "/kv/data/keys/public", nil, s.Callers["alice"], 200)
}

// A download link works as minted; a tampered or expired token is refused.
func TestStorageDownloadLinkTokens(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/assets", map[string]string{"kind": "files"}, s.Key, 201)
	s.Must(t, "PUT", "/files/assets/objects/a.txt", "hello", s.Key, 200)
	u, _ := url.Parse(s.Must(t, "POST", "/files/assets/download-link", `{"path":"a.txt"}`, s.Key, 200).JSON(t)["url"].(string))
	get := func(token string) rt.Resp {
		return s.A.At(t, "GET", rt.SiteDomain, "/v1/storage-download?token="+url.QueryEscape(token), nil, nil)
	}
	token := u.Query().Get("token")
	if r := get(token); r.Status != 200 || string(r.Body) != "hello" {
		t.Fatalf("link: %d %s", r.Status, r.Body)
	}
	// Change a middle character (the last one may only carry padding bits).
	mid := len(token) / 2
	swap := "A"
	if token[mid] == 'A' {
		swap = "B"
	}
	expired := s.A.Data.StorageFileToken(kvCall(t, s, "assets"), "a.txt", time.Now().Add(-time.Minute))
	for _, bad := range []string{token[:mid] + swap + token[mid+1:], expired} {
		if r := get(bad); r.Status != 404 || r.Code() != "file_link_invalid" {
			t.Fatalf("bad token: %d %s", r.Status, r.Body)
		}
	}
}
