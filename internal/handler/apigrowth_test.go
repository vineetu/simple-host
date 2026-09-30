package handler

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/geoip/geoiptest"
)

func TestAPIRouteGroup(t *testing.T) {
	for route, want := range map[string]string{
		"POST /mcp":                              "connector",
		"GET /mcp":                               "connector",
		"GET /v1/admin/users":                    "admin",
		"POST /v1/auth":                          "auth",
		"POST /v1/auth/verify":                   "auth",
		"GET /v1/auth/oauth/{provider}/callback": "auth",
		"GET /v1/visitor/establish":              "auth",
		"POST /v1/site-unlock":                   "auth",
		"POST /v1/sites/{sitename}/visitor/auth": "auth",
		"POST /v1/u/{handle}/sites/{sitename}/visitor/auth/verify": "auth",
		"GET /v1/me/keys":                                             "auth",
		"POST /v1/me/api-key/rotate":                                  "auth",
		"GET /v1/me":                                                  "other",
		"GET /v1/me/deleted-sites":                                    "other",
		"GET /v1/sites/{sitename}/state":                              "data",
		"PATCH /v1/u/{handle}/sites/{sitename}/state":                 "data",
		"POST /v1/sites/{sitename}/data/{coll}":                       "data",
		"GET /v1/sites/{sitename}/collections":                        "data",
		"GET /v1/u/{handle}/sites/{sitename}/me":                      "data",
		"PUT /v1/sites/{sitename}/savers":                             "data",
		"POST /v1/data-notify/stop":                                   "data",
		"GET /v1/sites":                                               "deploy",
		"PUT /v1/sites/{sitename}":                                    "deploy",
		"PUT /v1/sites/{sitename}/files":                              "deploy",
		"GET /v1/sites/{sitename}/versions/{version}/files/{path...}": "deploy",
		"GET /v1/sites/{sitename}/analytics":                          "deploy",
		"POST /v1/generate":                                           "deploy",
		"GET /v1/generate/status":                                     "deploy",
		"GET /v1/skills/{name}":                                       "other",
		"POST /v1/transcribe":                                         "other",
		"GET /":                                                       "other",
		"GET /v1/sites/{x}/state":                                     "data", // normalizeAPIPath fallback
		"GET /v1":                                                     "other",
	} {
		if got := apiRouteGroup(route); got != want {
			t.Errorf("apiRouteGroup(%q) = %q, want %q", route, got, want)
		}
	}
}

func TestGrowthWindow(t *testing.T) {
	today := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		rng                        string
		from, to, prevFrom, prevTo string
	}{
		{"7d", "2026-09-24", "2026-09-30", "2026-09-17", "2026-09-23"},
		{"14d", "2026-09-17", "2026-09-30", "2026-09-03", "2026-09-16"},
		{"30d", "2026-09-01", "2026-09-30", "2026-08-02", "2026-08-31"},
		{"6m", "2026-04-02", "2026-09-30", "2025-10-02", "2026-04-01"},
	}
	f := func(t time.Time) string { return t.Format("2006-01-02") }
	for _, c := range cases {
		w := newGrowthWindow(today, growthRanges[c.rng])
		if f(w.From) != c.from || f(w.To) != c.to || f(w.PrevFrom) != c.prevFrom || f(w.PrevTo) != c.prevTo {
			t.Errorf("%s: %s..%s prev %s..%s, want %s..%s prev %s..%s", c.rng, f(w.From), f(w.To), f(w.PrevFrom), f(w.PrevTo), c.from, c.to, c.prevFrom, c.prevTo)
		}
		if len(w.dates) != growthRanges[c.rng] || w.dates[0] != c.from || w.dates[len(w.dates)-1] != c.to {
			t.Errorf("%s: %d dates %v..", c.rng, len(w.dates), w.dates[:1])
		}
	}
	// A month boundary and a leap day stay contiguous.
	w := newGrowthWindow(time.Date(2028, 3, 1, 15, 4, 0, 0, time.UTC), 2)
	if w.dates[0] != "2028-02-29" || w.dates[1] != "2028-03-01" {
		t.Errorf("leap window = %v", w.dates)
	}
}

func TestChangePct(t *testing.T) {
	p := func(v *float64) any {
		if v == nil {
			return nil
		}
		return *v
	}
	for _, c := range []struct {
		cur, prev int64
		ok        bool
		want      any
	}{
		{134, 100, true, 34.0},
		{50, 100, true, -50.0},
		{1, 3, true, -66.7},
		{100, 100, true, 0.0},
		{5, 0, true, nil},
		{5, 4, false, nil},
	} {
		if got := p(changePct(c.cur, c.prev, c.ok)); got != c.want {
			t.Errorf("changePct(%d, %d, %v) = %v, want %v", c.cur, c.prev, c.ok, got, c.want)
		}
	}
}

func TestParseMCPLogLine(t *testing.T) {
	for line, want := range map[string][2]string{
		`203.0.113.9 - - [30/Sep/2026:00:00:03 +0000] "POST /mcp HTTP/1.1" 200 34 "-" "claude"`:                               {"203.0.113.9", "2026-09-30"},
		`2001:db8::1 - - [29/Sep/2026:23:59:59 +0000] "GET /mcp?x=1 HTTP/2.0" 401 0 "-" "-"`:                                  {"2001:db8::1", "2026-09-29"},
		`198.51.100.1 - - [30/Sep/2026:01:30:00 +0200] "DELETE /mcp HTTP/1.1" 204 0 "-" "-"`:                                  {"198.51.100.1", "2026-09-29"},
		`203.0.113.9 - - [30/Sep/2026:00:00:03 +0000] "OPTIONS /mcp HTTP/1.1" 204 0 "-" "-"`:                                  {},
		`203.0.113.9 - - [30/Sep/2026:00:00:03 +0000] "GET /.well-known/oauth-protected-resource/mcp HTTP/1.1" 200 0 "-" "-"`: {},
		`203.0.113.9 - - [30/Sep/2026:00:00:03 +0000] "GET /mcpx HTTP/1.1" 404 0 "-" "-"`:                                     {},
		`203.0.113.9 - - [30/Sep/2026:00:00:03 +0000] "GET /v1/sites HTTP/1.1" 200 0 "-" "-"`:                                 {},
		`garbage`: {},
		`203.0.113.9 - - [bad date] "POST /mcp HTTP/1.1" 200 0 "-" "-"`: {},
	} {
		ip, day, ok := parseMCPLogLine(line)
		if ok != (want[0] != "") || ip != want[0] || day != want[1] {
			t.Errorf("parseMCPLogLine(%q) = %q, %q, %v; want %q, %q", line, ip, day, ok, want[0], want[1])
		}
	}
}

// --- database tests (DB_DSN, db/schema.sql applied) ---

func growthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	if err := db.VerifySchema(context.Background(), database); err != nil {
		t.Fatalf("test database is behind the schema: %v", err)
	}
	clear := func() {
		for _, q := range []string{`DELETE FROM api_growth_daily`, `DELETE FROM api_request_daily`, `DELETE FROM api_ip_daily`, `DELETE FROM api_self_daily`} {
			if _, err := database.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
	clear()
	t.Cleanup(clear)
	return database
}

func growthTestGeo(t *testing.T) *geoip.DB {
	t.Helper()
	dir := t.TempDir()
	rec := func(iso, name string) geoiptest.Record {
		return geoiptest.Record{"country": geoiptest.Record{"iso_code": iso, "names": geoiptest.Record{"en": name}}}
	}
	if err := geoiptest.Write(filepath.Join(dir, geoip.CityFile), "DBIP-City-Lite", map[string]geoiptest.Record{
		"8.8.8.0/24":     rec("US", "United States"),
		"203.0.113.0/24": rec("JP", "Japan"),
		"2001:db8::/32":  rec("AU", "Australia"),
	}); err != nil {
		t.Fatal(err)
	}
	g := geoip.Open(dir)
	t.Cleanup(g.Close)
	return g
}

func growthRows(t *testing.T, database *sql.DB, day string) map[string]int64 {
	t.Helper()
	rows, err := database.Query(`SELECT dim || ':' || key, calls FROM api_growth_daily WHERE day = $1::date`, day)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

func sameCounts(t *testing.T, what string, got, want map[string]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v, want %v", what, got, want)
			return
		}
	}
}

// The live counter feeds both sets of tables; the backfill adds only what the
// growth table lacks, so the upgrade day gets exactly its pre-upgrade calls
// and a second run adds nothing.
func TestGrowthBackfillFromTrafficTables(t *testing.T) {
	database := growthTestDB(t)
	m := NewAPIGrowthBackfiller(database, growthTestGeo(t))
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// A day before the upgrade: only the traffic tables know it.
	exec(`INSERT INTO api_request_daily (day, route, status, calls) VALUES
		('2026-09-01', 'PUT /v1/sites/{sitename}/files', 200, 5),
		('2026-09-01', 'PUT /v1/sites/{sitename}/files', 500, 1),
		('2026-09-01', 'GET /v1/sites/{sitename}/state', 200, 3),
		('2026-09-01', 'POST /v1/auth', 200, 2)`)
	exec(`INSERT INTO api_ip_daily (day, ip, calls) VALUES
		('2026-09-01', '8.8.8.0', 6), ('2026-09-01', '203.0.113.0', 3), ('2026-09-01', '127.0.0.1', 1), ('2026-09-01', '192.0.2.0', 1)`)
	// The upgrade day: 4 calls before the new build (traffic tables only),
	// then 2 counted live into both.
	exec(`INSERT INTO api_request_daily (day, route, status, calls) VALUES ('2026-09-02', 'GET /v1/sites', 200, 6)`)
	exec(`INSERT INTO api_ip_daily (day, ip, calls) VALUES ('2026-09-02', '8.8.8.0', 6)`)
	exec(`INSERT INTO api_growth_daily (day, dim, key, calls) VALUES ('2026-09-02', 'group', 'deploy', 2), ('2026-09-02', 'country', 'US', 2)`)

	added, err := m.BackfillGrowth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if added != 11+11+4+4 {
		t.Errorf("added %d, want 30", added)
	}
	sameCounts(t, "2026-09-01", growthRows(t, database, "2026-09-01"), map[string]int64{
		"group:deploy": 6, "group:data": 3, "group:auth": 2,
		"country:US": 6, "country:JP": 3, "country:XX": 2,
	})
	sameCounts(t, "2026-09-02", growthRows(t, database, "2026-09-02"), map[string]int64{"group:deploy": 6, "country:US": 6})

	again, err := m.BackfillGrowth(ctx)
	if err != nil || again != 0 {
		t.Fatalf("second run added %d (err %v), want 0", again, err)
	}
}

// /mcp calls from old nginx logs fill days with no connector calls, and only
// those; plain and gzipped files both count.
func TestGrowthBackfillFromLogs(t *testing.T) {
	database := growthTestDB(t)
	m := NewAPIGrowthBackfiller(database, growthTestGeo(t))
	dir := t.TempDir()
	plain := filepath.Join(dir, "access.log")
	if err := os.WriteFile(plain, []byte(
		`8.8.8.8 - - [03/Sep/2026:10:00:00 +0000] "POST /mcp HTTP/1.1" 200 10 "-" "x"
8.8.8.9 - - [03/Sep/2026:11:00:00 +0000] "POST /mcp HTTP/1.1" 200 10 "-" "x"
203.0.113.5 - - [03/Sep/2026:11:00:00 +0000] "OPTIONS /mcp HTTP/1.1" 204 0 "-" "x"
203.0.113.5 - - [03/Sep/2026:12:00:00 +0000] "GET /v1/sites HTTP/1.1" 200 10 "-" "x"
8.8.8.8 - - [04/Sep/2026:10:00:00 +0000] "POST /mcp HTTP/1.1" 200 10 "-" "x"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	gzPath := filepath.Join(dir, "access.log.2.gz")
	f, _ := os.Create(gzPath)
	zw := gzip.NewWriter(f)
	zw.Write([]byte(`203.0.113.7 - - [02/Sep/2026:09:00:00 +0000] "POST /mcp HTTP/1.1" 200 10 "-" "x"
2001:db8::5 - - [02/Sep/2026:09:00:00 +0000] "POST /mcp HTTP/1.1" 200 10 "-" "x"
`))
	zw.Close()
	f.Close()
	// 4 Sep already has live connector counts: the log must not add to it.
	if _, err := database.Exec(`INSERT INTO api_growth_daily (day, dim, key, calls) VALUES ('2026-09-04', 'group', 'connector', 7)`); err != nil {
		t.Fatal(err)
	}

	added, err := m.BackfillGrowthFromLogs(context.Background(), []string{plain, gzPath})
	if err != nil {
		t.Fatal(err)
	}
	if added != 4 {
		t.Errorf("added %d, want 4", added)
	}
	sameCounts(t, "2 Sep", growthRows(t, database, "2026-09-02"), map[string]int64{"group:connector": 2, "country:JP": 1, "country:AU": 1})
	sameCounts(t, "3 Sep", growthRows(t, database, "2026-09-03"), map[string]int64{"group:connector": 2, "country:US": 2})
	sameCounts(t, "4 Sep", growthRows(t, database, "2026-09-04"), map[string]int64{"group:connector": 7})
	if again, err := m.BackfillGrowthFromLogs(context.Background(), []string{plain, gzPath}); err != nil || again != 0 {
		t.Fatalf("second run added %d (err %v)", again, err)
	}
}

// The live path: record() then flush() lands in api_growth_daily for today.
func TestGrowthLiveFlush(t *testing.T) {
	database := growthTestDB(t)
	m := NewAPIGrowthBackfiller(database, growthTestGeo(t))
	m.routes, m.ips = map[routeKey]int64{}, map[string]*ipAgg{}
	m.record("POST /mcp", 200, "8.8.8.0", m.countryOf("8.8.8.0"))
	m.record("PUT /v1/sites/{sitename}/files", 200, "203.0.113.0", m.countryOf("203.0.113.0"))
	m.record("GET /v1/sites/{sitename}/state", 200, "127.0.0.1", m.countryOf("127.0.0.1"))
	m.flush()
	var today string
	database.QueryRow(`SELECT CURRENT_DATE::text`).Scan(&today)
	sameCounts(t, "today", growthRows(t, database, today), map[string]int64{
		"group:connector": 1, "group:deploy": 1, "group:data": 1,
		"country:US": 1, "country:JP": 1, "country:XX": 1,
	})
	// The traffic tables got the same calls, so a backfill adds nothing.
	if n, err := m.BackfillGrowth(context.Background()); err != nil || n != 0 {
		t.Fatalf("backfill after live flush added %d (err %v)", n, err)
	}
}

func TestAdminGrowthEndpoint(t *testing.T) {
	database := growthTestDB(t)
	m := NewAPIGrowthBackfiller(database, nil)
	m.routes, m.ips = map[routeKey]int64{}, map[string]*ipAgg{}
	var today time.Time
	if err := database.QueryRow(`SELECT CURRENT_DATE`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	d := func(back int) string { return today.AddDate(0, 0, -back).Format("2006-01-02") }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Current 7 days: 10 today, 30 six days ago. Previous 7 days: 20 + 5.
	// Eight days before that is outside both and must not count.
	for _, r := range []struct {
		back     int
		dim, key string
		n        int64
	}{
		{0, "group", "deploy", 6}, {0, "group", "connector", 4}, {0, "country", "US", 7}, {0, "country", "XX", 3},
		{6, "group", "data", 30}, {6, "country", "JP", 30},
		{7, "group", "deploy", 20}, {13, "group", "auth", 5},
		{14, "group", "deploy", 99}, {14, "country", "US", 99},
	} {
		exec(`INSERT INTO api_growth_daily (day, dim, key, calls) VALUES ($1::date, $2, $3, $4)`, d(r.back), r.dim, r.key, r.n)
	}
	// Accounts and sites, marked so the test counts only its own rows.
	var before7, before14 int64
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE created_at >= $1::date`, d(6)).Scan(&before7)
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE created_at >= $1::date AND created_at < $2::date`, d(13), d(6)).Scan(&before14)
	var uid string
	if err := database.QueryRow(`INSERT INTO users (username, created_at) VALUES ('growth-t1', $1::date + interval '3 hours') RETURNING id`, d(2)).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO users (username, created_at) VALUES ('growth-t2', $1::date + interval '1 hour')`, d(9))
	t.Cleanup(func() { database.Exec(`DELETE FROM users WHERE username IN ('growth-t1', 'growth-t2')`) })

	call := func(user *db.User, q string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/admin/growth"+q, nil)
		if user != nil {
			r = r.WithContext(auth.WithUser(r.Context(), user))
		}
		w := httptest.NewRecorder()
		m.AdminGrowth(w, r)
		return w
	}
	if w := call(nil, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := call(&db.User{ID: uid}, ""); w.Code != http.StatusNotFound {
		t.Errorf("non-admin: %d", w.Code)
	}
	admin := &db.User{ID: uid, IsAdmin: true}
	if w := call(admin, "?range=3y"); w.Code != http.StatusBadRequest {
		t.Errorf("bad range: %d", w.Code)
	}
	w := call(admin, "?range=7d")
	if w.Code != http.StatusOK {
		t.Fatalf("7d: %d %s", w.Code, w.Body)
	}
	var out growthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.From != d(6) || out.To != d(0) || len(out.Dates) != 7 || out.Days != 7 {
		t.Errorf("window %s..%s (%d dates)", out.From, out.To, len(out.Dates))
	}
	if out.API.Total != 40 || out.API.Previous != 25 {
		t.Errorf("api total %d prev %d, want 40 / 25", out.API.Total, out.API.Previous)
	}
	if out.API.ChangePct == nil || *out.API.ChangePct != 60 {
		t.Errorf("api change %v, want 60", out.API.ChangePct)
	}
	if out.API.Daily[6] != 10 || out.API.Daily[0] != 30 || out.API.Groups["connector"][6] != 4 || out.API.Groups["data"][0] != 30 {
		t.Errorf("api daily %v groups %v", out.API.Daily, out.API.Groups)
	}
	if len(out.Countries) != 2 || out.Countries[0].Code != "JP" || out.Countries[0].Calls != 30 || out.Countries[0].Name != "Japan" || out.Countries[1].Calls != 7 || out.UnknownCalls != 3 {
		t.Errorf("countries %+v unknown %d", out.Countries, out.UnknownCalls)
	}
	if out.TrackingSince == nil || *out.TrackingSince != d(14) {
		t.Errorf("tracking since %v", out.TrackingSince)
	}
	if out.Users.Total != before7+1 || out.Users.Previous != before14+1 || out.Users.Daily[4] < 1 {
		t.Errorf("users total %d prev %d daily %v", out.Users.Total, out.Users.Previous, out.Users.Daily)
	}

	// 14 days: the previous period starts before counting did, so no change.
	w = call(admin, "?range=14d")
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.API.Total != 65 || out.API.ChangePct != nil {
		t.Errorf("14d total %d change %v, want 65 / nil", out.API.Total, out.API.ChangePct)
	}

	// Cached for a minute: a new row does not show until then.
	exec(`INSERT INTO api_growth_daily (day, dim, key, calls) VALUES ($1::date, 'group', 'other', 1000)`, d(0))
	w = call(admin, "?range=7d")
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.API.Total != 40 {
		t.Errorf("cached total %d, want 40", out.API.Total)
	}
}

// Calls from this server land in api_self_daily and the growth "source" count,
// never in the calls, errors or growth kinds; the admin endpoints show them
// apart. A backfill adds nothing for them.
func TestSelfCallsCountedApart(t *testing.T) {
	database := growthTestDB(t)
	m := NewAPIGrowthBackfiller(database, growthTestGeo(t))
	m.record("GET /v1/sites", 200, "8.8.8.0", m.countryOf("8.8.8.0"))
	m.recordSelf("GET /v1/sites/{sitename}/state", 403)
	m.recordSelf("GET /v1/sites/{sitename}/state", 200)
	m.flush()

	admin := &db.User{ID: "x", IsAdmin: true}
	r := httptest.NewRequest("GET", "/v1/admin/api-analytics", nil).WithContext(auth.WithUser(context.Background(), admin))
	w := httptest.NewRecorder()
	m.AdminSummary(w, r)
	var sum apiAnalyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if sum.CallsToday != 1 || sum.ErrorsToday != 0 || sum.SelfToday != 2 || sum.SelfErrorsToday != 1 || sum.IPsToday != 1 {
		t.Errorf("summary %+v", sum)
	}

	r = httptest.NewRequest("GET", "/v1/admin/growth?range=7d", nil).WithContext(auth.WithUser(context.Background(), admin))
	w = httptest.NewRecorder()
	m.AdminGrowth(w, r)
	var g growthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if g.API.Total != 1 || g.SelfCalls != 2 || g.UnknownCalls != 0 {
		t.Errorf("growth total %d self %d unknown %d, want 1 / 2 / 0", g.API.Total, g.SelfCalls, g.UnknownCalls)
	}
	if n, err := m.BackfillGrowth(context.Background()); err != nil || n != 0 {
		t.Fatalf("backfill added %d (err %v)", n, err)
	}
}

// v079 takes requests that matched no API route out of the traffic table and
// out of the growth kind they were counted in, and a second run changes
// nothing.
func TestMigrationDropsUnmatchedRequests(t *testing.T) {
	database := growthTestDB(t)
	sqlText, err := os.ReadFile("../../db/migrations/v079-api-self-calls.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO api_request_daily (day, route, status, calls) VALUES
		('2026-09-01', 'GET /', 404, 378),
		('2026-09-01', 'GET /', 301, 2),
		('2026-09-01', 'POST /v1/graphql', 405, 16),
		('2026-09-01', 'PUT /v1/u/{x}/sites/{x}/files', 405, 2),
		('2026-09-01', 'GET /v1/sites', 200, 50),
		('2026-09-01', 'PUT /v1/sites/{sitename}/files', 404, 16),
		('2026-09-01', 'POST /mcp', 405, 1)`)
	exec(`INSERT INTO api_growth_daily (day, dim, key, calls) VALUES
		('2026-09-01', 'group', 'other', 396), ('2026-09-01', 'group', 'deploy', 68),
		('2026-09-01', 'group', 'connector', 1), ('2026-09-01', 'country', 'US', 465)`)
	for i := 0; i < 2; i++ {
		exec(string(sqlText))
	}
	var routes int
	database.QueryRow(`SELECT COUNT(*) FROM api_request_daily`).Scan(&routes)
	if routes != 3 {
		t.Errorf("%d route rows left, want 3 (GET /v1/sites, the PUT 404, POST /mcp)", routes)
	}
	sameCounts(t, "growth", growthRows(t, database, "2026-09-01"), map[string]int64{
		"group:other": 0, "group:deploy": 66, "group:connector": 1, "country:US": 465,
	})
	m := NewAPIGrowthBackfiller(database, nil)
	if n, err := m.BackfillGrowth(context.Background()); err != nil || n != 0 {
		t.Fatalf("backfill after cleanup added %d (err %v)", n, err)
	}
}
