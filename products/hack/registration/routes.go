package registration

import (
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the registration routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	optional := func(fn http.HandlerFunc) http.Handler { return auth.Optional(authMW)(fn) }
	mux.Handle("GET /v1/hack/join/{code}", optional(h.getJoin))
	mux.Handle("POST /v1/hack/join/{code}", wrap(h.postJoin))
	mux.Handle("GET /v1/hack/judge/{code}", optional(h.getJudge))
	mux.Handle("POST /v1/hack/judge/{code}", wrap(h.postJudge))
	mux.Handle("GET /v1/hack/events/{slug}/registration", wrap(h.getRegistration))
	mux.Handle("PUT /v1/hack/events/{slug}/registration", wrap(h.putRegistration))
	mux.Handle("GET /v1/hack/events/{slug}/applications", wrap(h.getApplications))
	mux.Handle("POST /v1/hack/events/{slug}/applications/{user_id}/decision", wrap(h.decideApplication))
	mux.Handle("GET /v1/hack/events/{slug}/tracks", wrap(h.getTracks))
	mux.Handle("PUT /v1/hack/events/{slug}/tracks", wrap(h.putTracks))
	mux.Handle("PUT /v1/hack/events/{slug}/team/track", wrap(h.chooseTrack))
	mux.Handle("GET /v1/hack/events/{slug}/apply", optional(h.getApply))
	mux.Handle("POST /v1/hack/events/{slug}/apply", wrap(h.postApply))
	mux.Handle("GET /v1/hack/events/{slug}/leave", wrap(h.previewLeaveEvent))
	mux.Handle("POST /v1/hack/events/{slug}/leave", wrap(h.postLeaveEvent))
}

type handler struct{ *core.Hack }
