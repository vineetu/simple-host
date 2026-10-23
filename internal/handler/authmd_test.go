package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthMarkdown(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	for _, heading := range []string{"## Discover", "## Pick a method", "## Register", "## Claim", "## Exchange", "## Use the access_token", "## Errors", "## Revocation"} {
		if !strings.Contains(rec.Body.String(), heading+"\n") {
			t.Errorf("missing heading %q", heading)
		}
	}
}
