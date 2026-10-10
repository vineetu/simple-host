package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

// Account sign-in (emailed code and Google) is served only on the app's own
// address; on a site's, a person's or a connected address it answers a plain
// 403 that points at visitor sign-in, and a page on a site cannot reach the
// apex routes cross-origin either.
func TestAccountAuthOnlyOnAppAddress(t *testing.T) {
	mux := http.NewServeMux()
	users := NewUserHandler(nil, nil, "https://simple-host.app")
	identity := func(h http.Handler) http.Handler { return h }
	users.Register(mux, identity, identity)
	oauth := NewOAuthHandler(nil, config.Config{PublicBaseURL: "https://simple-host.app"})
	oauth.Register(mux)
	app := CORS(mux)

	routes := []struct{ method, path string }{
		{"POST", "/v1/auth"}, {"POST", "/v1/auth/verify"},
		{"GET", "/v1/auth/oauth/google/callback"},
	}
	hosts := []string{"shop.alice.simple-host.app", "alice.simple-host.app", "claimed.simple-host.app", "shop.example.org", "sites.simple-host.app", "simple-host.app.evil.example", "simple-host.app."}
	for _, rt := range routes {
		for _, host := range hosts {
			req := httptest.NewRequest(rt.method, "https://"+host+rt.path, strings.NewReader(`{"email":"customer@example.com","code":"123456"}`))
			req.Header.Set("Origin", "https://"+host)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusForbidden || !strings.Contains(body, `"code":"account_auth_unavailable"`) || !strings.Contains(body, "SH.mount") {
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
		// An opaque origin (a sandboxed frame) is foreign too.
		req = httptest.NewRequest(rt.method, "https://simple-host.app"+rt.path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "null")
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s from a null origin: %d", rt.path, rec.Code)
		}
	}

	// A hand-written Google link on a site's host is handed to the site-host
	// visitor start on that host (it never mints a key); the apex still starts
	// account sign-in (404 here: no provider is configured).
	for _, host := range hosts {
		req := httptest.NewRequest("GET", "https://"+host+"/v1/auth/oauth/google?return_to=https%3A%2F%2F"+host+"%2Fshop.html", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/v1/visitor/oauth/google?return_to=https%3A%2F%2F"+host) {
			t.Fatalf("start on %s: %d %q", host, rec.Code, rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("start on %s set a cookie", host)
		}
	}
	req := httptest.NewRequest("GET", "https://simple-host.app/v1/auth/oauth/google?return_to=https%3A%2F%2Fsimple-host.app%2F", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("apex start: %d %s", rec.Code, rec.Body.String())
	}
	// From a site page's origin the apex start is refused, not redirected.
	req = httptest.NewRequest("GET", "https://simple-host.app/v1/auth/oauth/google?return_to=https%3A%2F%2Fsimple-host.app%2F", nil)
	req.Header.Set("Origin", "https://shop.alice.simple-host.app")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("apex start from a site origin: %d", rec.Code)
	}

	// The Origin must be exactly the app's origin: another scheme or port on
	// the same host is foreign too.
	for _, origin := range []string{"http://simple-host.app", "https://simple-host.app:8443", "https://shop.alice.simple-host.app", "https://simple-host.app.evil.example"} {
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
		{"simple-host.app", ""}, {"simple-host.app", "https://simple-host.app"}, {"simple-host.app:443", "https://simple-host.app:443"}, {"SIMPLE-HOST.app", ""},
		{"localhost:8080", ""}, {"localhost:8080", "http://localhost:8080"}, {"127.0.0.1:8080", "http://127.0.0.1:8080"},
	} {
		scheme := "https://"
		if isLoopbackHost(strings.Split(c.host, ":")[0]) {
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
		req = httptest.NewRequest("POST", "http://localhost:8080/v1/auth", strings.NewReader(`{`))
		req.Header.Set("Origin", origin)
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("loopback with origin %q: %d", origin, rec.Code)
		}
	}

	// Provider discovery stays public: auth.js on a site reads it from the apex.
	req = httptest.NewRequest("GET", "https://simple-host.app/v1/auth/oauth/providers", nil)
	req.Header.Set("Origin", "https://shop.alice.simple-host.app")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("providers from a site: %d", rec.Code)
	}

	// Visitor sign-in is not an account route: it is not what this gate serves.
	if accountAuthAllowed(httptest.NewRequest("POST", "https://shop.alice.simple-host.app/v1/sites/shop/visitor/auth", nil), publicBaseOrigin("https://simple-host.app")) {
		t.Fatal("gate logic allowed a site host")
	}
	// A schemeless PUBLIC_BASE_URL still gates; an empty one gates nothing.
	if publicBaseOrigin("simple-host.app").Hostname() != "simple-host.app" || publicBaseOrigin("") != nil || publicBaseOrigin("://x") != nil {
		t.Fatal("publicBaseOrigin")
	}

	// No public base URL configured: nothing is refused.
	bare := http.NewServeMux()
	NewUserHandler(nil, nil, "").Register(bare, identity, identity)
	req = httptest.NewRequest("POST", "https://anything.example/v1/auth", strings.NewReader(`{`))
	rec = httptest.NewRecorder()
	bare.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no base host: %d", rec.Code)
	}
}
