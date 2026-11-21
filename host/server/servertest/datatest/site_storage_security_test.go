package datatest

// Regression assertions ported from Claude's storage2/claude-poc tests, plus
// the second review's quota, listing, normalization and policy edge cases.
import (
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
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type securityShop struct {
	a                     *servertest.PrivateApp
	host, ownerID, siteID string
	key, alice, bob       map[string]string
}

func newSecurityShop(t *testing.T) securityShop {
	t.Helper()
	a, cert := servertest.NewSiteApp(t, "canonical")
	owner := a.NewPerson(t, "security-owner")
	alice := a.NewPerson(t, "security-alice")
	bob := a.NewPerson(t, "security-bob")
	a.Deploy(t, owner, "shop")
	oid, handle := a.UserID(t, owner)
	servertest.MarkReady(t, cert, handle)
	host := "shop." + handle + "." + servertest.PcSiteDomain
	sid := a.SiteID(t, owner, "shop")
	return securityShop{a, host, oid, sid, map[string]string{"X-API-Key": owner.Key}, servertest.Browser(host, a.Session(t, alice, sid, host)), servertest.Browser(host, a.Session(t, bob, sid, host))}
}
func (s securityShop) call(t *testing.T, method, path string, body any, headers map[string]string, status int) servertest.Resp {
	t.Helper()
	host := s.host
	if headers["X-API-Key"] != "" {
		host = servertest.PcSiteDomain
	}
	r := s.a.At(t, method, host, "/v1/sites/shop/storage"+path, body, headers)
	if r.Status != status {
		t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.Status, r.Body)
	}
	return r
}
func (s securityShop) resource(t *testing.T, name, kind, read, write, mode string) {
	t.Helper()
	// Every kind takes rules: the same access as the older fields.
	rules := map[string]string{
		"own/signed-in/add":    servertest.RulesRecords,
		"anyone/anyone/add":    servertest.RulesOpenAdd,
		"anyone/signed-in/add": servertest.RulesSignedInAdd,
		// Anyone editing or deleting is not offered; these tests only add.
		"anyone/anyone/full": servertest.RulesOpenAdd,
		"anyone/owner/full":  servertest.RulesPublic,
	}[read+"/"+write+"/"+mode]
	if rules == "" {
		t.Fatalf("no rules for %s/%s/%s", read, write, mode)
	}
	s.call(t, "PUT", "/resources/"+name, servertest.StorageRules(kind, rules), s.key, 201)
}
func (s securityShop) schema(t *testing.T, sql string) {
	t.Helper()
	s.call(t, "POST", "/sqlite/db/schema", map[string]string{"sql": sql}, s.key, 200)
}
func (s securityShop) add(t *testing.T, table string, body any, headers map[string]string, status int) servertest.Resp {
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
	if !strings.Contains(string(got.Body), "alice-secret") || len(got.JSON(t)["rows"].([]any)) != 1 {
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
	// A reference needs a parent the visitor may read: the owner's rows in
	// an own table are not, so the catalogue goes public.
	s.add(t, "orders", map[string]any{"item": "catalog", "product_id": 7}, s.alice, 404)
	s.call(t, "PUT", "/resources/db", `{"kind":"sqlite","rules":`+servertest.RulesRecords+`,"tables":{"products":{"rules":`+servertest.RulesPublic+`}}}`, s.key, 200)
	for _, id := range []int{7, 8} {
		s.add(t, "orders", map[string]any{"item": "catalog", "product_id": id}, s.alice, 200)
	}
	other := s.add(t, "orders", `{"item":"bob"}`, s.bob, 200).JSON(t)["last_insert_id"]
	own := s.add(t, "orders", `{"item":"alice"}`, s.alice, 200).JSON(t)["last_insert_id"]
	a := s.add(t, "changes", map[string]any{"order_id": other}, s.alice, 404)
	b := s.add(t, "changes", `{"order_id":999999}`, s.alice, 404)
	if string(a.Body) != string(b.Body) {
		t.Fatalf("existence oracle %s / %s", a.Body, b.Body)
	}
	s.add(t, "changes", map[string]any{"order_id": own, "note": "history"}, s.alice, 200)
	s.call(t, "POST", "/sqlite/db/execute", map[string]any{"sql": "DELETE FROM orders WHERE id=?", "params": []any{own}}, s.key, 200)
	result := s.call(t, "POST", "/sqlite/db/query", `{"sql":"SELECT COUNT(*) FROM changes"}`, s.key, 200)
	if !strings.Contains(string(result.Body), "[[0]]") {
		t.Fatal("foreign-key cascade was not enabled")
	}
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO changes(order_id) VALUES(999999)"}`, s.key, 400)
	s.schema(t, "CREATE TABLE pairs (id INTEGER PRIMARY KEY, code TEXT, UNIQUE(id,code))")
	pair := s.add(t, "pairs", `{"code":"bob-code"}`, s.bob, 200).JSON(t)["last_insert_id"]
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
	// The parent is read with an own read filter for alice.
	if _, err = storage.CheckReferences(conn, "child", "rowid", 1, func(parent string) (ruleengine.Predicate, error) {
		return ruleengine.Predicate{SQL: `"visitor_id" COLLATE BINARY = ?`, Args: []ruleengine.Value{ruleengine.Text("alice")}}, nil
	}); err == nil {
		t.Fatal("parent without visitor_id accepted")
	}
}

func TestStorageSecurityFileObjectsListingsAndUnicode(t *testing.T) {
	s := newSecurityShop(t)
	lim := config.DefaultLimits()
	lim.StorageFileObjects = 2
	s.a.Data.Limits = lim
	s.resource(t, "files", "files", "own", "signed-in", "add")
	s.resource(t, "public", "files", "anyone", "anyone", "full")
	s.resource(t, "kv", "kv", "own", "signed-in", "add")
	p := "/files/files/objects/"
	s.call(t, "PUT", p+"a", "", s.alice, 200)
	s.call(t, "PUT", p+"b", "x", s.bob, 200)
	s.call(t, "PUT", p+"c", "", s.alice, 507)
	c := datahttp.StorageCall{OwnerID: s.ownerID, SiteName: "shop", ResourceName: "files"}
	blocked := filepath.Join(s.a.Data.StorageFilesDir(c), "other-private")
	if err := os.MkdirAll(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	list := s.call(t, "GET", strings.TrimSuffix(p, "/")+"?limit=1", nil, s.alice, 200)
	if len(list.JSON(t)["items"].([]any)) != 1 || strings.Contains(string(list.Body), `"path":"b"`) {
		t.Fatal("own listing not indexed/scoped")
	}
	os.Chmod(blocked, 0700)
	// Both public walks and metadata-backed own lists omit temporary objects.
	for _, name := range []string{"files", "public"} {
		c.ResourceName = name
		dir := s.a.Data.StorageFilesDir(c)
		os.MkdirAll(dir, 0700)
		if err := os.WriteFile(filepath.Join(dir, ".upload-secret"), []byte("secret bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.a.Database.Exec(`INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) SELECT $1,$2,'.upload-secret',writer_id FROM site_storage_files WHERE site_id=$1 AND resource_name='files' AND path='a'`, s.siteID, name); err != nil {
			t.Fatal(err)
		}
		r := s.call(t, "GET", "/files/"+name+"/objects", nil, s.alice, 200)
		if strings.Contains(string(r.Body), "upload-secret") {
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
		// normalized add-only name already exists
		s.call(t, "PUT", route+url.PathEscape("café"), body, s.alice, 409)
		s.call(t, "GET", strings.TrimSuffix(route, "/")+"?prefix="+url.QueryEscape("cafe\u0301"), nil, s.alice, 200)
	}
	s.call(t, "PUT", "/files/public/objects/"+strings.Repeat("a/", 16)+"file", "", s.alice, 400)
	// Own read missing and another writer have identical responses.
	for _, route := range []string{p + "a", "/kv/kv/keys/café"} {
		a := s.call(t, "GET", route, nil, s.bob, 404)
		b := s.call(t, "GET", route+"-absent", nil, s.bob, 404)
		if string(a.Body) != string(b.Body) {
			t.Fatal("object existence oracle")
		}
	}
	link := s.call(t, "POST", "/files/files/download-link", `{"path":"a"}`, s.key, 200).JSON(t)["url"].(string)
	u, _ := url.Parse(link)
	r := s.a.At(t, "GET", servertest.PcSiteDomain, u.RequestURI(), nil, nil)
	if r.Status != 200 || string(r.Body) != "new" {
		t.Fatalf("explicit owner link: %d %s", r.Status, r.Body)
	}
}

func TestStorageSecurityKVKeyQuotaAndConcurrentCreate(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "kv", "kv", "own", "signed-in", "add")
	t.Setenv("SITE_STORAGE_MAX_BYTES", "5")
	s.call(t, "PUT", "/kv/kv/keys/long-key", `{"value":1}`, s.alice, 507)
	s.call(t, "PUT", "/kv/kv/keys/key", `{"value":1}`, s.alice, 200)
	u := s.call(t, "GET", "/usage", nil, s.key, 200).JSON(t)
	if u["used_bytes"] != float64(4) {
		t.Fatalf("key bytes not counted: %v", u)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "1000000")
	// Exercise the actual save statement with concurrent independent connections,
	// deliberately without lockSite or the existence preflight.
	c := servertest.StorageKVFixtureCall(t, s.a.Data, s.siteID, "kv")
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
			cc.VisitorID = fmt.Sprintf("writer-%d", i)
			inserted, err := insertStorageKVRace(t, s.a.Data, cc, "race", fmt.Sprint(i), cc.VisitorID)
			if err != nil {
				t.Error(err)
				return
			}
			if inserted {
				mu.Lock()
				wins++
				winner = cc.VisitorID
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent creators succeeded", wins)
	}
	writer := servertest.ReadStorageKVAuthor(t, s.a.Data, s.siteID, "kv", "race")
	value := servertest.ReadStorageKVValue(t, s.a.Data, c, "race")
	if writer != winner || writer != "writer-"+value {
		t.Fatalf("overwritten creator: %s/%s vs %s", writer, value, winner)
	}
	// Full-mode overwrite tracks the last visitor; owner edits retain attribution.
	s.call(t, "PUT", "/resources/kv", servertest.StorageRules("kv", servertest.RulesSignedInWrite), s.key, 200)
	s.call(t, "PUT", "/kv/kv/keys/key", `{"value":2}`, s.bob, 200)
	var before, after string
	before = servertest.ReadStorageKVAuthor(t, s.a.Data, s.siteID, "kv", "key")
	s.call(t, "PUT", "/kv/kv/keys/key", `{"value":3}`, s.key, 200)
	after = servertest.ReadStorageKVAuthor(t, s.a.Data, s.siteID, "kv", "key")
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
	s.a.Data.StorageVisitorLimiter = httpx.NewRateLimiter(1, 0)
	s.a.Data.StorageIPLimiter = httpx.NewRateLimiter(100, 0)
	s.call(t, "PUT", "/kv/kv/keys/a", `{"value":1}`, s.alice, 200)
	s.call(t, "PUT", "/files/files/objects/a", "x", s.alice, 429)
	s.add(t, "orders", `{"item":"x"}`, s.alice, 429)
	for _, route := range []string{"/files/files/objects", "/sqlite/db/tables/orders/rows", "/kv/kv/keys"} {
		s.call(t, "GET", route, nil, s.alice, 200)
	}
	s.call(t, "PUT", "/kv/kv/keys/b", `{"value":1}`, s.bob, 200)
	s.call(t, "PUT", "/kv/kv/keys/c", `{"value":1}`, s.key, 200)
	s.a.Data.StorageIPLimiter = httpx.NewRateLimiter(1, 0)
	s.call(t, "PUT", "/kv/kv/keys/d", `{"value":1}`, servertest.Browser(s.host, ""), 200)
	s.call(t, "PUT", "/files/files/objects/d", "x", servertest.Browser(s.host, ""), 429)
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
	}{{"POST", "/sqlite/db/tables/orders/rows", `{"item":"x"}`, false}, {"POST", "/sqlite/db/schema", `{"sql":"CREATE TABLE x(v)"}`, true}, {"PUT", "/kv/kv/keys/slow", `{"value":1}`, false}, {"PUT", "/files/files/objects/slow", "x", false}, {"PUT", "/resources/db", servertest.StorageRules("sqlite", servertest.RulesOpenAdd), true}} {
		t.Run(tc.path, func(t *testing.T) {
			b := &securitySlowBody{make(chan struct{}), make(chan struct{}), sync.Once{}, strings.NewReader(tc.body)}
			req := httptest.NewRequest(tc.method, "https://"+s.host+"/v1/sites/shop/storage"+tc.path, b)
			headers := servertest.Browser(s.host, "")
			if tc.owner {
				headers = s.key
				req.Host = servertest.PcSiteDomain
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			done := make(chan struct{})
			go func() { s.a.Sites.SiteHosts(s.a.Mux, s.a.Mux).ServeHTTP(httptest.NewRecorder(), req); close(done) }()
			select {
			case <-b.entered:
			case <-time.After(2 * time.Second):
				close(b.release)
				t.Fatal("body not reached")
			}
			writes := make(chan struct{})
			go func() {
				s.call(t, "PUT", "/kv/kv/keys/owner", `{"value":1}`, s.key, 200)
				r := s.a.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "x"}}, s.key)
				if r.Status != 200 && r.Status != 409 {
					t.Errorf("deploy %d %s", r.Status, r.Body)
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
	// A read that needs the deploy lock cannot finish until the watchdog fires.
	unlock := sync.OnceFunc(s.a.Sites.LockSite(s.ownerID, "shop"))
	defer unlock()
	watchdog := time.AfterFunc(30*time.Second, unlock)
	defer watchdog.Stop()
	for _, route := range []string{"/sqlite/db/tables/orders/rows", "/kv/kv/keys", "/files/files/objects/photo", "/files/files/objects"} {
		s.call(t, "GET", route, nil, nil, 200)
	}
	start := time.Now()
	s.call(t, "PUT", "/kv/kv/keys/blocked", `{"value":1}`, servertest.Browser(s.host, ""), 503)
	if elapsed := time.Since(start); elapsed > s.a.Sites.Limits.StorageWriteLockWait+3*time.Second {
		t.Fatalf("unbounded lock wait %v", elapsed)
	}
	if !watchdog.Stop() {
		t.Fatal("storage requests did not complete while the deploy lock was held")
	}
	unlock()
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
	if strings.Contains(string(a.Body), "orders") || strings.Contains(string(b.Body), "bad-column") {
		t.Fatal("schema reflected")
	}
	missing := s.call(t, "GET", "/sqlite/db/tables/missing-sensitive-name/rows", nil, s.alice, 400)
	unknown := s.call(t, "GET", "/sqlite/db/tables/orders/rows?order=unknown-sensitive-name", nil, s.alice, 400)
	if string(missing.Body) != string(unknown.Body) {
		t.Fatal("schema-specific error")
	}
	s.add(t, "orders", map[string]string{"item": strings.Repeat("a", 1000)}, s.alice, 200)
	t.Setenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES", "200")
	s.call(t, "GET", "/sqlite/db/tables/orders/rows", nil, s.alice, 400)
}
