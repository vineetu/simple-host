package servertest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

func TestLegacyStorageGate(t *testing.T) {
	paths := []string{
		"/v1/sites/team/state", "/v1/sites/team/state/history/1",
		"/v1/sites/team/collections/entries/items/1", "/v1/sites/team/data/name/kind",
		"/v1/sites/team/savers/block", "/v1/sites/team/history",
		"/v1/sites/team/allowed-origins", "/v1/sites/team/allow-anonymous-writes",
		"/v1/u/owner/sites/team/state", "/v1/u/owner/sites/team/collections/entries",
		"/v1/u/owner/sites/team/data/name/history/1", "/v1/u/owner/sites/team/history",
		"/v1/admin/sites/id/collections/private/export.csv", "/v1/admin/data-watch",
		"/v1/data-notify/stop",
	}
	currentPaths := []string{
		"/v1/sites/team/storage/resources", "/v1/sites/team/storage/kv/settings/keys/theme",
		"/v1/sites/team/visitor/auth", "/v1/sites/team/me", "/v1/sites/team/lock",
		"/v1/hack/events/event/website/storage/resources", "/v1/hack/events/event/state",
		"/v1/admin/sites/id/versions", "/v1/visitor/establish",
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	const refusal = "{\"error\":\"Simple Hack website storage now uses KV, SQLite and files under /v1/sites/{site}/storage\",\"code\":\"legacy_storage_removed\"}\n"
	for _, kind := range []edition.Kind{edition.Host, edition.Hack} {
		gate := hackhttp.LegacyStorageGate(edition.ForKind(kind), handler.LegacyStoragePath)(next)
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
			for _, path := range paths {
				r := httptest.NewRecorder()
				gate.ServeHTTP(r, httptest.NewRequest(method, path, nil))
				if kind == edition.Hack {
					if r.Code != http.StatusGone || r.Body.String() != refusal || r.Header().Get("Content-Type") != "application/json" {
						t.Errorf("Hack %s %s: %d %s", method, path, r.Code, r.Body.String())
					}
				} else if r.Code != http.StatusNoContent {
					t.Errorf("Host %s %s: %d %s", method, path, r.Code, r.Body.String())
				}
			}
			for _, path := range currentPaths {
				r := httptest.NewRecorder()
				gate.ServeHTTP(r, httptest.NewRequest(method, path, nil))
				if r.Code != http.StatusNoContent {
					t.Errorf("current route %s %s: %d %s", method, path, r.Code, r.Body.String())
				}
			}
		}
	}
}
