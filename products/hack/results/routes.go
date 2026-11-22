package results

import (
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the results routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	optional := func(fn http.HandlerFunc) http.Handler { return auth.Optional(authMW)(fn) }
	mux.Handle("POST /v1/hack/events/{slug}/results/publish", wrap(h.publishResults))
	mux.Handle("PATCH /v1/hack/events/{slug}/results", wrap(h.patchResults))
	mux.Handle("GET /v1/hack/events/{slug}/results", optional(h.getPublicResults))
	mux.Handle("GET /v1/hack/events/{slug}/my-results", wrap(h.getMyResults))
	mux.Handle("GET /v1/hack/events/{slug}/export/scores.csv", wrap(h.exportScoresCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/results.csv", wrap(h.exportResultsCSV))
}

type handler struct{ *core.Hack }
