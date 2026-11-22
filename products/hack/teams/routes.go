package teams

import (
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the teams routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	optional := func(fn http.HandlerFunc) http.Handler { return auth.Optional(authMW)(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/people", wrap(h.listPeople))
	mux.Handle("DELETE /v1/hack/events/{slug}/people/{user_id}", wrap(h.removePerson))
	mux.Handle("GET /v1/hack/events/{slug}/teams", wrap(h.listTeams))
	mux.Handle("GET /v1/hack/events/{slug}/teams/open", wrap(h.listOpenTeams))
	mux.Handle("PUT /v1/hack/events/{slug}/team/finding", wrap(h.setTeamFinding)) // deprecated, one release
	mux.Handle("PUT /v1/hack/events/{slug}/me/looking", wrap(h.setLooking))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/requests", wrap(h.createTeamRequest))
	mux.Handle("POST /v1/hack/events/{slug}/team/invites", wrap(h.createTeamRequest))
	mux.Handle("POST /v1/hack/events/{slug}/requests/{id}/{action}", wrap(h.resolveTeamRequest))
	mux.Handle("DELETE /v1/hack/events/{slug}/requests/{id}", wrap(h.resolveTeamRequest))
	mux.Handle("DELETE /v1/hack/events/{slug}/team/members/{user_id}", wrap(h.removeTeamMember))
	mux.Handle("POST /v1/hack/events/{slug}/teams/fill", wrap(h.fillTeams))
	mux.Handle("POST /v1/hack/events/{slug}/teams", wrap(h.createTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/join", wrap(h.joinTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/leave", wrap(h.leaveTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/members", wrap(h.moveMember))
	mux.Handle("DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}", wrap(h.removeTeamMember))
	mux.Handle("DELETE /v1/hack/events/{slug}/teams/{team}", wrap(h.deleteTeam))
	mux.Handle("PATCH /v1/hack/events/{slug}/team", wrap(h.patchTeam))
	mux.Handle("PUT /v1/hack/events/{slug}/team/lock", wrap(h.setMyTeamLock))
	mux.Handle("PUT /v1/hack/events/{slug}/team/members/{user_id}/admin", wrap(h.setMyTeammateAdmin))
	mux.Handle("POST /v1/hack/events/{slug}/team/link", wrap(h.newMyTeamLink))
	mux.Handle("PUT /v1/hack/events/{slug}/teams/{team}/lock", wrap(h.lockOrganiserTeam))
	mux.Handle("PUT /v1/hack/events/{slug}/teams/{team}/members/{user_id}/admin", wrap(h.setOrganiserTeamAdmin))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/link", wrap(h.newOrganiserTeamLink))
	mux.Handle("GET /v1/hack/team-links/{code}", optional(h.getTeamLink))
	mux.Handle("PATCH /v1/hack/events/{slug}/teams/{team}", wrap(h.renameTeam))
}

type handler struct{ *core.Hack }
