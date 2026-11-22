package organisers

import (
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the organisers routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	optional := func(fn http.HandlerFunc) http.Handler { return auth.Optional(authMW)(fn) }
	mux.Handle("POST /v1/hack/events/{slug}/organiser-invite", wrap(h.createOrganiserInvite))
	mux.Handle("DELETE /v1/hack/events/{slug}/organiser-invite", wrap(h.revokeOrganiserInvite))
	mux.Handle("DELETE /v1/hack/events/{slug}/organisers/{user_id}", wrap(h.removeOrganiser))
	mux.Handle("GET /v1/hack/organiser/{code}", optional(h.getOrganiserInvite))
	mux.Handle("POST /v1/hack/organiser/{code}", wrap(h.acceptOrganiserInvite))
}

type handler struct{ *core.Hack }
