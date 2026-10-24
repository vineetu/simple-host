package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/mcp"
)

func TestAPIRoutingErrors(t *testing.T) {
	for _, websiteFallback := range []bool{false, true} {
		mux := http.NewServeMux()
		identity := func(h http.Handler) http.Handler { return h }
		users := &UserHandler{}
		users.Register(mux, auth.Middleware("admin", "admin-id", nil), identity)
		connector := &ConnectorHandler{mcp: &mcp.Server{}}
		connector.Register(mux, identity)
		// The archive route supports writes, but no GET at the site root.
		mux.HandleFunc("PUT /v1/sites/{sitename}", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 404, errorResponse{Error: "site not found", Code: "site_not_found", Hint: r.PathValue("sitename")})
		})
		if websiteFallback {
			mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(404)
				_, _ = w.Write([]byte("<h1>Not found</h1>"))
			})
			mux.Handle("POST /", http.NotFoundHandler())
		}
		app := APIErrors(mux, mux, "https://simple-host.app")
		for _, tc := range []struct {
			method, path string
			status       int
			code, allow  string
		}{
			{"GET", "/v1/unknown", 404, "not_found", ""},
			{"POST", "/v1/unknown", 404, "not_found", ""},
			{"PATCH", "/v1/unknown", 404, "not_found", ""},
			{"GET", "/v1/sites/example/unknown", 404, "not_found", ""},
			{"GET", "/v1/sites/example", 405, "method_not_allowed", "PUT"},
			{"POST", "/v1/me", 405, "method_not_allowed", "GET, HEAD"},
			{"PATCH", "/v1/me", 405, "method_not_allowed", "GET, HEAD"},
			{"GET", "/mcp/unknown", 404, "not_found", ""},
			{"POST", "/mcp/unknown", 404, "not_found", ""},
			{"DELETE", "/mcp/unknown", 404, "not_found", ""},
			{"PUT", "/mcp", 405, "method_not_allowed", "DELETE, GET, HEAD, POST"},
			{"GET", "/v1/me", 401, "missing_api_key", ""},
			{"POST", "/mcp", 401, "sign_in_required", ""},
			{"PUT", "/v1/sites/example", 404, "site_not_found", ""},
		} {
			t.Run(tc.method+tc.path, func(t *testing.T) {
				r := httptest.NewRecorder()
				app.ServeHTTP(r, httptest.NewRequest(tc.method, tc.path, nil))
				body := checkAPIError(t, r, tc.status, tc.code)
				if r.Header().Get("Allow") != tc.allow {
					t.Errorf("Allow = %q, want %q", r.Header().Get("Allow"), tc.allow)
				}
				if tc.status == 405 || tc.code == "not_found" {
					if !strings.Contains(body.Hint, "https://simple-host.app/docs.html") {
						t.Errorf("missing discovery hint: %+v", body)
					}
				}
				if tc.code == "site_not_found" && body.Hint != "example" {
					t.Errorf("route response or path value changed: %+v", body)
				}
			})
		}
		if websiteFallback {
			for _, path := range []string{"/missing-page", "/v10/missing", "/mcp-other"} {
				r := httptest.NewRecorder()
				app.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
				if r.Code != 404 || r.Body.String() != "<h1>Not found</h1>" || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/html") {
					t.Errorf("website fallback changed for %s: %d %s", path, r.Code, r.Body.String())
				}
			}
		}
	}
}

func checkAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errorResponse {
	t.Helper()
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %s: %v", rec.Body.String(), err)
	}
	if rec.Code != status || body.Code != code || body.Error == "" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("got %d %s (%s), want %d %s", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), status, code)
	}
	return body
}

func TestAPIErrorCodesAndHeaders(t *testing.T) {
	t.Run("missing skills", func(t *testing.T) {
		mux := http.NewServeMux()
		RegisterSkillsHub(mux, "https://simple-host.test")
		for _, path := range []string{"/v1/skills/unknown-skill", "/v1/skills/unknown-skill/references/missing.md", "/v1/skills/website-deploy/references/missing.md"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			checkAPIError(t, rec, 404, "not_found")
		}
	})
	t.Run("archive body cap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		_, err := readLimitedBody(rec, httptest.NewRequest("PUT", "/v1/sites/example", strings.NewReader("oversize")), 4)
		if err == nil {
			t.Fatal("oversized body accepted")
		}
		checkAPIError(t, rec, 413, "site_too_large")
	})
	t.Run("photo concurrency cap", func(t *testing.T) {
		t.Setenv("PHOTO_MAX_CONCURRENT", "1")
		if !acquirePhotoSave() {
			t.Fatal("could not occupy photo slot")
		}
		defer photoSaves.Add(-1)
		rec := httptest.NewRecorder()
		(&SiteHandler{}).addPhotos(rec, httptest.NewRequest("POST", "/v1/sites/example/photos", nil))
		checkAPIError(t, rec, 429, "rate_limited")
		if rec.Header().Get("Retry-After") != "2" {
			t.Fatal("missing Retry-After")
		}
	})
	t.Run("rate limit", func(t *testing.T) {
		limited := rateLimitByIP(newRateLimiter(1, 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		limited.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/me", nil))
		rec := httptest.NewRecorder()
		limited.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/me", nil))
		checkAPIError(t, rec, 429, "rate_limited")
		// A zero-rate bucket never refills, so allowHTTP's real refill-time
		// computation reports 0 here rather than a fabricated wait.
		if rec.Header().Get("Retry-After") != "0" {
			t.Fatal("missing Retry-After")
		}
	})
	t.Run("MCP auth", func(t *testing.T) {
		h := &ConnectorHandler{issuer: "https://simple-host.test"}
		rec := httptest.NewRecorder()
		h.mcpUnauthorized(rec, true)
		checkAPIError(t, rec, 401, "invalid_token")
		if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
			t.Fatal("missing bearer challenge")
		}
	})
	t.Run("MCP transport method", func(t *testing.T) {
		for _, method := range []string{"GET", "DELETE", "PUT"} {
			rec := httptest.NewRecorder()
			(&mcp.Server{}).ServeHTTP(rec, httptest.NewRequest(method, "/mcp", nil))
			// MCP transport errors use the JSON-RPC envelope, not the REST one.
			var body struct {
				JSONRPC string `json:"jsonrpc"`
				Error   struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 405 || body.JSONRPC != "2.0" || body.Error.Code != -32600 {
				t.Fatalf("invalid MCP transport error: status %d, body %s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Allow") != "POST" {
				t.Fatal("missing Allow")
			}
		}
	})
	for _, tc := range []struct {
		status int
		code   string
	}{{401, "sign_in_required"}, {429, "rate_limited"}, {413, "item_too_large"}} {
		rec := httptest.NewRecorder()
		writeJSON(rec, tc.status, errorResponse{Error: "refused"})
		checkAPIError(t, rec, tc.status, tc.code)
		rec = httptest.NewRecorder()
		writeJSON(rec, tc.status, errorResponse{Error: "refused", Code: "specific_code"})
		checkAPIError(t, rec, tc.status, "specific_code")
	}
}
