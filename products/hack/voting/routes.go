package voting

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the voting routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/voting", wrap(h.getVotingSettings))
	mux.Handle("PUT /v1/hack/events/{slug}/voting", wrap(h.putVotingSettings))
	mux.Handle("PATCH /v1/hack/events/{slug}/directory", wrap(h.patchDirectoryListed))
	mux.Handle("GET /v1/hack/events/{slug}/vote", http.HandlerFunc(h.getVote))
	mux.Handle("GET /v1/hack/events/{slug}/my-vote", wrap(h.getMyVote))
	mux.Handle("PUT /v1/hack/events/{slug}/vote", wrap(h.putVote))
}

type handler struct{ *core.Hack }
