package signin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// Account sign-in (emailed code; Google is the gate's) is served only on the app's own
// address; on a site's, a person's or a connected address it answers a plain
// 403 that points at visitor sign-in, and a page on a site cannot reach the
// apex routes cross-origin either.
func TestAccountAuthOnlyOnAppAddress(t *testing.T) {
	mux := http.NewServeMux()
	identity := func(h http.Handler) http.Handler { return h }
	New(nil, accountapp.New(nil), nil, "https://simple-host.app", nil, httpx.NewRateLimiter(100, 100), httpx.NewRateLimiter(100, 100), nil, &httpx.Caches{}).Register(mux, identity, identity)
	app := web.CORS(mux)

	routes := []struct{ method, path string }{
		{"POST", "/v1/auth"}, {"POST", "/v1/auth/verify"},
	}
	hosts := []string{"shop.alice.simple-host.app", "shop.example.org", "sites.simple-host.app", "simple-host.app.evil.example"}
	for _, rt := range routes {
		for _, host := range hosts {
			req := httptest.NewRequest(rt.method, "https://"+host+rt.path, strings.NewReader(`{"email":"customer@example.com","code":"123456"}`))
			req.Header.Set("Origin", "https://"+host)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusForbidden || !strings.Contains(body, `"code":"account_auth_unavailable"`) {
				t.Fatalf("%s %s on %s: %d %s", rt.method, rt.path, host, rec.Code, body)
			}
			if len(rec.Result().Cookies()) != 0 || strings.Contains(body, "api_key") {
				t.Fatalf("%s on %s issued a credential", rt.path, host)
			}
		}
		// The apex, called cross-origin from a hosted page: refused too.
		req := httptest.NewRequest(rt.method, "https://simple-host.app"+rt.path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://shop.alice.simple-host.app")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s from a site origin: %d %s", rt.path, rec.Code, rec.Body.String())
		}
	}

	// The Origin must be exactly the app's origin: another scheme or port on
	// the same host is foreign too.
	for _, origin := range []string{"http://simple-host.app", "https://simple-host.app.evil.example"} {
		req := httptest.NewRequest("POST", "https://simple-host.app/v1/auth", strings.NewReader(`{}`))
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("origin %q on the apex: %d", origin, rec.Code)
		}
	}

	// The app's own address reaches the handlers: an agent (no Origin), the
	// dashboard (same origin), a proxied port and a bare local run (loopback)
	// all get the ordinary 400 for a malformed body, never the 403.
	for _, c := range []struct{ host, origin string }{
		{"simple-host.app", ""}, {"simple-host.app", "https://simple-host.app"}, {"localhost:8080", "http://localhost:8080"},
	} {
		scheme := "https://"
		if web.IsLoopbackHost(strings.Split(c.host, ":")[0]) {
			scheme = "http://"
		}
		req := httptest.NewRequest("POST", scheme+c.host+"/v1/auth", strings.NewReader(`{`))
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("apex %q origin %q: %d %s", c.host, c.origin, rec.Code, rec.Body.String())
		}
	}

	// A loopback host with a foreign Origin, or another local app's port, is still refused.
	for _, origin := range []string{"https://shop.example.org", "http://localhost:3000", "https://localhost:8080"} {
		req := httptest.NewRequest("POST", "http://localhost:8080/v1/auth", strings.NewReader(`{`))
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("loopback with origin %q: %d", origin, rec.Code)
		}
	}

	// No public base URL configured: nothing is refused.
	bare := http.NewServeMux()
	New(nil, accountapp.New(nil), nil, "", nil, httpx.NewRateLimiter(100, 100), httpx.NewRateLimiter(100, 100), nil, &httpx.Caches{}).Register(bare, identity, identity)
	req := httptest.NewRequest("POST", "https://anything.example/v1/auth", strings.NewReader(`{`))
	rec := httptest.NewRecorder()
	bare.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no base host: %d", rec.Code)
	}
}
