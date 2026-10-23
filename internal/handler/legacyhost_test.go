package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyHostRedirectMCPReachesNext(t *testing.T) {
	h := &SiteHandler{siteDomain: "simple-host.app", contentHost: "sites.simple-host.app"}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://mcp.simple-host.app/mcp", nil)
	h.LegacyHostRedirect(next).ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusNoContent || rec.Header().Get("Location") != "" {
		t.Fatalf("next called = %v, status = %d, Location = %q", called, rec.Code, rec.Header().Get("Location"))
	}
}
