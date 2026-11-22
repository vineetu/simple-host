package teamsites

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the teamsites routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/site", wrap(h.getTeamSiteStatus))
	mux.Handle("PUT /v1/hack/events/{slug}/teams/{team}/deadline", wrap(h.setTeamDeadline))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/takedown", wrap(h.takeDownTeamSite))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/restore", wrap(h.restoreTeamSite))
	mux.Handle("GET /v1/hack/my-teams", wrap(h.myTeams))
	mux.Handle("GET /v1/hack/events/{slug}/website", wrap(h.getEventWebsite))
	mux.Handle("PATCH /v1/hack/events/{slug}/website", wrap(h.patchEventWebsite))
}

type handler struct{ *core.Hack }
