package assistant

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Handler serves the organiser assistant; with no Service every assistant route is off.
type Handler struct {
	*core.Hack
	Service *AssistantService
}

// Register adds the assistant routes.
func (h *Handler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/assistant", wrap(h.getAssistant))
	mux.Handle("POST /v1/hack/events/{slug}/assistant", wrap(h.postAssistant))
}
