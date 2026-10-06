package handler

// Regression assertions ported from Claude's storage2/claude-poc tests, plus
// the second review's quota, listing, normalization and policy edge cases.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/internal/config"
)

type securityShop struct {
	a                     *privateApp
	host, ownerID, siteID string
	key, alice, bob       map[string]string
}

func newSecurityShop(t *testing.T) securityShop {
	t.Helper()
	a, cert := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "security-owner")
	alice := a.newPerson(t, "security-alice")
	bob := a.newPerson(t, "security-bob")
	a.deploy(t, owner, "shop")
	oid, handle := a.userID(t, owner)
	markReady(t, cert, handle)
	host := "shop." + handle + "." + pcSiteDomain
	sid := a.siteID(t, owner, "shop")
	return securityShop{a, host, oid, sid, map[string]string{"X-API-Key": owner.key}, browser(host, a.session(t, alice, sid, host)), browser(host, a.session(t, bob, sid, host))}
}
func (s securityShop) call(t *testing.T, method, path string, body any, headers map[string]string, status int) resp {
	t.Helper()
	host := s.host
	if headers["X-API-Key"] != "" {
		host = pcSiteDomain
	}
	r := s.a.at(t, method, host, "/v1/sites/shop/storage"+path, body, headers)
	if r.status != status {
		t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.status, r.body)
	}
	return r
}
func (s securityShop) resource(t *testing.T, name, kind, read, write, mode string) {
	t.Helper()
	s.call(t, "PUT", "/resources/"+name, map[string]string{"kind": kind, "read": read, "write": write, "write_mode": mode}, s.key, 201)
}
func (s securityShop) schema(t *testing.T, sql string) {
	t.Helper()
	s.call(t, "POST", "/sqlite/db/schema", map[string]string{"sql": sql}, s.key, 200)
}
func (s securityShop) add(t *testing.T, table string, body any, headers map[string]string, status int) resp {
	t.Helper()
	return s.call(t, "POST", "/sqlite/db/tables/"+table+"/rows", body, headers, status)
}

func TestStorageSecurityReplaceAndMaxRowidPOC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "own", "signed-in", "add")
	s.schema(t, "CREATE TABLE orders (id INTEGER PRIMARY KEY ON CONFLICT REPLACE, item TEXT UNIQUE ON CONFLICT REPLACE)")
	s.add(t, "orders", `{"item":"alice-secret"}`, s.alice, 200)
	for _, body := range []string{`{"id":1,"item":"overwritten"}`, `{"id":9223372036854775807}`, `{"rowid":1}`, `{"_rowid_":1}`, `{"oid":1}`} {
		s.add(t, "orders", body, s.bob, 400)
	}
	s.add(t, "orders", `{"item":"alice-secret"}`, s.bob, 409)
	got := s.call(t, "GET", "/sqlite/db/tables/orders/rows", nil, s.alice, 200)
	if !strings.Contains(string(got.body), "alice-secret") || len(got.json(t)["rows"].([]any)) != 1 {
		t.Fatal("REPLACE changed Alice's row")
	}
	s.schema(t, "CREATE TABLE auto (id INTEGER PRIMARY KEY AUTOINCREMENT, item TEXT)")
	s.add(t, "auto", `{"id":9223372036854775807,"item":"bricked"}`, s.bob, 400)
	for i := 0; i < 2; i++ {
		s.add(t, "auto", `{"item":"still works"}`, s.alice, 200)
	}
	s.schema(t, "CREATE TABLE text_keys (id TEXT PRIMARY KEY DEFAULT (hex(randomblob(8))), item TEXT)")
	s.add(t, "text_keys", `{"id":"chosen","item":"x"}`, s.bob, 400)
	s.add(t, "text_keys", `{"item":"assigned"}`, s.alice, 200)
	s.schema(t, "CREATE TABLE no_rowid (id TEXT PRIMARY KEY DEFAULT 'x') WITHOUT ROWID")
	s.add(t, "no_rowid", `{}`, s.alice, 400)
	s.call(t, "HEAD", "/sqlite/db/tables/orders/rows", nil, s.alice, 200)
}

func TestStorageSecurityReferencesCatalogAndOraclePOC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "own", "signed-in", "add")
	s.schema(t, "CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)")
	s.schema(t, "CREATE TABLE orders (id INTEGER PRIMARY KEY, product_id INTEGER REFERENCES products(id), item TEXT)")
	s.schema(t, "CREATE TABLE changes (id INTEGER PRIMARY KEY, order_id INTEGER REFERENCES orders(id) ON DELETE CASCADE, note TEXT)")
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO products(id,name) VALUES(7,'tea'),(8,'coffee')"}`, s.key, 200)
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"UPDATE products SET visitor_id='' WHERE id=8"}`, s.key, 200)
	for _, id := range []int{7, 8} {
		s.add(t, "orders", map[string]any{"item": "catalog", "product_id": id}, s.alice, 200)
	}
	other := s.add(t, "orders", `{"item":"bob"}`, s.bob, 200).json(t)["last_insert_id"]
	own := s.add(t, "orders", `{"item":"alice"}`, s.alice, 200).json(t)["last_insert_id"]
	a := s.add(t, "changes", map[string]any{"order_id": other}, s.alice, 404)
	b := s.add(t, "changes", `{"order_id":999999}`, s.alice, 404)
	if string(a.body) != string(b.body) {
		t.Fatalf("existence oracle %s / %s", a.body, b.body)
	}
	s.add(t, "changes", map[string]any{"order_id": own, "note": "history"}, s.alice, 200)
	s.call(t, "POST", "/sqlite/db/execute", map[string]any{"sql": "DELETE FROM orders WHERE id=?", "params": []any{own}}, s.key, 200)
	result := s.call(t, "POST", "/sqlite/db/query", `{"sql":"SELECT COUNT(*) FROM changes"}`, s.key, 200)
	if !strings.Contains(string(result.body), "[[0]]") {
		t.Fatal("foreign-key cascade was not enabled")
	}
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO changes(order_id) VALUES(999999)"}`, s.key, 400)
	s.schema(t, "CREATE TABLE pairs (id INTEGER PRIMARY KEY, code TEXT, UNIQUE(id,code))")
	pair := s.add(t, "pairs", `{"code":"bob-code"}`, s.bob, 200).json(t)["last_insert_id"]
	s.schema(t, "CREATE TABLE pair_changes (id INTEGER PRIMARY KEY,a INTEGER,b TEXT,FOREIGN KEY(a,b) REFERENCES pairs(id,code) DEFERRABLE INITIALLY DEFERRED)")
	for _, v := range []map[string]any{{"a": nil, "b": "bob-code"}, {"a": pair, "b": nil}, {"a": pair, "b": "bob-code"}, {"a": 999999, "b": "absent"}} {
		s.add(t, "pair_changes", v, s.alice, 404)
	}
	s.add(t, "pair_changes", `{"a":null,"b":null}`, s.alice, 200)
	s.add(t, "pair_changes", map[string]any{"a": pair, "b": "bob-code"}, s.bob, 200)
	// Unsafe parent schemas fail closed, even outside the schema-route invariant.
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.Exec("CREATE TABLE catalog(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER PRIMARY KEY, parent INTEGER REFERENCES catalog, visitor_id TEXT); INSERT INTO catalog VALUES(1); INSERT INTO child VALUES(1,1,'alice')"); err != nil {
		t.Fatal(err)
	}
	if _, err = storageCheckOwnReferences(conn, "child", "alice", 1); err == nil {
		t.Fatal("parent without visitor_id accepted")
	}
}

func TestStorageSecurityPolicyConversionAndOmittedMode(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "own", "signed-in", "add")
	s.schema(t, "CREATE TABLE orders (id INTEGER PRIMARY KEY,item TEXT)")
	for _, body := range []map[string]string{{"kind": "sqlite", "read": "own", "write": "signed-in"}, {"kind": "sqlite", "read": "anyone", "write": "signed-in", "site_passcode": "off"}} {
		if r := s.call(t, "PUT", "/resources/db", body, s.key, 200); r.json(t)["write_mode"] != "add" {
			t.Fatal("omitted mode reset add")
		}
	}
	s.add(t, "orders", `{"item":"earlier"}`, s.alice, 200)
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"UPDATE orders SET visitor_id='forged'"}`, s.key, 200)
	s.call(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite", "read": "own", "write": "signed-in"}, s.key, 400)
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"UPDATE orders SET visitor_id=NULL"}`, s.key, 200)
	s.call(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite", "read": "own", "write": "signed-in"}, s.key, 400)
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"DELETE FROM orders"}`, s.key, 200)
	s.call(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite", "read": "own", "write": "signed-in"}, s.key, 200)
	// Missing identity refuses even if a later cookie lookup could succeed.
	req := httptest.NewRequest("GET", "https://"+s.host+"/", nil)
	for k, v := range s.alice {
		req.Header.Set(k, v)
	}
	c := storageCall{siteID: s.siteID, resource: storageResource{Read: "own"}}
	w := httptest.NewRecorder()
	if s.a.sites.storageAccess(w, req, c, false) || w.Code != 401 || c.ownReader() == "" {
		t.Fatal("own reads failed open")
	}
	c.owner = true
	if c.ownReader() != "" {
		t.Fatal("explicit owner should read all")
	}
}

func TestStorageSecurityFileObjectsListingsAndUnicode(t *testing.T) {
	old := *config.Active()
	lim := old
	lim.StorageFileObjects = 2
	ApplyLimits(lim)
	t.Cleanup(func() { ApplyLimits(old) })
	s := newSecurityShop(t)
	s.resource(t, "files", "files", "own", "signed-in", "add")
	s.resource(t, "public", "files", "anyone", "anyone", "full")
	s.resource(t, "kv", "kv", "own", "signed-in", "add")
	p := "/files/files/objects/"
	s.call(t, "PUT", p+"a", "", s.alice, 200)
	s.call(t, "PUT", p+"b", "x", s.bob, 200)
	s.call(t, "PUT", p+"c", "", s.alice, 507)
	c := storageCall{ownerID: s.ownerID, siteName: "shop", resourceName: "files"}
	blocked := filepath.Join(s.a.sites.storageFilesDir(c), "other-private")
	if err := os.MkdirAll(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	list := s.call(t, "GET", strings.TrimSuffix(p, "/")+"?limit=1", nil, s.alice, 200)
	if len(list.json(t)["items"].([]any)) != 1 || strings.Contains(string(list.body), `"path":"b"`) {
		t.Fatal("own listing not indexed/scoped")
	}
	os.Chmod(blocked, 0700)
	// Both public walks and metadata-backed own lists omit temporary objects.
	for _, name := range []string{"files", "public"} {
		c.resourceName = name
		dir := s.a.sites.storageFilesDir(c)
		os.MkdirAll(dir, 0700)
		if err := os.WriteFile(filepath.Join(dir, ".upload-secret"), []byte("secret bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.a.database.Exec(`INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) SELECT $1,$2,'.upload-secret',writer_id FROM site_storage_files WHERE site_id=$1 AND resource_name='files' AND path='a'`, s.siteID, name); err != nil {
			t.Fatal(err)
		}
		r := s.call(t, "GET", "/files/"+name+"/objects", nil, s.alice, 200)
		if strings.Contains(string(r.body), "upload-secret") {
			t.Fatal("temporary object listed")
		}
	}
	// Cap belongs to each bucket, so owners can overwrite/delete at capacity.
	s.call(t, "PUT", p+"a", "new", s.key, 200)
	s.call(t, "DELETE", p+"b", nil, s.key, 200)
	s.call(t, "PUT", p+"c", "", s.alice, 200)
	for _, kind := range []string{"kv", "files"} {
		route := "/kv/kv/keys/"
		body := any(`{"value":1}`)
		if kind == "files" {
			route = "/files/public/objects/"
			body = "x"
		}
		s.call(t, "PUT", route+url.PathEscape("cafe\u0301"), body, s.alice, 200)
		want := 200
		if kind == "kv" {
			want = 409
		} // normalized add-only name already exists
		s.call(t, "PUT", route+url.PathEscape("café"), body, s.alice, want)
		s.call(t, "GET", strings.TrimSuffix(route, "/")+"?prefix="+url.QueryEscape("cafe\u0301"), nil, s.alice, 200)
	}
	s.call(t, "PUT", "/files/public/objects/"+strings.Repeat("a/", 16)+"file", "", s.alice, 400)
	// Own read missing and another writer have identical responses.
	for _, route := range []string{p + "a", "/kv/kv/keys/café"} {
		a := s.call(t, "GET", route, nil, s.bob, 404)
		b := s.call(t, "GET", route+"-absent", nil, s.bob, 404)
		if string(a.body) != string(b.body) {
			t.Fatal("object existence oracle")
		}
	}
	link := s.call(t, "POST", "/files/files/download-link", `{"path":"a"}`, s.key, 200).json(t)["url"].(string)
	u, _ := url.Parse(link)
	r := s.a.at(t, "GET", pcSiteDomain, u.RequestURI(), nil, nil)
	if r.status != 200 || string(r.body) != "new" {
		t.Fatalf("explicit owner link: %d %s", r.status, r.body)
	}
}

func TestStorageSecurityKVKeyQuotaAndConcurrentCreate(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "kv", "kv", "own", "signed-in", "add")
	t.Setenv("SITE_STORAGE_MAX_BYTES", "5")
	s.call(t, "PUT", "/kv/kv/keys/long-key", `{"value":1}`, s.alice, 507)
	s.call(t, "PUT", "/kv/kv/keys/key", `{"value":1}`, s.alice, 200)
	u := s.call(t, "GET", "/usage", nil, s.key, 200).json(t)
	if u["used_bytes"] != float64(4) {
		t.Fatalf("key bytes not counted: %v", u)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "1000000")
	// Exercise the actual save statement with concurrent independent connections,
	// deliberately without lockSite or the existence preflight.
	c := storageCall{siteID: s.siteID, resourceName: "kv", resource: storageResource{WriteMode: "add"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	winner := ""
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cc := c
			cc.visitorID = fmt.Sprintf("writer-%d", i)
			result, err := s.a.sites.storageKVSave(context.Background(), cc, "race", json.RawMessage(fmt.Sprint(i)))
			if err != nil {
				t.Error(err)
				return
			}
			n, err := result.RowsAffected()
			if err != nil {
				t.Error(err)
				return
			}
			if n == 1 {
				mu.Lock()
				wins++
				winner = cc.visitorID
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent creators succeeded", wins)
	}
	var writer, value string
	if err := s.a.database.QueryRow(`SELECT writer_id,value::text FROM site_storage_kv WHERE site_id=$1 AND key='race'`, s.siteID).Scan(&writer, &value); err != nil {
		t.Fatal(err)
	}
	if writer != winner || writer != "writer-"+value {
		t.Fatalf("overwritten creator: %s/%s vs %s", writer, value, winner)
	}
	// Full-mode overwrite tracks the last visitor; owner edits retain attribution.
	s.call(t, "PUT", "/resources/kv", map[string]string{"kind": "kv", "read": "anyone", "write": "signed-in", "write_mode": "full"}, s.key, 200)
	s.call(t, "PUT", "/kv/kv/keys/key", `{"value":2}`, s.bob, 200)
	var before, after string
	s.a.database.QueryRow(`SELECT writer_id FROM site_storage_kv WHERE site_id=$1 AND key='key'`, s.siteID).Scan(&before)
	s.call(t, "PUT", "/kv/kv/keys/key", `{"value":3}`, s.key, 200)
	s.a.database.QueryRow(`SELECT writer_id FROM site_storage_kv WHERE site_id=$1 AND key='key'`, s.siteID).Scan(&after)
	if before == "" || before != after {
		t.Fatal("writer attribution not preserved for owner")
	}
}

func TestStorageSecurityVisitorRateLimits(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "kv", "kv", "anyone", "anyone", "add")
	s.resource(t, "files", "files", "anyone", "anyone", "add")
	s.resource(t, "db", "sqlite", "own", "signed-in", "add")
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT)")
	s.a.sites.storageVisitorLimiter = newRateLimiter(1, 0)
	s.a.sites.storageIPLimiter = newRateLimiter(100, 0)
	s.call(t, "PUT", "/kv/kv/keys/a", `{"value":1}`, s.alice, 200)
	s.call(t, "PUT", "/files/files/objects/a", "x", s.alice, 429)
	s.add(t, "orders", `{"item":"x"}`, s.alice, 429)
	for _, route := range []string{"/files/files/objects", "/sqlite/db/tables/orders/rows", "/kv/kv/keys"} {
		s.call(t, "GET", route, nil, s.alice, 200)
	}
	s.call(t, "PUT", "/kv/kv/keys/b", `{"value":1}`, s.bob, 200)
	s.call(t, "PUT", "/kv/kv/keys/c", `{"value":1}`, s.key, 200)
	s.a.sites.storageIPLimiter = newRateLimiter(1, 0)
	s.call(t, "PUT", "/kv/kv/keys/d", `{"value":1}`, browser(s.host, ""), 200)
	s.call(t, "PUT", "/files/files/objects/d", "x", browser(s.host, ""), 429)
	s.call(t, "GET", "/files/files/objects", nil, nil, 200)
	s.call(t, "PUT", "/files/files/objects/d", "x", s.key, 200)
}

// Body reader signals the exact instant the handler starts reading, then waits.
type securitySlowBody struct {
	entered, release chan struct{}
	once             sync.Once
	body             io.Reader
}

func (b *securitySlowBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.body.Read(p)
}
func (b *securitySlowBody) Close() error { return nil }
func TestStorageSecuritySlowBodiesDoNotHoldSiteLockPOC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "anyone", "anyone", "full")
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT)")
	s.resource(t, "kv", "kv", "anyone", "anyone", "full")
	s.resource(t, "files", "files", "anyone", "anyone", "full")
	for _, tc := range []struct {
		method, path, body string
		owner              bool
	}{{"POST", "/sqlite/db/query", `{"sql":"SELECT 1"}`, false}, {"POST", "/sqlite/db/tables/orders/rows", `{"item":"x"}`, false}, {"POST", "/sqlite/db/schema", `{"sql":"CREATE TABLE x(v)"}`, true}, {"PUT", "/kv/kv/keys/slow", `{"value":1}`, false}, {"PUT", "/files/files/objects/slow", "x", false}, {"PUT", "/resources/db", `{"kind":"sqlite","read":"anyone","write":"anyone"}`, true}} {
		t.Run(tc.path, func(t *testing.T) {
			b := &securitySlowBody{make(chan struct{}), make(chan struct{}), sync.Once{}, strings.NewReader(tc.body)}
			req := httptest.NewRequest(tc.method, "https://"+s.host+"/v1/sites/shop/storage"+tc.path, b)
			headers := browser(s.host, "")
			if tc.owner {
				headers = s.key
				req.Host = pcSiteDomain
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			done := make(chan struct{})
			go func() { s.a.sites.SiteHosts(s.a.mux, s.a.mux).ServeHTTP(httptest.NewRecorder(), req); close(done) }()
			select {
			case <-b.entered:
			case <-time.After(2 * time.Second):
				close(b.release)
				t.Fatal("body not reached")
			}
			writes := make(chan struct{})
			go func() {
				s.call(t, "PUT", "/kv/kv/keys/owner", `{"value":1}`, s.key, 200)
				r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "x"}}, s.key)
				if r.status != 200 && r.status != 409 {
					t.Errorf("deploy %d %s", r.status, r.body)
				}
				close(writes)
			}()
			select {
			case <-writes:
			case <-time.After(1500 * time.Millisecond):
				t.Error("slow body blocked owner write/deploy")
			}
			close(b.release)
			<-done
		})
	}
}

func TestStorageSecurityReadsAndBoundedLockPOC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "anyone", "owner", "full")
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT)")
	s.resource(t, "kv", "kv", "anyone", "anyone", "full")
	s.resource(t, "files", "files", "anyone", "anyone", "full")
	s.call(t, "PUT", "/files/files/objects/photo", "photo", s.key, 200)
	unlock := s.a.sites.lockSite(s.ownerID, "shop")
	for _, route := range []string{"/sqlite/db/tables/orders/rows", "/kv/kv/keys", "/files/files/objects/photo", "/files/files/objects"} {
		done := make(chan struct{})
		go func() { s.call(t, "GET", route, nil, nil, 200); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			unlock()
			t.Fatal("read took deploy lock")
		}
	}
	start := time.Now()
	s.call(t, "PUT", "/kv/kv/keys/blocked", `{"value":1}`, browser(s.host, ""), 503)
	elapsed := time.Since(start)
	unlock()
	if elapsed > config.Active().StorageWriteLockWait+500*time.Millisecond {
		t.Fatalf("unbounded lock wait %v", elapsed)
	}
	slow := make(chan struct{})
	go func() {
		s.call(t, "POST", "/sqlite/db/query", `{"sql":"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c"}`, browser(s.host, ""), 400)
		close(slow)
	}()
	time.Sleep(100 * time.Millisecond)
	start = time.Now()
	s.call(t, "PUT", "/kv/kv/keys/available", `{"value":1}`, s.key, 200)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("slow query held deploy lock")
	}
	<-slow
}

func TestStorageSecurityWideTablesResultsAndGenericErrors(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "own", "signed-in", "add")
	cols := []string{"id INTEGER PRIMARY KEY"}
	for i := 0; i < 100; i++ {
		cols = append(cols, fmt.Sprintf("c%d TEXT", i))
	}
	s.schema(t, "CREATE TABLE wide("+strings.Join(cols, ",")+")")
	s.call(t, "GET", "/sqlite/db/tables/wide/rows", nil, s.alice, 400)
	s.add(t, "wide", `{"c1":"x"}`, s.alice, 400)
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT UNIQUE)")
	s.add(t, "orders", `{"item":"secret"}`, s.bob, 200)
	a := s.add(t, "orders", `{"item":"secret"}`, s.alice, 409)
	b := s.add(t, "orders", `{"bad-column":1}`, s.alice, 400)
	if strings.Contains(string(a.body), "orders") || strings.Contains(string(b.body), "bad-column") {
		t.Fatal("schema reflected")
	}
	missing := s.call(t, "GET", "/sqlite/db/tables/missing-sensitive-name/rows", nil, s.alice, 400)
	unknown := s.call(t, "GET", "/sqlite/db/tables/orders/rows?order=unknown-sensitive-name", nil, s.alice, 400)
	if string(missing.body) != string(unknown.body) {
		t.Fatal("schema-specific error")
	}
	s.add(t, "orders", map[string]string{"item": strings.Repeat("a", 1000)}, s.alice, 200)
	t.Setenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES", "200")
	s.call(t, "GET", "/sqlite/db/tables/orders/rows", nil, s.alice, 400)
}
