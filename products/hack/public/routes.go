package public

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the public routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/directory", http.HandlerFunc(h.getDirectory))
	mux.HandleFunc("GET /v1/hack/events/{slug}/public", h.PublicEventJSON)
	mux.HandleFunc("OPTIONS /v1/hack/events/{slug}/public", h.PublicEventJSON)
	mux.Handle("GET /v1/hack/events/{slug}/submissions", wrap(h.getSubmissions))
	mux.Handle("GET /v1/hack/events/{slug}/submissions/{team}/screenshot", wrap(h.getSubmissionScreenshot))
}

type handler struct{ *core.Hack }
