package servertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

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

func TestAPIErrorCodesAndHeaders(t *testing.T) {
	t.Run("missing skills", func(t *testing.T) {
		mux := http.NewServeMux()
		testAssetPipeline(edition.Default(config.DefaultLimits()), "").RegisterSkillsHub(mux, "https://simple-host.test")
		for _, path := range []string{"/v1/skills/unknown-skill", "/v1/skills/unknown-skill/references/missing.md", "/v1/skills/website-deploy/references/missing.md"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			checkAPIError(t, rec, 404, "not_found")
		}
	})
	t.Run("archive body cap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		_, err := siteshttp.ReadLimitedBody(rec, httptest.NewRequest("PUT", "/v1/sites/example", strings.NewReader("oversize")), 4)
		if err == nil {
			t.Fatal("oversized body accepted")
		}
		checkAPIError(t, rec, 413, "site_too_large")
	})
	t.Run("photo concurrency cap", func(t *testing.T) {
		t.Setenv("PHOTO_MAX_CONCURRENT", "1")
		if !siteshttp.AcquirePhotoSave() {
			t.Fatal("could not occupy photo slot")
		}
		defer siteshttp.PhotoSaves.Add(-1)
		rec := httptest.NewRecorder()
		(WithHosts(&siteshttp.SiteHandler{})).AddPhotos(rec, httptest.NewRequest("POST", "/v1/sites/example/photos", nil))
		checkAPIError(t, rec, 429, "rate_limited")
		if rec.Header().Get("Retry-After") != "2" {
			t.Fatal("missing Retry-After")
		}
	})
	t.Run("rate limit", func(t *testing.T) {
		limited := httpx.RateLimitByIP(httpx.NewRateLimiter(1, 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
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
		h := &accesshttp.ConnectorHandler{Issuer: "https://simple-host.test"}
		rec := httptest.NewRecorder()
		h.MCPUnauthorized(rec, true)
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
		httpx.WriteJSON(rec, tc.status, httpx.ErrorResponse{Error: "refused"})
		checkAPIError(t, rec, tc.status, tc.code)
		rec = httptest.NewRecorder()
		httpx.WriteJSON(rec, tc.status, httpx.ErrorResponse{Error: "refused", Code: "specific_code"})
		checkAPIError(t, rec, tc.status, "specific_code")
	}
}
