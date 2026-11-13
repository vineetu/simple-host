package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/httpx"
)

// LegacyStorageGate removes the old saved-data API from hosted Simple Hack.
// It wraps both public requests and the in-process REST calls made by MCP.
// Simple Host keeps the existing routes and stored data is never changed here.
func LegacyStorageGate(ed *edition.Edition, legacyPath func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !ed.Storage().LegacyRoutes && legacyPath(r.URL.Path) {
				httpx.WriteJSON(w, http.StatusGone, httpx.ErrorResponse{
					Error: "Simple Hack website storage now uses KV, SQLite and files under /v1/sites/{site}/storage",
					Code:  "legacy_storage_removed",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
