package datatest

import (
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/url"
	"strings"
	"testing"
)

func TestStorageStoryShopAndRefusals(t *testing.T) {
	a, cert := servertest.NewSiteApp(t, "canonical")
	owner := a.NewPerson(t, "story-owner")
	alice := a.NewPerson(t, "story-alice")
	bob := a.NewPerson(t, "story-bob")
	a.Deploy(t, owner, "shop")
	_, handle := a.UserID(t, owner)
	servertest.MarkReady(t, cert, handle)
	host := "shop." + handle + "." + servertest.PcSiteDomain
	base := "/v1/sites/shop/storage"
	key := map[string]string{"X-API-Key": owner.Key}
	sid := a.SiteID(t, owner, "shop")
	ac := a.Session(t, alice, sid, host)
	bc := a.Session(t, bob, sid, host)
	ah, bh := servertest.Browser(host, ac), servertest.Browser(host, bc)
	check := func(method, h, path string, body any, headers map[string]string, status int, code string) servertest.Resp {
		t.Helper()
		r := a.At(t, method, h, base+path, body, headers)
		if r.Status != status {
			t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.Status, r.Body)
		}
		if code != "" && r.JSON(t)["code"] != code {
			t.Fatalf("%s: want %s got %s", path, code, r.Body)
		}
		return r
	}
	for _, kind := range []string{"sqlite", "kv", "files"} {
		check("PUT", servertest.PcSiteDomain, "/resources/"+kind, servertest.StorageRules(kind, servertest.RulesRecords), key, 201, "")
		check("GET", host, map[string]string{"sqlite": "/sqlite/sqlite/tables/orders/rows", "kv": "/kv/kv/keys", "files": "/files/files/objects"}[kind], nil, nil, 401, "sign_in_required")
		// The removed presets' fields are refused on every kind.
		check("PUT", servertest.PcSiteDomain, "/resources/personal-"+kind, map[string]string{"kind": kind, "read": "own", "write": "signed-in"}, key, 400, "invalid_resource")
	}
	check("PUT", servertest.PcSiteDomain, "/resources/invalid-mode", map[string]string{"kind": "kv", "write_mode": "upsert"}, key, 400, "invalid_resource")
	check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/schema", `{"sql":"CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT, status TEXT DEFAULT 'placed')"}`, key, 200, "")
	// Automatic identity column and index, and schema rollback on removing it.
	check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/query", `{"sql":"SELECT name FROM sqlite_schema WHERE type='index' AND tbl_name='orders'"}`, key, 200, "")
	check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/schema", `{"sql":"ALTER TABLE orders DROP COLUMN visitor_id"}`, key, 400, "")
	rows := "/sqlite/sqlite/tables/orders/rows"
	for _, body := range []string{`{"unknown":1}`, `{"item\") VALUES ('bad'); DELETE FROM orders; --":"bad"}`, `{"item":{"sql":"DELETE FROM orders"}}`, `{"item":"one"} {"item":"two"}`, `null`, `[]`} {
		check("POST", host, rows, body, ah, 400, "")
	}
	check("POST", host, "/sqlite/sqlite/tables/"+url.PathEscape(`orders";DROP TABLE orders;--`)+"/rows", `{"item":"x"}`, ah, 400, "invalid_rows")
	check("POST", host, rows, `{"item":"alice-only"}`, ah, 200, "")
	check("POST", host, rows, `{"item":"bob-only"}`, bh, 200, "")
	for _, h := range []map[string]string{ah, bh, servertest.Browser(host, "")} {
		for _, route := range []string{"query", "execute"} {
			check("POST", host, "/sqlite/sqlite/"+route, `{"sql":"DELETE FROM orders"}`, h, 403, "fixed_routes_required")
		}
	}
	for _, q := range []string{"?order=" + url.QueryEscape("id; DELETE FROM orders"), "?order=unknown", "?desc=sql", "?limit=0", "?after=forged"} {
		check("GET", host, rows+q, nil, ah, 400, "invalid_rows")
	}
	check("POST", host, rows, `{"item":"alice-second"}`, ah, 200, "")
	ar := check("GET", host, rows+"?order=id&desc=1&limit=1", nil, ah, 200, "")
	if strings.Contains(string(ar.Body), "bob-only") || !strings.Contains(string(ar.Body), "alice-second") {
		t.Fatalf("alice rows: %s", ar.Body)
	}
	aid, _ := a.UserID(t, alice)
	if !strings.Contains(string(ar.Body), aid) {
		t.Fatalf("server identity absent: %s", ar.Body)
	}
	next := ar.JSON(t)["next_after"].(string)
	page := check("GET", host, rows+"?order=id&desc=1&limit=1&after="+url.QueryEscape(next), nil, ah, 200, "")
	if len(page.JSON(t)["rows"].([]any)) != 1 || !strings.Contains(string(page.Body), "alice-only") || page.JSON(t)["next_after"] != "" {
		t.Fatalf("cursor: %s", page.Body)
	}
	br := check("GET", host, rows, nil, bh, 200, "")
	if strings.Contains(string(br.Body), "alice-only") || !strings.Contains(string(br.Body), "bob-only") {
		t.Fatalf("bob rows: %s", br.Body)
	}
	// A cursor from another visitor remains scoped to the reader.
	check("GET", host, rows+"?order=id&desc=1&after="+url.QueryEscape(next), nil, bh, 200, "")
	all := check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/query", `{"sql":"SELECT item,status FROM orders ORDER BY id"}`, key, 200, "")
	if len(all.JSON(t)["rows"].([]any)) != 3 {
		t.Fatalf("owner rows: %s", all.Body)
	}
	check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/execute", `{"sql":"UPDATE orders SET status=? WHERE visitor_id=?","params":["packed","`+aid+`"]}`, key, 200, "")
	ar = check("GET", host, rows, nil, ah, 200, "")
	if !strings.Contains(string(ar.Body), "packed") {
		t.Fatalf("owner status not visible: %s", ar.Body)
	}
	for _, kind := range []string{"kv", "files"} {
		path := map[string]string{"kv": "/kv/kv/keys/", "files": "/files/files/objects/"}[kind]
		body := any("alice-bytes")
		if kind == "kv" {
			body = `{"value":"alice-value","writer_id":"forged"}`
		}
		check("PUT", host, path+"alice", body, ah, 200, "")
		check("PUT", host, path+"bob", body, bh, 200, "")
		conflict := map[string]string{"kv": "key_exists", "files": "file_exists"}[kind]
		check("PUT", host, path+"alice", body, ah, 409, conflict)
		check("PUT", host, path+"alice", body, bh, 409, conflict)
		check("DELETE", host, path+"alice", nil, ah, 403, "add_only")
		check("DELETE", host, path+"bob", nil, ah, 403, "add_only")
		check("GET", host, path+"alice", nil, bh, 404, "")
		check("GET", host, path+"bob", nil, ah, 404, "")
		check("GET", host, path+"alice", nil, ah, 200, "")
		check("GET", servertest.PcSiteDomain, path+"bob", nil, key, 200, "")
		list := strings.TrimSuffix(path, "/")
		res := check("GET", host, list, nil, ah, 200, "")
		if strings.Contains(string(res.Body), `"bob"`) {
			t.Fatalf("own list leaked %s", res.Body)
		}
		// Owner overwrites preserve the original visitor identity.
		check("PUT", servertest.PcSiteDomain, path+"alice", body, key, 200, "")
		check("GET", host, path+"alice", nil, ah, 200, "")
		check("DELETE", servertest.PcSiteDomain, path+"alice", nil, key, 200, "")
		check("PUT", host, path+"alice", body, bh, 200, "")
		check("GET", host, path+"alice", nil, ah, 404, "")
	}
	aliceID, _ := a.UserID(t, alice)
	if _, err := a.Database.Exec(`INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) VALUES($1,'files','interrupted',$2)`, sid, aliceID); err != nil {
		t.Fatal(err)
	}
	check("PUT", host, "/files/files/objects/interrupted", "bob-private", bh, 200, "")
	check("GET", host, "/files/files/objects/interrupted", nil, ah, 404, "file_not_found")
	check("GET", host, "/files/files/objects/interrupted", nil, bh, 200, "")

	// Owner-defined triggers cannot turn add-only visitor inserts into edits.
	for _, statement := range []string{"UPDATE orders SET status='changed'", "DELETE FROM orders", "INSERT INTO orders(item) VALUES('triggered')"} {
		check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/schema", map[string]string{"sql": "CREATE TRIGGER bad AFTER INSERT ON orders BEGIN " + statement + "; END"}, key, 200, "")
		check("POST", host, rows, `{"item":"must-not-insert"}`, ah, 403, "forbidden")
		check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/schema", `{"sql":"DROP TRIGGER bad"}`, key, 200, "")
	}
	unchanged := check("POST", servertest.PcSiteDomain, "/sqlite/sqlite/query", `{"sql":"SELECT COUNT(*) FROM orders"}`, key, 200, "")
	if !strings.Contains(string(unchanged.Body), "[[3]]") {
		t.Fatal("denied trigger changed rows")
	}

	// Switching an existing indexed-capable table to own adds the identity index.
	check("PUT", servertest.PcSiteDomain, "/resources/converted", map[string]string{"kind": "sqlite"}, key, 201, "")
	check("POST", servertest.PcSiteDomain, "/sqlite/converted/schema", `{"sql":"CREATE TABLE entries (id INTEGER PRIMARY KEY,v TEXT,visitor_id TEXT)"}`, key, 200, "")
	check("PUT", servertest.PcSiteDomain, "/resources/converted", servertest.StorageRules("sqlite", servertest.RulesRecords), key, 200, "")
	idx := check("POST", servertest.PcSiteDomain, "/sqlite/converted/query", `{"sql":"SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND tbl_name='entries'"}`, key, 200, "")
	if !strings.Contains(string(idx.Body), "[[1]]") {
		t.Fatalf("missing identity index: %s", idx.Body)
	}
	check("POST", host, "/sqlite/converted/tables/entries/rows", `{"v":"anonymous"}`, servertest.Browser(host, ""), 401, "sign_in_required")
	check("POST", host, "/sqlite/converted/tables/entries/rows", `{"v":"owned"}`, ah, 200, "")
	// Rules stamp the server's identity whatever the spelling sent.
	check("POST", host, "/sqlite/converted/tables/entries/rows", `{"VISITOR_ID":"forged"}`, ah, 200, "")
	if forged := check("POST", servertest.PcSiteDomain, "/sqlite/converted/query", `{"sql":"SELECT COUNT(*) FROM entries WHERE visitor_id='forged'"}`, key, 200, ""); !strings.Contains(string(forged.Body), "[[0]]") {
		t.Fatalf("forged visitor_id stored: %s", forged.Body)
	}
	check("POST", servertest.PcSiteDomain, "/sqlite/converted/schema", `{"sql":"CREATE TABLE without_id (v INTEGER,visitor_id INTEGER)"}`, key, 400, "visitor_id_required")
	missing := check("POST", servertest.PcSiteDomain, "/sqlite/converted/query", `{"sql":"SELECT COUNT(*) FROM sqlite_schema WHERE name='without_id'"}`, key, 200, "")
	if !strings.Contains(string(missing.Body), "[[0]]") {
		t.Fatal("rejected schema was committed")
	}
	// Equal and NULL ordering values use a rowid tie-breaker for both directions.
	for _, value := range []string{`null`, `"same"`, `"same"`} {
		check("POST", host, "/sqlite/converted/tables/entries/rows", `{"v":`+value+`}`, ah, 200, "")
	}
	for _, desc := range []string{"0", "1"} {
		count := 0
		after := ""
		for i := 0; i < 6; i++ {
			got := check("GET", host, "/sqlite/converted/tables/entries/rows?order=v&desc="+desc+"&limit=1&after="+url.QueryEscape(after), nil, ah, 200, "").JSON(t)
			count += len(got["rows"].([]any))
			after = got["next_after"].(string)
			if after == "" {
				break
			}
		}
		if count != 5 {
			t.Fatalf("pagination desc=%s got %d", desc, count)
		}
	}
	// Unattributed legacy data is never assigned to whoever first reads it.
	check("POST", servertest.PcSiteDomain, "/sqlite/converted/execute", `{"sql":"INSERT INTO entries(v) VALUES('owner-only')"}`, key, 200, "")
	got := check("GET", host, "/sqlite/converted/tables/entries/rows", nil, ah, 200, "")
	if strings.Contains(string(got.Body), "owner-only") {
		t.Fatal("unattributed row leaked")
	}

	// Add-only public SQLite permits anonymous inserts, but no raw SQL.
	check("PUT", servertest.PcSiteDomain, "/resources/public", servertest.StorageRules("sqlite", servertest.RulesOpenAdd), key, 201, "")
	check("POST", servertest.PcSiteDomain, "/sqlite/public/schema", `{"sql":"CREATE TABLE notes (v TEXT)"}`, key, 200, "")
	check("POST", host, "/sqlite/public/tables/notes/rows", `{"v":"public"}`, servertest.Browser(host, ""), 200, "")
	check("GET", host, "/sqlite/public/tables/notes/rows", nil, nil, 200, "")
	check("POST", host, "/sqlite/public/execute", `{"sql":"INSERT INTO notes VALUES('x')"}`, servertest.Browser(host, ""), 403, "fixed_routes_required")
	for _, kind := range []string{"kv", "files"} {
		check("PUT", servertest.PcSiteDomain, "/resources/open-"+kind, servertest.StorageRules(kind, servertest.RulesOpenAdd), key, 201, "")
		path := map[string]string{"kv": "/kv/open-kv/keys/x", "files": "/files/open-files/objects/x"}[kind]
		body := any("x")
		if kind == "kv" {
			body = `{"value":1}`
		}
		check("PUT", host, path, body, servertest.Browser(host, ""), 200, "")
		check("PUT", host, path, body, servertest.Browser(host, ""), 409, "")
		check("DELETE", host, path, nil, servertest.Browser(host, ""), 403, "add_only")
	}
}

func TestStorageStorySeparateAllowances(t *testing.T) {
	t.Setenv("SITE_STORAGE_MAX_BYTES", "16384")
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "10")
	t.Setenv("SITE_STORAGE_FILE_MAX_BYTES", "100")
	a := servertest.NewPrivateApp(t)
	owner := a.NewPerson(t, "story-quota")
	a.Deploy(t, owner, "shop")
	base := "/v1/sites/shop/storage"
	key := map[string]string{"X-API-Key": owner.Key}
	call := func(method, path string, body any, status int) servertest.Resp {
		t.Helper()
		r := a.At(t, method, servertest.PcSiteDomain, base+path, body, key)
		if r.Status != status {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
		return r
	}
	for _, kind := range []string{"kv", "sqlite", "files"} {
		call("PUT", "/resources/"+kind, map[string]string{"kind": kind}, 201)
	}
	call("PUT", "/files/files/objects/full", "1234567890", 200)
	call("POST", "/sqlite/sqlite/schema", `{"sql":"CREATE TABLE t(v TEXT)"}`, 200)
	call("PUT", "/kv/kv/keys/data", `{"value":"yes"}`, 200)
	call("PUT", "/files/files/objects/overflow", "x", 507)
	call("POST", "/sqlite/sqlite/execute", `{"sql":"INSERT INTO t VALUES(randomblob(20000))"}`, 507)
	u := call("GET", "/usage", nil, 200).JSON(t)
	if u["files_used_bytes"] != float64(10) || u["files_remaining_bytes"] != float64(0) || u["used_bytes"].(float64) >= 16384 {
		t.Fatalf("usage %v", u)
	}
	call("DELETE", "/files/files/objects/full", nil, 200)
	call("PUT", "/files/files/objects/new", "ok", 200)
	call("PUT", "/files/files/objects/too-big", strings.Repeat("x", 101), 400)
	call("PUT", "/kv/kv/keys/full", map[string]any{"value": strings.Repeat("x", 16384)}, 507)
	call("GET", "/kv/kv/keys/full", nil, 404)
	// Files default is decimal 10 MB, and Hack keeps its old pool.
	t.Setenv("SITE_STORAGE_FILES_MAX_BYTES", "")
	if a.Data.StorageSvc().FilesLimitBytes() != 10000000 {
		t.Fatal("file default")
	}
	a.Sites.Ed = servertest.GroupTestEdition()
	a.Sites.Hosts.SetEdition(a.Sites.Ed)
	a.Access.Ed, a.Data.Ed = a.Sites.Ed, a.Sites.Ed
	if a.Data.StorageSvc().FilesLimitBytes() != 16384 || a.Data.StorageSvc().BudgetUsed(datahttp.SiteStorageUsage{KV: 1, SQLite: 2, Files: 3}) != 6 {
		t.Fatal("Hack allowance changed")
	}
}
