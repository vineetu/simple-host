package servertest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/internal/extension"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

// The product half of cmd/server's compatibility test needs the product schema.
func TestCompatibilityRoutes(t *testing.T) {
	for _, domain := range []string{"simple-host.app", "host.example.test"} {
		t.Run(domain, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			cfg.SiteDomain = domain
			cfg.PublicBaseURL = "https://" + domain
			app := platformtest.Build(t, cfg, []*extension.Extension{hack.Extension()})
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, path := range []string{"/hackathons", "/hackathons.html"} {
					w := httptest.NewRecorder()
					app.Handler.ServeHTTP(w, httptest.NewRequest(method, cfg.PublicBaseURL+path+"?from=legacy", nil))
					if w.Code != http.StatusOK {
						t.Errorf("%s %s: status %d, Location %q", method, path, w.Code, w.Header().Get("Location"))
					}
				}
			}
		})
	}
}
