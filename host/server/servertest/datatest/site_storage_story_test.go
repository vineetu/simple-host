package datatest

import (
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/edition"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// One shop through the storage routes: own records on each kind, the
// refusals a page most likely meets, and what the owner sees.
func TestStorageStoryShopAndRefusals(t *testing.T) {
	s := NewShop(t)
	ah, bh, anon := s.Callers["alice"], s.Callers["bob"], s.Callers["anon"]
	check := func(method, path string, body any, headers map[string]string, status int, code string) rt.Resp {
		t.Helper()
		r := s.Must(t, method, path, body, headers, status)
		if code != "" && r.Code() != code {
			t.Fatalf("%s: want %s got %s", path, code, r.Body)
		}
		return r
	}
	for _, kind := range []string{"sqlite", "kv", "files"} {
		check("PUT", "/resources/"+kind, StorageRules(kind, RulesRecords), s.Key, 201, "")
		check("GET", map[string]string{"sqlite": "/sqlite/sqlite/tables/orders/rows", "kv": "/kv/kv/keys", "files": "/files/files/objects"}[kind], nil, anon, 401, "sign_in_required")
	}
	// The removed presets' fields are refused.
	check("PUT", "/resources/personal", map[string]string{"kind": "kv", "read": "own", "write": "signed-in"}, s.Key, 400, "invalid_resource")
	check("POST", "/sqlite/sqlite/schema", `{"sql":"CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT, status TEXT DEFAULT 'placed')"}`, s.Key, 200, "")
	// The server's identity column cannot be dropped.
	check("POST", "/sqlite/sqlite/schema", `{"sql":"ALTER TABLE orders DROP COLUMN visitor_id"}`, s.Key, 400, "")

	rows := "/sqlite/sqlite/tables/orders/rows"
	for _, body := range []string{`{"unknown":1}`, `{"item\") VALUES ('bad'); DELETE FROM orders; --":"bad"}`, `{"item":{"sql":"DELETE FROM orders"}}`, `null`} {
		check("POST", rows, body, ah, 400, "")
	}
	check("POST", "/sqlite/sqlite/tables/"+url.PathEscape(`orders";DROP TABLE orders;--`)+"/rows", `{"item":"x"}`, ah, 400, "invalid_rows")
	check("POST", "/sqlite/sqlite/execute", `{"sql":"DELETE FROM orders"}`, ah, 403, "fixed_routes_required")
	check("GET", rows+"?order="+url.QueryEscape("id; DELETE FROM orders"), nil, ah, 400, "invalid_rows")
	check("POST", rows, `{"item":"alice-only"}`, ah, 200, "")
	check("POST", rows, `{"item":"bob-only"}`, bh, 200, "")
	check("POST", rows, `{"item":"alice-second"}`, ah, 200, "")
	// Each visitor pages through only their own rows.
	first := check("GET", rows+"?order=id&desc=1&limit=1", nil, ah, 200, "").JSON(t)
	page := check("GET", rows+"?order=id&desc=1&limit=1&after="+url.QueryEscape(first["next_after"].(string)), nil, ah, 200, "").JSON(t)
	if len(page["rows"].([]any)) != 1 || page["next_after"] != "" || !strings.Contains(jsonString(page), "alice-only") || !strings.Contains(jsonString(first), s.AliceID) {
		t.Fatalf("alice pages: %v / %v", first, page)
	}
	if b := string(check("GET", rows, nil, bh, 200, "").Body); strings.Contains(b, "alice") {
		t.Fatalf("bob rows: %s", b)
	}
	// The owner sees and changes every row.
	check("POST", "/sqlite/sqlite/execute", `{"sql":"UPDATE orders SET status='packed' WHERE visitor_id=?","params":["`+s.AliceID+`"]}`, s.Key, 200, "")
	if b := string(check("GET", rows, nil, ah, 200, "").Body); !strings.Contains(b, "packed") {
		t.Fatalf("owner status not visible: %s", b)
	}
	// Owner triggers cannot turn an add-only insert into an edit.
	check("POST", "/sqlite/sqlite/schema", map[string]string{"sql": "CREATE TRIGGER bad AFTER INSERT ON orders BEGIN DELETE FROM orders; END"}, s.Key, 200, "")
	check("POST", rows, `{"item":"must-not-insert"}`, ah, 403, "forbidden")

	for _, kind := range []string{"kv", "files"} {
		path := map[string]string{"kv": "/kv/kv/keys/", "files": "/files/files/objects/"}[kind]
		body := any("alice-bytes")
		if kind == "kv" {
			body = `{"value":"alice-value"}`
		}
		conflict := map[string]string{"kv": "key_exists", "files": "file_exists"}[kind]
		check("PUT", path+"alice", body, ah, 200, "")
		check("PUT", path+"alice", body, bh, 409, conflict)
		check("DELETE", path+"alice", nil, ah, 403, "add_only")
		check("GET", path+"alice", nil, bh, 404, "")
		if b := string(check("GET", strings.TrimSuffix(path, "/"), nil, bh, 200, "").Body); strings.Contains(b, "alice") {
			t.Fatalf("own list leaked %s", b)
		}
		// Owner overwrites keep the visitor's identity; owner deletes free the name.
		check("PUT", path+"alice", body, s.Key, 200, "")
		check("GET", path+"alice", nil, ah, 200, "")
		check("DELETE", path+"alice", nil, s.Key, 200, "")
		check("PUT", path+"alice", body, bh, 200, "")
		check("GET", path+"alice", nil, ah, 404, "")
	}

	// Switching an existing table to own adds the identity index, and
	// unattributed rows stay the owner's.
	check("PUT", "/resources/converted", map[string]string{"kind": "sqlite"}, s.Key, 201, "")
	check("POST", "/sqlite/converted/schema", `{"sql":"CREATE TABLE entries (id INTEGER PRIMARY KEY,v TEXT,visitor_id TEXT)"}`, s.Key, 200, "")
	check("POST", "/sqlite/converted/execute", `{"sql":"INSERT INTO entries(v) VALUES('owner-only')"}`, s.Key, 200, "")
	check("PUT", "/resources/converted", StorageRules("sqlite", RulesRecords), s.Key, 200, "")
	if r := s.Must(t, "POST", "/sqlite/converted/query", `{"sql":"SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND tbl_name='entries'"}`, s.Key, 200); !strings.Contains(string(r.Body), "[[1]]") {
		t.Fatalf("missing identity index: %s", r.Body)
	}
	check("POST", "/sqlite/converted/schema", `{"sql":"CREATE TABLE without_id (v INTEGER,visitor_id INTEGER)"}`, s.Key, 400, "visitor_id_required")
	// Equal and NULL ordering values page with a rowid tie-breaker.
	for _, value := range []string{`null`, `"same"`, `"same"`} {
		check("POST", "/sqlite/converted/tables/entries/rows", `{"v":`+value+`}`, ah, 200, "")
	}
	for _, desc := range []string{"0", "1"} {
		count, after := 0, ""
		for i := 0; i < 6; i++ {
			got := check("GET", "/sqlite/converted/tables/entries/rows?order=v&desc="+desc+"&limit=1&after="+url.QueryEscape(after), nil, ah, 200, "").JSON(t)
			count += len(got["rows"].([]any))
			if after = got["next_after"].(string); after == "" {
				break
			}
		}
		if count != 3 {
			t.Fatalf("pagination desc=%s got %d", desc, count)
		}
	}
}

// Files have their own allowance beside the shared KV/SQLite one, and a
// product with a combined pool counts all three together.
func TestStorageStorySeparateAllowances(t *testing.T) {
	t.Setenv("SITE_STORAGE_MAX_BYTES", "16384")
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "10")
	t.Setenv("SITE_STORAGE_FILE_MAX_BYTES", "100")
	s := NewShop(t)
	call := func(method, path string, body any, status int) map[string]any {
		t.Helper()
		r := s.Must(t, method, path, body, s.Key, status)
		if method == "GET" && status == 200 {
			return r.JSON(t)
		}
		return nil
	}
	for _, kind := range []string{"kv", "sqlite", "files"} {
		call("PUT", "/resources/"+kind, map[string]string{"kind": kind}, 201)
	}
	call("PUT", "/files/files/objects/full", "1234567890", 200)
	call("POST", "/sqlite/sqlite/schema", `{"sql":"CREATE TABLE t(v TEXT)"}`, 200)
	call("PUT", "/kv/kv/keys/data", `{"value":"yes"}`, 200)
	call("PUT", "/files/files/objects/overflow", "x", 507)
	call("POST", "/sqlite/sqlite/execute", `{"sql":"INSERT INTO t VALUES(randomblob(20000))"}`, 507)
	if u := call("GET", "/usage", nil, 200); u["files_used_bytes"] != float64(10) || u["files_remaining_bytes"] != float64(0) || u["used_bytes"].(float64) >= 16384 {
		t.Fatalf("usage %v", u)
	}
	call("DELETE", "/files/files/objects/full", nil, 200)
	call("PUT", "/files/files/objects/new", "ok", 200)
	call("PUT", "/files/files/objects/too-big", strings.Repeat("x", 101), 400)
	call("PUT", "/kv/kv/keys/full", map[string]any{"value": strings.Repeat("x", 16384)}, 507)
	call("GET", "/kv/kv/keys/full", nil, 404)
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "")
	if s.A.Data.StorageSvc().FilesLimitBytes() != 10000000 {
		t.Fatal("file default")
	}
	s.A.Data.Ed = edition.FromPolicies(edition.RoutePolicy{}, edition.PresentationPolicy{}, edition.IdentityPolicy{}, edition.StoragePolicy{CombinedPool: true}, edition.NotificationPolicy{}, config.DefaultLimits())
	if s.A.Data.StorageSvc().FilesLimitBytes() != 16384 || s.A.Data.StorageSvc().BudgetUsed(data.Usage{KV: 1, SQLite: 2, Files: 3}) != 6 {
		t.Fatal("combined pool allowance changed")
	}
}
