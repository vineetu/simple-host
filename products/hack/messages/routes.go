package messages

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the messages routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("POST /v1/hack/events/{slug}/team-messages", wrap(h.postTeamMessages))
	mux.Handle("GET /v1/hack/events/{slug}/teams/{team}/messages", wrap(h.getOrganiserTeamMessages))
	mux.Handle("GET /v1/hack/events/{slug}/team/messages", wrap(h.getMyTeamMessages))
	mux.Handle("POST /v1/hack/events/{slug}/team/messages", wrap(h.postMyTeamMessage))
}

type handler struct{ *core.Hack }
