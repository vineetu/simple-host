package datatest

// Regression assertions from the storage security reviews: row ids,
// references, listings, quotas, rate limits, locks and generic errors.
import (
	"context"
	"errors"
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
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func (s Shop) resource(t *testing.T, name, kind, rules string) {
	t.Helper()
	s.Must(t, "PUT", "/resources/"+name, StorageRules(kind, rules), s.Key, 201)
}

func (s Shop) schema(t *testing.T, sql string) {
	t.Helper()
	s.Must(t, "POST", "/sqlite/db/schema", map[string]string{"sql": sql}, s.Key, 200)
}

func (s Shop) add(t *testing.T, table string, body any, headers map[string]string, status int) rt.Resp {
	t.Helper()
	return s.Must(t, "POST", "/sqlite/db/tables/"+table+"/rows", body, headers, status)
}

func (s Shop) lockSite() func() { return sites.LockSiteName(s.A.Data.UploadLocks, s.OwnerID, "shop") }

func TestStorageSecurityRowIDs(t *testing.T) {
	s := NewShop(t)
	alice, bob := s.Callers["alice"], s.Callers["bob"]
	s.resource(t, "db", "sqlite", RulesRecords)
	s.schema(t, "CREATE TABLE orders (id INTEGER PRIMARY KEY ON CONFLICT REPLACE, item TEXT UNIQUE ON CONFLICT REPLACE)")
	s.add(t, "orders", `{"item":"alice-secret"}`, alice, 200)
	// A visitor never picks a row id (REPLACE would overwrite another's row).
	for _, body := range []string{`{"id":1,"item":"overwritten"}`, `{"id":9223372036854775807}`, `{"rowid":1}`, `{"_rowid_":1}`, `{"oid":1}`} {
		s.add(t, "orders", body, bob, 400)
	}
	s.add(t, "orders", `{"item":"alice-secret"}`, bob, 409)
	if got := s.Must(t, "GET", "/sqlite/db/tables/orders/rows", nil, alice, 200); len(got.JSON(t)["rows"].([]any)) != 1 || !strings.Contains(string(got.Body), "alice-secret") {
		t.Fatal("REPLACE changed Alice's row")
	}
	s.schema(t, "CREATE TABLE text_keys (id TEXT PRIMARY KEY DEFAULT (hex(randomblob(8))), item TEXT)")
	s.add(t, "text_keys", `{"id":"chosen","item":"x"}`, bob, 400)
	s.add(t, "text_keys", `{"item":"assigned"}`, alice, 200)
	s.schema(t, "CREATE TABLE no_rowid (id TEXT PRIMARY KEY DEFAULT 'x') WITHOUT ROWID")
	s.add(t, "no_rowid", `{}`, alice, 400)
}

func TestStorageSecurityReferences(t *testing.T) {
	s := NewShop(t)
	alice, bob := s.Callers["alice"], s.Callers["bob"]
	s.resource(t, "db", "sqlite", RulesRecords)
	s.schema(t, "CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)")
	s.schema(t, "CREATE TABLE orders (id INTEGER PRIMARY KEY, product_id INTEGER REFERENCES products(id), item TEXT)")
	s.schema(t, "CREATE TABLE changes (id INTEGER PRIMARY KEY, order_id INTEGER REFERENCES orders(id) ON DELETE CASCADE, note TEXT)")
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO products(id,name) VALUES(7,'tea')"}`, s.Key, 200)
	// The owner's rows in an own table are not readable to the visitor, so
	// not referable; a public catalogue table is.
	s.add(t, "orders", map[string]any{"item": "catalog", "product_id": 7}, alice, 404)
	s.Must(t, "PUT", "/resources/db", `{"kind":"sqlite","rules":`+RulesRecords+`,"tables":{"products":{"rules":`+RulesPublic+`}}}`, s.Key, 200)
	s.add(t, "orders", map[string]any{"item": "catalog", "product_id": 7}, alice, 200)
	other := s.add(t, "orders", `{"item":"bob"}`, bob, 200).JSON(t)["last_insert_id"]
	own := s.add(t, "orders", `{"item":"alice"}`, alice, 200).JSON(t)["last_insert_id"]
	// Another visitor's row and a missing one answer alike (no existence oracle).
	if a, b := s.add(t, "changes", map[string]any{"order_id": other}, alice, 404), s.add(t, "changes", `{"order_id":999999}`, alice, 404); string(a.Body) != string(b.Body) {
		t.Fatalf("existence oracle %s / %s", a.Body, b.Body)
	}
	s.add(t, "changes", map[string]any{"order_id": own}, alice, 200)
	// Foreign keys are enforced, cascades included.
	s.Must(t, "POST", "/sqlite/db/execute", map[string]any{"sql": "DELETE FROM orders WHERE id=?", "params": []any{own}}, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/db/query", `{"sql":"SELECT COUNT(*) FROM changes"}`, s.Key, 200); !strings.Contains(string(r.Body), "[[0]]") {
		t.Fatal("foreign-key cascade was not enabled")
	}
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO changes(order_id) VALUES(999999)"}`, s.Key, 400)
}

func TestStorageSecurityFileObjects(t *testing.T) {
	s := NewShop(t)
	alice, bob := s.Callers["alice"], s.Callers["bob"]
	lim := config.DefaultLimits()
	lim.StorageFileObjects = 2
	s.A.Data.Limits = lim
	s.resource(t, "files", "files", RulesRecords)
	s.resource(t, "public", "files", RulesOpenAdd)
	s.resource(t, "kv", "kv", RulesRecords)
	p := "/files/files/objects/"
	s.Must(t, "PUT", p+"a", "", alice, 200)
	s.Must(t, "PUT", p+"b", "x", bob, 200)
	s.Must(t, "PUT", p+"c", "", alice, 507)
	// An own listing shows only the caller's objects.
	if list := s.Must(t, "GET", "/files/files/objects", nil, alice, 200); len(list.JSON(t)["items"].([]any)) != 1 || strings.Contains(string(list.Body), `"path":"b"`) {
		t.Fatalf("own listing not scoped: %s", list.Body)
	}
	// Temporary upload objects are never listed.
	c := datahttp.StorageCall{OwnerID: s.OwnerID, SiteName: "shop"}
	for _, name := range []string{"files", "public"} {
		c.ResourceName = name
		dir := s.A.Data.StorageFilesDir(c)
		_ = os.MkdirAll(dir, 0o700)
		if err := os.WriteFile(filepath.Join(dir, ".upload-secret"), []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.A.Database.Exec(`INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) SELECT $1,$2,'.upload-secret',writer_id FROM site_storage_files WHERE site_id=$1 AND resource_name='files' AND path='a'`, s.Sid, name); err != nil {
			t.Fatal(err)
		}
		if r := s.Must(t, "GET", "/files/"+name+"/objects", nil, alice, 200); strings.Contains(string(r.Body), "upload-secret") {
			t.Fatal("temporary object listed")
		}
	}
	// The cap is per bucket: the owner still overwrites and deletes at it.
	s.Must(t, "PUT", p+"a", "new", s.Key, 200)
	s.Must(t, "DELETE", p+"b", nil, s.Key, 200)
	s.Must(t, "PUT", p+"c", "", alice, 200)
	// Names are normalized: an add-only name exists in either form.
	for _, route := range []string{"/kv/kv/keys/", "/files/public/objects/"} {
		body := any(`{"value":1}`)
		if strings.HasPrefix(route, "/files") {
			body = "x"
		}
		s.Must(t, "PUT", route+url.PathEscape("café"), body, alice, 200)
		s.Must(t, "PUT", route+url.PathEscape("café"), body, alice, 409)
	}
	s.Must(t, "PUT", "/files/public/objects/"+strings.Repeat("a/", 16)+"file", "", alice, 400)
	// Another writer's object and a missing one answer alike.
	for _, route := range []string{p + "a", "/kv/kv/keys/café"} {
		if a, b := s.Must(t, "GET", route, nil, bob, 404), s.Must(t, "GET", route+"-absent", nil, bob, 404); string(a.Body) != string(b.Body) {
			t.Fatal("object existence oracle")
		}
	}
	link := s.Must(t, "POST", "/files/files/download-link", `{"path":"a"}`, s.Key, 200).JSON(t)["url"].(string)
	u, _ := url.Parse(link)
	if r := s.A.At(t, "GET", rt.SiteDomain, u.RequestURI(), nil, nil); r.Status != 200 || string(r.Body) != "new" {
		t.Fatalf("owner link: %d %s", r.Status, r.Body)
	}
}

// insertKVRace runs the add-only save statement on its own connection.
func insertKVRace(h *datahttp.DataHandler, c datahttp.StorageCall, key, value, writer string) (bool, error) {
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

func readKV(t *testing.T, h *datahttp.DataHandler, c datahttp.StorageCall, key string) runtime.KVEntry {
	t.Helper()
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.StorageRuntimeDir(c), c.ResourceName), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	item, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func kvCall(t *testing.T, s Shop, resource string) datahttp.StorageCall {
	t.Helper()
	c := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: resource}
	var err error
	if c.Resource, err = s.A.Data.LoadStorageResource(context.Background(), s.Sid, resource); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStorageSecurityKVQuotaAndConcurrentCreate(t *testing.T) {
	s := NewShop(t)
	s.resource(t, "kv", "kv", RulesRecords)
	// Key bytes count toward the quota.
	t.Setenv("SITE_STORAGE_MAX_BYTES", "5")
	s.Must(t, "PUT", "/kv/kv/keys/long-key", `{"value":1}`, s.Callers["alice"], 507)
	s.Must(t, "PUT", "/kv/kv/keys/key", `{"value":1}`, s.Callers["alice"], 200)
	if u := s.Must(t, "GET", "/usage", nil, s.Key, 200).JSON(t); u["used_bytes"] != float64(4) {
		t.Fatalf("key bytes not counted: %v", u)
	}
	t.Setenv("SITE_STORAGE_MAX_BYTES", "1000000")
	// Concurrent creators of one add-only key: exactly one wins, unchanged.
	c := kvCall(t, s, "kv")
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []string
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			w := fmt.Sprintf("writer-%d", i)
			ok, err := insertKVRace(s.A.Data, c, "race", fmt.Sprint(i), w)
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				winners = append(winners, w)
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	item := readKV(t, s.A.Data, c, "race")
	if len(winners) != 1 || item.VisitorID != winners[0] || item.VisitorID != "writer-"+string(item.Value) {
		t.Fatalf("creators %v, stored %s/%s", winners, item.VisitorID, item.Value)
	}
	// An owner edit keeps the visitor's attribution.
	s.Must(t, "PUT", "/resources/kv", StorageRules("kv", RulesSignedInWrite), s.Key, 200)
	s.Must(t, "PUT", "/kv/kv/keys/key", `{"value":2}`, s.Callers["bob"], 200)
	before := readKV(t, s.A.Data, c, "key").VisitorID
	s.Must(t, "PUT", "/kv/kv/keys/key", `{"value":3}`, s.Key, 200)
	if after := readKV(t, s.A.Data, c, "key").VisitorID; before == "" || after != before {
		t.Fatalf("attribution %q -> %q", before, after)
	}
}

func TestStorageSecurityVisitorRateLimits(t *testing.T) {
	s := NewShop(t)
	alice, anon := s.Callers["alice"], s.Callers["anon"]
	s.resource(t, "kv", "kv", RulesOpenAdd)
	s.resource(t, "files", "files", RulesOpenAdd)
	s.A.Data.StorageVisitorLimiter = httpx.NewRateLimiter(1, 0)
	s.A.Data.StorageIPLimiter = httpx.NewRateLimiter(100, 0)
	s.Must(t, "PUT", "/kv/kv/keys/a", `{"value":1}`, alice, 200)
	s.Must(t, "PUT", "/files/files/objects/a", "x", alice, 429)
	s.Must(t, "GET", "/files/files/objects", nil, alice, 200) // reads are not limited
	s.Must(t, "PUT", "/kv/kv/keys/b", `{"value":1}`, s.Callers["bob"], 200)
	s.Must(t, "PUT", "/kv/kv/keys/c", `{"value":1}`, s.Key, 200)
	s.A.Data.StorageIPLimiter = httpx.NewRateLimiter(1, 0)
	s.Must(t, "PUT", "/kv/kv/keys/d", `{"value":1}`, anon, 200)
	s.Must(t, "PUT", "/files/files/objects/d", "x", anon, 429)
	s.Must(t, "PUT", "/files/files/objects/d", "x", s.Key, 200) // the owner is not limited
}

// Each site has its own write buckets: one site's limit never throttles another.
func TestStorageWriteBucketsPerSite(t *testing.T) {
	s := NewShop(t)
	s.resource(t, "kv", "kv", RulesSignedInAdd)
	otherID := s.A.Deploy(t, s.Owner, "other")
	otherHost := s.Owner.SiteHost("other")
	otherAlice := rt.Browser(otherHost, s.A.Session(t, rt.Person{ID: s.AliceID}, otherID, otherHost))
	if r := s.A.At(t, "PUT", rt.SiteDomain, "/v1/sites/other/storage/resources/kv", StorageRules("kv", RulesSignedInAdd), s.Key); r.Status != 201 {
		t.Fatalf("other resource: %d", r.Status)
	}
	for _, visitorCap := range []bool{false, true} {
		ip, visitor := 1.0, 100.0
		if visitorCap {
			ip, visitor = 100, 1
		}
		s.A.Data.StorageIPLimiter, s.A.Data.StorageVisitorLimiter = httpx.NewRateLimiter(ip, 0), httpx.NewRateLimiter(visitor, 0)
		key := fmt.Sprintf("key-%v", visitorCap)
		s.Must(t, "PUT", "/kv/kv/keys/"+key, `{"value":1}`, s.Callers["alice"], 200)
		s.Must(t, "PUT", "/kv/kv/keys/"+key+"-blocked", `{"value":1}`, s.Callers["alice"], 429)
		if r := s.A.At(t, "PUT", otherHost, "/v1/sites/other/storage/kv/kv/keys/"+key, `{"value":1}`, otherAlice); r.Status != 200 {
			t.Fatalf("one site's bucket throttled another: %d %s", r.Status, r.Body)
		}
	}
}

// Body reader signals the instant the handler starts reading, then waits.
type slowBody struct {
	entered, release chan struct{}
	once             sync.Once
	body             io.Reader
}

func (b *slowBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.body.Read(p)
}

// A slow request body never holds the site's write lock.
func TestStorageSlowBodiesDoNotHoldSiteLock(t *testing.T) {
	s := NewShop(t)
	s.resource(t, "db", "sqlite", RulesOpenAdd)
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT)")
	s.resource(t, "kv", "kv", RulesOpenAdd)
	for _, tc := range []struct {
		method, path, body string
		owner              bool
	}{{"POST", "/sqlite/db/tables/orders/rows", `{"item":"x"}`, false}, {"PUT", "/kv/kv/keys/slow", `{"value":1}`, false}, {"PUT", "/resources/db", StorageRules("sqlite", RulesOpenAdd), true}} {
		t.Run(tc.path, func(t *testing.T) {
			b := &slowBody{entered: make(chan struct{}), release: make(chan struct{}), body: strings.NewReader(tc.body)}
			req := httptest.NewRequest(tc.method, "https://"+s.Host+"/v1/sites/shop/storage"+tc.path, b)
			headers := s.Callers["anon"]
			if tc.owner {
				headers = s.Key
				req.Host = rt.SiteDomain
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			done := make(chan struct{})
			go func() { s.A.Srv.Config.Handler.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
			select {
			case <-b.entered:
			case <-time.After(2 * time.Second):
				close(b.release)
				t.Fatal("body not reached")
			}
			writes := make(chan struct{})
			go func() {
				s.Must(t, "PUT", "/kv/kv/keys/owner", `{"value":1}`, s.Key, 200)
				s.lockSite()() // a deploy takes the same lock
				close(writes)
			}()
			select {
			case <-writes:
			case <-time.After(1500 * time.Millisecond):
				t.Error("slow body blocked an owner write or a deploy")
			}
			close(b.release)
			<-done
		})
	}
}

// While a deploy holds the site lock, reads go on and a write waits a
// bounded time, then answers 503.
func TestStorageReadsAndBoundedLock(t *testing.T) {
	s := NewShop(t)
	s.resource(t, "db", "sqlite", RulesPublic)
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT)")
	s.resource(t, "kv", "kv", RulesOpenAdd)
	s.resource(t, "files", "files", RulesOpenAdd)
	s.Must(t, "PUT", "/files/files/objects/photo", "photo", s.Key, 200)
	unlock := sync.OnceFunc(s.lockSite())
	defer unlock()
	watchdog := time.AfterFunc(30*time.Second, unlock)
	defer watchdog.Stop()
	for _, route := range []string{"/sqlite/db/tables/orders/rows", "/kv/kv/keys", "/files/files/objects/photo"} {
		s.Must(t, "GET", route, nil, nil, 200)
	}
	start := time.Now()
	s.Must(t, "PUT", "/kv/kv/keys/blocked", `{"value":1}`, s.Callers["anon"], 503)
	if elapsed := time.Since(start); elapsed > s.A.Data.Limits.StorageWriteLockWait+3*time.Second {
		t.Fatalf("unbounded lock wait %v", elapsed)
	}
	if !watchdog.Stop() {
		t.Fatal("storage requests did not complete while the deploy lock was held")
	}
}

// A write waits for a short deploy to finish rather than failing.
func TestStorageWriteWaitsForDeploy(t *testing.T) {
	s := NewShop(t)
	s.resource(t, "kv", "kv", RulesOpenAdd)
	unlock := sync.OnceFunc(s.lockSite())
	defer unlock()
	timer := time.AfterFunc(150*time.Millisecond, unlock)
	defer timer.Stop()
	start := time.Now()
	s.Must(t, "PUT", "/kv/kv/keys/during-deploy", `{"value":1}`, s.Callers["anon"], 200)
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("write did not wait for deploy")
	}
}

// Wide tables and big results are refused; errors never echo the schema.
func TestStorageSecurityWideTablesAndGenericErrors(t *testing.T) {
	s := NewShop(t)
	alice := s.Callers["alice"]
	s.resource(t, "db", "sqlite", RulesRecords)
	cols := []string{"id INTEGER PRIMARY KEY"}
	for i := 0; i < 100; i++ {
		cols = append(cols, fmt.Sprintf("c%d TEXT", i))
	}
	s.schema(t, "CREATE TABLE wide("+strings.Join(cols, ",")+")")
	s.Must(t, "GET", "/sqlite/db/tables/wide/rows", nil, alice, 400)
	s.add(t, "wide", `{"c1":"x"}`, alice, 400)
	s.schema(t, "CREATE TABLE orders(id INTEGER PRIMARY KEY,item TEXT UNIQUE)")
	s.add(t, "orders", `{"item":"secret"}`, s.Callers["bob"], 200)
	a := s.add(t, "orders", `{"item":"secret"}`, alice, 409)
	b := s.add(t, "orders", `{"bad-column":1}`, alice, 400)
	if strings.Contains(string(a.Body), "orders") || strings.Contains(string(b.Body), "bad-column") {
		t.Fatal("schema reflected")
	}
	missing := s.Must(t, "GET", "/sqlite/db/tables/missing-sensitive-name/rows", nil, alice, 400)
	unknown := s.Must(t, "GET", "/sqlite/db/tables/orders/rows?order=unknown-sensitive-name", nil, alice, 400)
	if string(missing.Body) != string(unknown.Body) {
		t.Fatal("schema-specific error")
	}
	s.add(t, "orders", map[string]string{"item": strings.Repeat("a", 1000)}, alice, 200)
	t.Setenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES", "200")
	s.Must(t, "GET", "/sqlite/db/tables/orders/rows", nil, alice, 400)
}

// With every SQLite slot taken, owner and visitor reads get a quick 503 with
// Retry-After, and a waiting writer gives the site lock back.
func TestStorageSQLiteConcurrency(t *testing.T) {
	s := NewShop(t)
	s.resource(t, "db", "sqlite", RulesPublic)
	s.schema(t, "CREATE TABLE t(v)")
	var releases []func()
	for i := 0; i < cap(s.A.Data.Sqlite.Slots); i++ {
		release, err := s.A.Data.Sqlite.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for _, call := range []struct {
		method, path string
		body         any
		headers      map[string]string
	}{
		{"POST", "/sqlite/db/query", `{"sql":"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c"}`, s.Key},
		{"GET", "/sqlite/db/tables/t/rows", nil, s.Callers["anon"]},
	} {
		start := time.Now()
		r := s.Must(t, call.method, call.path, call.body, call.headers, 503)
		if r.Header.Get("Retry-After") == "" || time.Since(start) > s.A.Data.Limits.StorageAcquireWait+time.Second {
			t.Fatal("SQLite queue did not return a bounded retry response")
		}
	}
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO t VALUES(1)"}`, s.Key, 503)
	mu, _ := s.A.Data.UploadLocks.Load(s.OwnerID + "/shop")
	if !mu.(*sync.Mutex).TryLock() {
		t.Fatal("writer retained site lock")
	}
	mu.(*sync.Mutex).Unlock()
	releases[0]()
	releases = releases[1:]
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO t VALUES(1)"}`, s.Key, 200)
}

// New names are stored NFC; a pre-upgrade NFD name stays reachable,
// listable and deletable by its exact name.
func TestStorageLegacyNFDNames(t *testing.T) {
	s := NewShop(t)
	alice := s.Callers["alice"]
	s.resource(t, "photos", "files", RulesOpenAdd)
	s.resource(t, "kv", "kv", RulesOpenAdd)
	nfd, nfc := "café.txt", "café.txt"
	if r := s.Must(t, "PUT", "/files/photos/objects/"+url.PathEscape(nfd), "macOS", alice, 200); r.JSON(t)["path"] != nfc {
		t.Fatal("upload did not normalize new name")
	}
	if r := s.Must(t, "PUT", "/kv/kv/keys/"+url.PathEscape(nfd), `{"value":1}`, alice, 200); r.JSON(t)["key"] != nfc {
		t.Fatal("KV did not normalize new name")
	}
	if err := os.WriteFile(filepath.Join(s.A.Data.StorageFilesDir(datahttp.StorageCall{OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "photos"}), nfd), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := kvCall(t, s, "kv")
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(s.A.Data.StorageRuntimeDir(c), "kv"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.Exec("BEGIN"); err == nil {
		if err = runtime.RuntimeKVPut(conn, nfd, "2", "", false, 0); err == nil {
			err = conn.Exec("COMMIT; PRAGMA wal_checkpoint(TRUNCATE)")
		}
	}
	done()
	if err != nil {
		t.Fatal(err)
	}
	for route, field := range map[string]string{"/files/photos/objects": "path", "/kv/kv/keys": "key"} {
		s.Must(t, "GET", route+"/"+url.PathEscape(nfd), nil, s.Key, 200)
		if items := s.Must(t, "GET", route+"?limit=1", nil, s.Key, 200).JSON(t); items["items"].([]any)[0].(map[string]any)[field] != nfd {
			t.Fatal("legacy exact name absent from list")
		}
		s.Must(t, "DELETE", route+"/"+url.PathEscape(nfd), nil, s.Key, 200)
		s.Must(t, "GET", route+"/"+url.PathEscape(nfd), nil, s.Key, 404)
		s.Must(t, "GET", route+"/"+url.PathEscape(nfc), nil, s.Key, 200)
	}
}

// Own listings page past objects whose files are gone.
func TestStorageOwnListStalePagination(t *testing.T) {
	s := NewShop(t)
	alice := s.Callers["alice"]
	s.resource(t, "photos", "files", RulesRecords)
	for _, name := range []string{"a", "b", "c", "d"} {
		s.Must(t, "PUT", "/files/photos/objects/"+name, "x", alice, 200)
	}
	c := datahttp.StorageCall{OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "photos"}
	for _, name := range []string{"a", "b"} {
		if err := os.Remove(s.A.Data.StorageFilePath(c, name)); err != nil {
			t.Fatal(err)
		}
	}
	if r := s.Must(t, "GET", "/files/photos/objects?limit=2", nil, alice, 200).JSON(t); len(r["items"].([]any)) != 0 || r["next_after"] != "b" {
		t.Fatalf("stale rows ended paging: %v", r)
	}
	if r := s.Must(t, "GET", "/files/photos/objects?limit=2&after=b", nil, alice, 200).JSON(t); len(r["items"].([]any)) != 2 || r["next_after"] != "" {
		t.Fatalf("remaining objects lost: %v", r)
	}
}
