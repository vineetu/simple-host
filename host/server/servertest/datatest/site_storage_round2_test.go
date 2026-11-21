package datatest

// Deterministic regression assertions ported from the final storage2 review
// PoCs: gallery, owner/anonymous CPU queries and stranded pre-upgrade NFD files.
import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestStorageRound2Gallery150POC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "photos", "files", "anyone", "owner", "full")
	for i := 0; i < 150; i++ {
		s.call(t, "PUT", fmt.Sprintf("/files/photos/objects/p%03d.webp", i), "RIFF....WEBP", s.key, 200)
	}
	for _, headers := range []map[string]string{servertest.Browser(s.host, ""), s.alice} {
		for i := 0; i < 150; i++ {
			s.call(t, "GET", fmt.Sprintf("/files/photos/objects/p%03d.webp", i), nil, headers, 200)
		}
	}
	s.call(t, "HEAD", "/files/photos/objects/p000.webp", nil, s.alice, 200)
}

func TestStorageRound2WriteBucketsPerSite(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "kv", "kv", "anyone", "signed-in", "add")
	owner := servertest.Person{Key: s.key["X-API-Key"]}
	if err := s.a.Database.QueryRow(`SELECT username FROM users WHERE id=$1`, s.ownerID).Scan(&owner.Email); err != nil {
		t.Fatal(err)
	}
	s.a.Deploy(t, owner, "other")
	otherHost := strings.Replace(s.host, "shop.", "other.", 1)
	otherID := s.a.SiteID(t, owner, "other")
	var aliceID string
	// Resolve Alice exactly; both users have sessions in the original site.
	sess, ok := s.a.Access.StrictVisitorSession(func() *http.Request {
		r := httptest.NewRequest("GET", "https://"+s.host+"/", nil)
		for k, v := range s.alice {
			r.Header.Set(k, v)
		}
		return r
	}(), s.siteID)
	if !ok {
		t.Fatal("Alice session missing")
	}
	aliceID = sess.UserID
	var aliceEmail string
	if err := s.a.Database.QueryRow(`SELECT username FROM users WHERE id=$1`, aliceID).Scan(&aliceEmail); err != nil {
		t.Fatal(err)
	}
	otherAlice := servertest.Browser(otherHost, s.a.Session(t, servertest.Person{Email: aliceEmail}, otherID, otherHost))
	r := s.a.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/other/storage/resources/kv", servertest.StorageRules("kv", servertest.RulesSignedInAdd), s.key)
	if r.Status != 201 {
		t.Fatalf("other resource: %d", r.Status)
	}
	for _, visitorCap := range []bool{false, true} {
		s.a.Data.StorageIPLimiter = httpx.NewRateLimiter(1, 0)
		s.a.Data.StorageVisitorLimiter = httpx.NewRateLimiter(100, 0)
		if visitorCap {
			s.a.Data.StorageIPLimiter = httpx.NewRateLimiter(100, 0)
			s.a.Data.StorageVisitorLimiter = httpx.NewRateLimiter(1, 0)
		}
		key := fmt.Sprintf("key-%v", visitorCap)
		s.call(t, "PUT", "/kv/kv/keys/"+key, `{"value":1}`, s.alice, 200)
		s.call(t, "PUT", "/kv/kv/keys/"+key+"-blocked", `{"value":1}`, s.alice, 429)
		r = s.a.At(t, "PUT", otherHost, "/v1/sites/other/storage/kv/kv/keys/"+key, `{"value":1}`, otherAlice)
		if r.Status != 200 {
			t.Fatalf("one site's %v bucket throttled another: %d %s", visitorCap, r.Status, r.Body)
		}
	}
}

func TestStorageRound2SQLiteConcurrencyPOC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "db", "sqlite", "anyone", "owner", "full")
	s.schema(t, "CREATE TABLE t(v)")
	// Occupy every slot of this handler's own gate, independent of this
	// site/identity. s.a.sites is the SiteHandler under test, so this must be
	// the same gate instance its storage routes acquire from.
	initRelease, err := s.a.Data.Sqlite.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	initRelease()
	var releases []func()
	for i := 0; i < cap(s.a.Data.Sqlite.Slots); i++ {
		release, err := s.a.Data.Sqlite.Acquire(context.Background())
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
	// Same recursive query as the review. It must be refused before execution,
	// for both owner and anonymous callers, with a short bounded wait.
	// Pages read through the table rows routes (page SQL is refused).
	for _, call := range []struct {
		method, path, body string
		headers            map[string]string
	}{
		{"POST", "/sqlite/db/query", `{"sql":"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT count(*) FROM c"}`, s.key},
		{"GET", "/sqlite/db/tables/t/rows", "", servertest.Browser(s.host, "")},
	} {
		start := time.Now()
		var body any
		if call.body != "" {
			body = call.body
		}
		r := s.call(t, call.method, call.path, body, call.headers, 503)
		if r.Header.Get("Retry-After") == "" || time.Since(start) > s.a.Sites.Limits.StorageAcquireWait+time.Second {
			t.Fatal("SQLite queue did not return bounded retry response")
		}
	}
	// A writer waiting for a slot releases its write lock on 503.
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO t VALUES(1)"}`, s.key, 503)
	mu, _ := s.a.Sites.UploadLocks.Load(s.ownerID + "/shop")
	if !mu.(*sync.Mutex).TryLock() {
		t.Fatal("writer retained site lock")
	}
	mu.(*sync.Mutex).Unlock()
	releases[0]()
	releases = releases[1:]
	s.call(t, "POST", "/sqlite/db/query", `{"sql":"SELECT 1"}`, s.key, 200)
	s.call(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO t VALUES(1)"}`, s.key, 200)
}

func TestStorageRound2OwnerTimeoutAndDeployWait(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "kv", "kv", "anyone", "anyone", "full")
	release := s.a.Sites.LockSite(s.ownerID, "shop")
	var once sync.Once
	unlock := func() { once.Do(release) }
	defer unlock()
	timer := time.AfterFunc(150*time.Millisecond, unlock)
	defer timer.Stop()
	start := time.Now()
	s.call(t, "PUT", "/kv/kv/keys/during-deploy", `{"value":1}`, servertest.Browser(s.host, ""), 200)
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("write did not wait for deploy")
	}
}

func TestStorageRound2LegacyNFDNamesPOC(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "photos", "files", "anyone", "anyone", "full")
	s.resource(t, "kv", "kv", "anyone", "anyone", "full")
	nfd, nfc := "cafe\u0301.txt", "café.txt"
	r := s.call(t, "PUT", "/files/photos/objects/"+url.PathEscape(nfd), "macOS", s.alice, 200)
	if r.JSON(t)["path"] != nfc {
		t.Fatal("upload did not normalize new name")
	}
	s.call(t, "GET", "/files/photos/objects/"+url.PathEscape(nfc), nil, nil, 200)
	r = s.call(t, "PUT", "/kv/kv/keys/"+url.PathEscape(nfd), `{"value":1}`, s.alice, 200)
	if r.JSON(t)["key"] != nfc {
		t.Fatal("KV did not normalize new name")
	}
	c := datahttp.StorageCall{OwnerID: s.ownerID, SiteName: "shop", ResourceName: "photos"}
	if err := os.WriteFile(filepath.Join(s.a.Data.StorageFilesDir(c), nfd), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	servertest.SeedStorageKV(t, s.a.Data, s.siteID, "kv", nfd, "2", "")
	for _, kind := range []string{"files", "kv"} {
		route := "/files/photos/objects/"
		field := "path"
		list := strings.TrimSuffix(route, "/")
		if kind == "kv" {
			route = "/kv/kv/keys/"
			field = "key"
			list = strings.TrimSuffix(route, "/")
		}
		s.call(t, "GET", route+url.PathEscape(nfd), nil, s.key, 200)
		items := s.call(t, "GET", list+"?limit=1", nil, s.key, 200).JSON(t)
		if items["items"].([]any)[0].(map[string]any)[field] != nfd {
			t.Fatal("legacy exact name absent from list")
		}
		cursor := items["next_after"].(string)
		s.call(t, "GET", list+"?after="+url.QueryEscape(cursor), nil, s.key, 200)
		s.call(t, "DELETE", route+url.PathEscape(nfd), nil, s.key, 200)
		s.call(t, "GET", route+url.PathEscape(nfd), nil, s.key, 404)
		s.call(t, "GET", route+url.PathEscape(nfc), nil, s.key, 200)
	}
}

func TestStorageRound2OwnListStalePagination(t *testing.T) {
	s := newSecurityShop(t)
	s.resource(t, "photos", "files", "own", "signed-in", "add")
	for _, name := range []string{"a", "b", "c", "d"} {
		s.call(t, "PUT", "/files/photos/objects/"+name, "x", s.alice, 200)
	}
	c := datahttp.StorageCall{OwnerID: s.ownerID, SiteName: "shop", ResourceName: "photos"}
	for _, name := range []string{"a", "b"} {
		if err := os.Remove(s.a.Data.StorageFilePath(c, name)); err != nil {
			t.Fatal(err)
		}
	}
	r := s.call(t, "GET", "/files/photos/objects?limit=2", nil, s.alice, 200).JSON(t)
	if len(r["items"].([]any)) != 0 || r["next_after"] != "b" {
		t.Fatalf("stale rows ended paging: %v", r)
	}
	r = s.call(t, "GET", "/files/photos/objects?limit=2&after=b", nil, s.alice, 200).JSON(t)
	if len(r["items"].([]any)) != 2 || r["next_after"] != "" {
		t.Fatalf("remaining objects lost: %v", r)
	}
}
