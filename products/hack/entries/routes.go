package entries

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the entries routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/entry", wrap(h.getEntry))
	mux.Handle("PUT /v1/hack/events/{slug}/entry", wrap(h.putEntry))
	mux.Handle("POST /v1/hack/events/{slug}/entry/submit", wrap(h.submitEntry))
	mux.Handle("POST /v1/hack/events/{slug}/entry/unsubmit", wrap(h.unsubmitEntry))
	mux.Handle("GET /v1/hack/events/{slug}/entry/screenshot", wrap(h.getEntryScreenshot))
	mux.Handle("PUT /v1/hack/events/{slug}/entry/screenshot", wrap(h.putEntryScreenshot))
	mux.Handle("DELETE /v1/hack/events/{slug}/entry/screenshot", wrap(h.deleteEntryScreenshot))
	mux.Handle("GET /v1/hack/events/{slug}/entry-rules", wrap(h.getEntryRules))
	mux.Handle("PUT /v1/hack/events/{slug}/entry-rules", wrap(h.putEntryRules))
	mux.Handle("GET /v1/hack/events/{slug}/teams/{team}/screenshot", wrap(h.getTeamScreenshot))
	mux.Handle("GET /v1/hack/events/{slug}/entries", wrap(h.listEntries))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/entry/exclude", wrap(h.excludeEntry))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/entry/include", wrap(h.includeEntry))
}

type handler struct{ *core.Hack }
