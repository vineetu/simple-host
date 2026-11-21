package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/model"
)

// patchTeam is PATCH /v1/hack/events/{slug}/team: name and looking_for are
// each optional; send only what changes.
func (h *participationHTTP) patchTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Name       *string `json:"name"`
		LookingFor *string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	team, err := h.teams.PatchTeam(r.Context(), a.event, a.user.ID, req.Name, req.LookingFor)
	h.writeParticipantTeam(w, r, http.StatusOK, a.user.ID, team, err)
}

// setMyTeamLock is PUT /v1/hack/events/{slug}/team/lock. Locking clears
// the team's pending requests and invites and reports the counts.
func (h *participationHTTP) setMyTeamLock(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	requests, invites, err := h.teams.ParticipantLock(r.Context(), a.event, a.user.ID, req.Locked)
	writeLockAnswer(w, req.Locked, requests, invites, err)
}

// writeLockAnswer answers a lock change with the counts it cleared.
func writeLockAnswer(w http.ResponseWriter, locked bool, requests, invites int, err error) {
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"locked": locked, "cleared": map[string]any{"requests": requests, "invites": invites}})
}

// setMyTeammateAdmin is PUT /v1/hack/events/{slug}/team/members/{user_id}/admin.
func (h *participationHTTP) setMyTeammateAdmin(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Admin bool `json:"admin"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	userID := r.PathValue("user_id")
	err := h.teams.ParticipantAdmin(r.Context(), a.event, a.user.ID, userID, req.Admin)
	writeAdminAnswer(w, userID, req.Admin, err)
}

// writeAdminAnswer answers an admin-flag change.
func writeAdminAnswer(w http.ResponseWriter, userID string, admin bool, err error) {
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": userID, "admin": admin})
}

// newMyTeamLink is POST /v1/hack/events/{slug}/team/link. The old code and
// link stop working at once.
func (h *participationHTTP) newMyTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	fresh, err := h.teams.ParticipantLink(r.Context(), a.event, a.user.ID)
	h.writeLinkAnswer(w, fresh, err)
}

// writeLinkAnswer answers a link rotation with the new code and link.
func (h *participationHTTP) writeLinkAnswer(w http.ResponseWriter, fresh model.EventTeam, err error) {
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": fresh.Code, "link": h.links.teamLinkURL(fresh.Code)})
}

func (h *participationHTTP) lockOrganiserTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	requests, invites, err := h.teams.OrganiserLock(r.Context(), a.event, r.PathValue("team"), a.user.ID, req.Locked)
	writeLockAnswer(w, req.Locked, requests, invites, err)
}

func (h *participationHTTP) setOrganiserTeamAdmin(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Admin bool `json:"admin"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	userID := r.PathValue("user_id")
	err := h.teams.OrganiserAdmin(r.Context(), a.event, r.PathValue("team"), userID, req.Admin)
	writeAdminAnswer(w, userID, req.Admin, err)
}

func (h *participationHTTP) newOrganiserTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	fresh, err := h.teams.OrganiserLink(r.Context(), a.event, r.PathValue("team"))
	h.writeLinkAnswer(w, fresh, err)
}
