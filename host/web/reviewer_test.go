package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIAppsChallenge(t *testing.T) {
	for _, c := range []struct {
		token, want string
		status      int
	}{{"", "", 404}, {"  tok_abc123  ", "tok_abc123", 200}} {
		mux := http.NewServeMux()
		RegisterOpenAIAppsChallenge(mux, c.token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/openai-apps-challenge", nil))
		if rec.Code != c.status {
			t.Fatalf("token %q: status %d", c.token, rec.Code)
		}
		if c.status == 200 {
			if rec.Body.String() != c.want || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
				t.Fatalf("body %q type %q", rec.Body.String(), rec.Header().Get("Content-Type"))
			}
		}
	}
}
