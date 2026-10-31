package papermetrics

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestClassify(t *testing.T) {
	c := New(nil, "", "simple-host.app")
	cases := []struct{ method, host, path, want string }{
		{"GET", "madurai-idly.vineetu.simple-host.app", "/", "site_file"},
		{"GET", "sites.simple-host.app", "/vineetu/x/app.js", "site_file"},
		{"GET", "brand.com", "/index.html", "site_file"},
		{"GET", "simple-host.app", "/", "platform_page"},
		{"GET", "www.simple-host.app", "/pricing", "platform_page"},
		{"GET", "x.vineetu.simple-host.app", "/v1/sites/x/storage/kv/a/keys/b", "data_read"},
		{"PUT", "simple-host.app", "/v1/sites/x/storage/kv/a/keys/b", "data_write"},
		{"POST", "simple-host.app", "/v1/sites/x/storage/sqlite/db/tables/t/rows", "data_write"},
		{"GET", "simple-host.app", "/v1/sites/x/state", "data_read"},
		{"GET", "simple-host.app", "/v1/sites/x/data", "data_read"},
		{"PUT", "simple-host.app", "/v1/sites/data/files", "api_write"}, // a site named "data"
		{"GET", "simple-host.app", "/v1/sites/x/me", "auth"},
		{"POST", "simple-host.app", "/v1/auth/email/code", "auth"},
		{"GET", "simple-host.app", "/oauth/authorize", "auth"},
		{"POST", "simple-host.app", "/mcp", "mcp"},
		{"GET", "simple-host.app", "/mcp", "mcp_stream"},
		{"GET", "simple-host.app", "/v1/sites", "api_read"},
		{"POST", "simple-host.app", "/v1/sites/x", "api_write"},
		{"GET", "simple-host.app", "/healthz", ""},
		{"OPTIONS", "simple-host.app", "/v1/sites", ""},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		if got := classNames[c.classify(r)]; got != tc.want {
			t.Errorf("%s %s%s = %q, want %q", tc.method, tc.host, tc.path, got, tc.want)
		}
	}
}

func TestPercentiles(t *testing.T) {
	// 100 requests, all in the (1 ms, 2.5 ms] bucket.
	h := map[int64]int64{2_500: 100}
	if got := pct(h, 0.5); got != 1.75 {
		t.Fatalf("p50 = %v, want 1.75", got)
	}
	h = map[int64]int64{50: 99, -1: 1}
	if got := pct(h, 0.999); got != 10_000 {
		t.Fatalf("overflow p99.9 = %v, want the 10 s floor", got)
	}
	if bucketFor(0) != 0 || bucketFor(51) != 1 || bucketFor(20_000_000) != numBuckets-1 {
		t.Fatal("bucketFor")
	}
}

// The wrapper must keep sendfile (ReadFrom) and flushing available.
func TestWrapKeepsFastPaths(t *testing.T) {
	c := New(nil, "", "simple-host.app")
	var sawReaderFrom, sawFlusher bool
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawReaderFrom = w.(io.ReaderFrom)
		_, sawFlusher = w.(http.Flusher)
		w.WriteHeader(503)
		io.Copy(w, strings.NewReader("hello"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://a.b.simple-host.app/", nil))
	if !sawReaderFrom || !sawFlusher || rec.Body.String() != "hello" {
		t.Fatalf("readerFrom=%v flusher=%v body=%q", sawReaderFrom, sawFlusher, rec.Body.String())
	}
	hs := &c.hist[classSiteFile]
	if hs.requests.Load() != 1 || hs.errors.Load() != 1 || hs.bytes.Load() != 5 {
		t.Fatalf("counted %d req %d err %d bytes", hs.requests.Load(), hs.errors.Load(), hs.bytes.Load())
	}
}

// BenchmarkWrap is the per-request overhead the paper's own numbers carry.
func BenchmarkWrap(b *testing.B) {
	c := New(nil, "", "simple-host.app")
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest("GET", "http://madurai-idly.vineetu.simple-host.app/menu.html", nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN not set")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("paper_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	d, err := sql.Open("postgres", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	ddl, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(string(ddl)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return d
}

func TestCollectAndExport(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	var uid, sid string
	if err := d.QueryRow(`INSERT INTO users (username) VALUES ('p') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO sites (user_id, name, site_url) VALUES ($1, 'menu', '') RETURNING id`, uid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{"current/index.html": "<p>hi</p>", "current/app.js": "x=1", "v1/index.html": "<p>hi</p>", "runtime/sqlite/db.sqlite": "0123456789"} {
		full := filepath.Join(dir, "by-id", uid, "menu", p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}

	c := New(d, dir, "simple-host.app")
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	for i := 0; i < 10; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://menu.p.simple-host.app/", nil))
	}
	c.flush(ctx)
	c.takeSample(ctx)
	c.lastSample.at = c.lastSample.at.Add(-time.Minute) // a second sample needs a gap
	c.takeSample(ctx)
	now := time.Now().UTC()
	if err := c.dailySnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := c.weeklyFootprint(ctx, now); err != nil {
		t.Fatal(err)
	}
	// Both snapshots are once only.
	if err := c.weeklyFootprint(ctx, now); err != nil {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM paper_site_footprint`).Scan(&n)
	if n != 1 {
		t.Fatalf("footprint rows = %d, want 1", n)
	}
	var live, js, sqlite int64
	var versions int
	d.QueryRow(`SELECT live_bytes, js_bytes, sqlite_bytes, versions FROM paper_site_footprint`).Scan(&live, &js, &sqlite, &versions)
	if live != 12 || js != 3 || sqlite != 10 || versions != 1 {
		t.Fatalf("footprint live=%d js=%d sqlite=%d versions=%d", live, js, sqlite, versions)
	}
	var missing int
	d.QueryRow(`SELECT count(*) FROM paper_daily`).Scan(&missing)
	if missing < len(dailyMetrics)-2 {
		t.Fatalf("daily metrics stored = %d of %d", missing, len(dailyMetrics))
	}

	type who struct{ signedIn, admin bool }
	admin := &who{true, true}
	get := func(q string, u *who) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/admin/paper-metrics?"+q, nil)
		w := httptest.NewRecorder()
		c.Export(func(*http.Request) (bool, bool) {
			if u == nil {
				return false, false
			}
			return u.signedIn, u.admin
		}).ServeHTTP(w, r)
		return w
	}
	if w := get("", &who{true, false}); w.Code != 404 {
		t.Fatalf("non-admin got %d", w.Code)
	}
	if w := get("", nil); w.Code != 401 {
		t.Fatalf("signed out got %d", w.Code)
	}
	w := get("set=latency&all=1", admin)
	var lat table
	if err := json.Unmarshal(w.Body.Bytes(), &lat); err != nil || len(lat.Rows) != 1 || lat.Rows[0][1] != "site_file" {
		t.Fatalf("latency: %v %s", err, w.Body.String())
	}
	w = get("set=footprint&format=csv", admin)
	recs, err := csv.NewReader(bytes.NewReader(w.Body.Bytes())).ReadAll()
	if err != nil || len(recs) != 2 || recs[0][3] != "live_bytes" || recs[1][3] != "12" {
		t.Fatalf("footprint csv: %v %q", err, w.Body.String())
	}
	w = get("", admin)
	var all map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	for _, s := range sortedSets() {
		if bytes.Contains(all[s], []byte(`"error"`)) {
			t.Errorf("summary set %s: %s", s, all[s])
		}
	}
}

// BenchmarkBare is the same request without the wrapper; the difference
// from BenchmarkWrap is the overhead.
func BenchmarkBare(b *testing.B) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	r := httptest.NewRequest("GET", "http://madurai-idly.vineetu.simple-host.app/menu.html", nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}
