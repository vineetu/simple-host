package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

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
func TestIsAPIRoute(t *testing.T) {
	for p, want := range map[string]bool{
		"GET /v1/sites/{sitename}/versions": true,
		"/v1/sites":                         false, // no method: a redirect's literal path
		"POST /mcp":                         true,
		"/mcp":                              false,
		"GET example.com/v1/sites":          true,
		"GET /":                             false, // the site catch-all a bot's /v1/<junk> lands on
		"/":                                 false,
		"":                                  false, // nothing matched, or not for this method
		"GET /features":                     false,
		"GET /mcpx":                         false,
	} {
		if got := isAPIRoute(p); got != want {
			t.Errorf("isAPIRoute(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestAPIMetricsWrapCountsOnlyOutsideAPICalls(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sites/{sitename}/versions", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	database := growthTestDB(t)
	m := NewAPIMetrics(operations.NewAPIMetrics(database, nil))
	m.SetRouting(mux, "147.224.49.228")
	// A middleware that clones the request (as the connector's bearer
	// exchange does), so the outer request never sees the pattern.
	cloning := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mux.ServeHTTP(w, r.Clone(r.Context())) })
	h := m.Wrap(cloning)
	call := func(method, path, from string) {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", from)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	call("GET", "/v1/wp-login.php", "8.8.8.8")         // catch-all 404
	call("POST", "/v1/sites/blog/versions", "8.8.8.8") // 405: no route for POST
	call("GET", "/v1/sites/blog/versions", "8.8.8.8")
	call("GET", "/v1/sites/blog/versions", "147.224.49.228")
	call("GET", "/v1/sites/blog/versions", "127.0.0.1")
	call("GET", "/v1/sites/blog/versions", "::1")
	call("GET", "/v1/sites/blog/versions", "10.0.0.5")

	sum, err := m.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.CallsToday != 2 || sum.SelfToday != 3 || sum.IPsToday != 2 {
		t.Fatalf("summary: %+v", sum)
	}
	var n int64
	if err := database.QueryRow(`SELECT calls FROM api_growth_daily WHERE day=CURRENT_DATE AND dim='source' AND key='self'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("self calls=%d err=%v", n, err)
	}

}

func TestUploadMetricsUsePattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/uploads/{token}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	database := growthTestDB(t)
	m := NewAPIMetrics(operations.NewAPIMetrics(database, nil))
	m.SetRouting(mux)
	r := httptest.NewRequest("PUT", "/v1/uploads/private-test-token", nil)
	r.RemoteAddr = "203.0.113.8:1234"
	m.Wrap(mux).ServeHTTP(httptest.NewRecorder(), r)
	pattern := "PUT /v1/uploads/{token}"
	sum, err := m.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Routes) != 1 || sum.Routes[0].Route != pattern || sum.Routes[0].Today != 1 {
		t.Fatalf("upload routes: %+v", sum.Routes)
	}
	for _, ip := range sum.IPs {
		if ip.LastPath != pattern {
			t.Fatal("token recorded in metrics")
		}
	}

}
func TestAdminGrowthEndpoint(t *testing.T) {
	database := growthTestDB(t)
	m := NewAPIMetrics(operations.NewAPIGrowthBackfiller(database, nil))
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
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE EXISTS (SELECT 1 FROM sites s WHERE s.user_id=users.id) AND created_at >= $1::date`, d(6)).Scan(&before7)
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE EXISTS (SELECT 1 FROM sites s WHERE s.user_id=users.id) AND created_at >= $1::date AND created_at < $2::date`, d(13), d(6)).Scan(&before14)
	var uid string
	if err := database.QueryRow(`INSERT INTO users (username, created_at) VALUES ('growth-t1', $1::date + interval '3 hours') RETURNING id`, d(2)).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO users (username, created_at) VALUES ('growth-t2', $1::date + interval '1 hour')`, d(9))
	exec(`INSERT INTO sites(user_id,name) SELECT id,'growth-site' FROM users WHERE username IN ('growth-t1','growth-t2')`)
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
	var out operations.GrowthResponse
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
