package siteshttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/sites/sitesroute"

	"github.com/vsriram/simple-host/host/edition"
)

func TestLegacyHostRedirectMCPReachesNext(t *testing.T) {
	h := withHosts(&SiteHandler{Handler: &sitesroute.Handler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"}})
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
