package accesshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/connector"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

func TestAPIRoutingErrors(t *testing.T) {
	for _, websiteFallback := range []bool{false, true} {
		mux := http.NewServeMux()
		identity := func(h http.Handler) http.Handler { return h }
		users := NewUserHandler(nil, accountapp.New(nil), nil, "")
		users.Register(mux, auth.Middleware("admin", "admin-id", accountapp.New(nil), 0), identity)
		connector := connector.New(nil, "https://simple-host.app", "", "simple-host.app", "sites.simple-host.app", "1.0.0", nil, false, nil, nil)
		connector.Register(mux, identity)
		// The archive route supports writes, but no GET at the site root.
		mux.HandleFunc("PUT /v1/sites/{sitename}", func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "site not found", Code: "site_not_found", Hint: r.PathValue("sitename")})
		})
		if websiteFallback {
			mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(404)
				_, _ = w.Write([]byte("<h1>Not found</h1>"))
			})
			mux.Handle("POST /", http.NotFoundHandler())
		}
		app := web.APIErrors(mux, mux, "https://simple-host.app")
		for _, tc := range []struct {
			method, path string
			status       int
			code, allow  string
		}{
			{"GET", "/v1/unknown", 404, "not_found", ""},
			{"GET", "/v1/sites/example", 405, "method_not_allowed", "PUT"},
			{"POST", "/v1/me", 405, "method_not_allowed", "GET, HEAD"},
			{"GET", "/mcp/unknown", 404, "not_found", ""},
			{"PUT", "/mcp", 405, "method_not_allowed", "DELETE, GET, HEAD, POST"},
			{"GET", "/v1/me", 401, "missing_api_key", ""},
			{"POST", "/mcp", 401, "sign_in_required", ""},
			{"PUT", "/v1/sites/example", 404, "site_not_found", ""},
		} {
			t.Run(tc.method+tc.path, func(t *testing.T) {
				r := httptest.NewRecorder()
				app.ServeHTTP(r, httptest.NewRequest(tc.method, tc.path, nil))
				checkAPIError(t, r, tc.status, tc.code)
				if r.Header().Get("Allow") != tc.allow {
					t.Errorf("Allow = %q, want %q", r.Header().Get("Allow"), tc.allow)
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

func checkAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) httpx.ErrorResponse {
	t.Helper()
	var body httpx.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %s: %v", rec.Body.String(), err)
	}
	if rec.Code != status || body.Code != code || body.Error == "" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("got %d %s (%s), want %d %s", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), status, code)
	}
	return body
}
