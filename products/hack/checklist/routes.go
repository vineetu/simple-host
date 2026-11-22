package checklist

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the checklist routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/checklist", wrap(h.getChecklist))
	mux.Handle("POST /v1/hack/events/{slug}/checklist/not-needed/{item}", wrap(h.setChecklistNotNeeded))
	mux.Handle("DELETE /v1/hack/events/{slug}/checklist/not-needed/{item}", wrap(h.setChecklistNotNeeded))
	mux.Handle("POST /v1/hack/events/{slug}/checklist/acknowledgements/{item}", wrap(h.acknowledgeChecklist))
}

type handler struct{ *core.Hack }
