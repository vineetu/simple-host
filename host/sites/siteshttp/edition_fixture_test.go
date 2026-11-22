package siteshttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

func groupTestEdition() *edition.Edition { return groupTestEditionWithLimits(config.DefaultLimits()) }
func groupTestEditionWithLimits(limits config.Limits) *edition.Edition {
	return edition.FromPolicies(edition.RoutePolicy{HostedEvents: true}, edition.PresentationPolicy{ProductName: "Test product", ProductAssets: true, CanonicalBaseURL: "https://product.example", StylePath: "product.css", StyleClass: "product"}, edition.IdentityPolicy{RandomHandles: true, PaperMetrics: true}, edition.StoragePolicy{EventSites: true, StrictArchives: true, JudgePreviewFrames: true, EventVersionRetention: true, CombinedPool: true}, edition.NotificationPolicy{EventScoped: true}, limits)
}

func platformTestEdition() *edition.Edition { return edition.Default(config.DefaultLimits()) }

// chromeTestHandler is a SiteHandler with no database, for routes that never query it.
func chromeTestHandler(ed *edition.Edition) *SiteHandler {
	return withHosts(&SiteHandler{Handler: &sitesroute.Handler{Ed: ed, Assets: testAssetPipeline(ed, ""), Limits: ed.Limits(), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"}})
}

func chromeTestMux(t *testing.T, ed *edition.Edition) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(ed))
	return mux
}

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
