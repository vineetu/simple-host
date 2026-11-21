package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	model "github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
)

func growthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database := testDB(t)
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

// TestAPIMetricsWrapFlushesSSEStream is the adversarial-review regression
// test for the SIM-394 organiser assistant's SSE blocker: every /v1/
// request goes through APIMetrics.Wrap, whose StatusCapture wraps the
// real ResponseWriter. A handler's http.NewResponseController(w).Flush()
// (what hack_assistant.go now calls, instead of a direct w.(http.Flusher)
// assertion that StatusCapture would fail) must actually reach the
// client's socket -- not sit buffered until the handler returns. Proven
// over a real connection (httptest.NewServer, not NewRecorder, which
// cannot distinguish "flushed" from "buffered"): the client's first Read
// must succeed while the handler is still blocked waiting to write a
// second chunk.
func TestAPIMetricsWrapFlushesSSEStream(t *testing.T) {
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: one\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		<-release
		fmt.Fprint(w, "data: two\n\n")
	})
	m := NewAPIMetrics(operations.NewAPIMetrics(nil, nil))
	srv := httptest.NewServer(m.Wrap(h))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/stream-test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	readDone := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		readDone <- string(buf[:n])
	}()

	select {
	case got := <-readDone:
		if !strings.Contains(got, "one") {
			t.Fatalf("first chunk = %q, want it to contain %q", got, "one")
		}
	case <-time.After(2 * time.Second):
		// The handler is still blocked on <-Release, so this can only
		// mean the first write never reached the client -- Flush isn't
		// propagating through StatusCapture.
		t.Fatal("first delta never arrived while the handler was still writing -- SSE flush regression")
	}

	close(release)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if !strings.Contains(string(rest), "two") {
		t.Fatalf("second chunk missing from %q", rest)
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
	m := NewAPIMetrics(operations.NewAPIMetrics(database, nil))
	var today time.Time
	if err := database.QueryRow(`SELECT CURRENT_DATE`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	d := func(back int) string { return today.AddDate(0, 0, -back).Format("2006-01-02") }
	// Current 7 days: 10 today, 30 six days ago. Previous 7 days: 20 + 5.
	for _, r := range []struct {
		back int
		key  string
		n    int64
	}{{0, "deploy", 10}, {6, "data", 30}, {7, "deploy", 20}, {13, "auth", 5}} {
		if _, err := database.Exec(`INSERT INTO api_growth_daily (day, dim, key, calls) VALUES ($1::date, 'group', $2, $3)`, d(r.back), r.key, r.n); err != nil {
			t.Fatal(err)
		}
	}
	call := func(user *model.User, q string) *httptest.ResponseRecorder {
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
	if w := call(&model.User{ID: "u"}, ""); w.Code != http.StatusNotFound {
		t.Errorf("non-admin: %d", w.Code)
	}
	w := call(&model.User{ID: "u", IsAdmin: true}, "?range=7d")
	if w.Code != http.StatusOK {
		t.Fatalf("7d: %d %s", w.Code, w.Body)
	}
	var out operations.GrowthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.API.Total != 40 || out.API.Previous != 25 || out.API.ChangePct == nil || *out.API.ChangePct != 60 {
		t.Errorf("api total %d prev %d change %v, want 40 / 25 / 60", out.API.Total, out.API.Previous, out.API.ChangePct)
	}
}
