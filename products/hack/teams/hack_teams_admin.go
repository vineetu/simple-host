package teams

import (
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/model"
)

// patchTeam is PATCH /v1/hack/events/{slug}/team: name and looking_for are
// each optional; send only what changes.
func (h *handler) patchTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Name       *string `json:"name"`
		LookingFor *string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	team, err := h.Teams.PatchTeam(r.Context(), a.Event, a.User.ID, req.Name, req.LookingFor)
	h.writeParticipantTeam(w, r, http.StatusOK, a.User.ID, team, err)
}

// setMyTeamLock is PUT /v1/hack/events/{slug}/team/lock. Locking clears
// the team's pending requests and invites and reports the counts.
func (h *handler) setMyTeamLock(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	requests, invites, err := h.Teams.ParticipantLock(r.Context(), a.Event, a.User.ID, req.Locked)
	writeLockAnswer(w, req.Locked, requests, invites, err)
}

func writeLockAnswer(w http.ResponseWriter, locked bool, requests, invites int, err error) {
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"locked": locked, "cleared": map[string]any{"requests": requests, "invites": invites}})
}

// setMyTeammateAdmin is PUT /v1/hack/events/{slug}/team/members/{user_id}/admin.
func (h *handler) setMyTeammateAdmin(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Admin bool `json:"admin"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	userID := r.PathValue("user_id")
	err := h.Teams.ParticipantAdmin(r.Context(), a.Event, a.User.ID, userID, req.Admin)
	writeAdminAnswer(w, userID, req.Admin, err)
}

func writeAdminAnswer(w http.ResponseWriter, userID string, admin bool, err error) {
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": userID, "admin": admin})
}

// newMyTeamLink is POST /v1/hack/events/{slug}/team/link. The old code and
// link stop working at once.
func (h *handler) newMyTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	fresh, err := h.Teams.ParticipantLink(r.Context(), a.Event, a.User.ID)
	h.writeLinkAnswer(w, fresh, err)
}

func (h *handler) writeLinkAnswer(w http.ResponseWriter, fresh model.EventTeam, err error) {
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": fresh.Code, "link": h.Links.TeamLinkURL(fresh.Code)})
}

func (h *handler) lockOrganiserTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	requests, invites, err := h.Teams.OrganiserLock(r.Context(), a.Event, r.PathValue("team"), a.User.ID, req.Locked)
	writeLockAnswer(w, req.Locked, requests, invites, err)
}

func (h *handler) setOrganiserTeamAdmin(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Admin bool `json:"admin"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	userID := r.PathValue("user_id")
	err := h.Teams.OrganiserAdmin(r.Context(), a.Event, r.PathValue("team"), userID, req.Admin)
	writeAdminAnswer(w, userID, req.Admin, err)
}

func (h *handler) newOrganiserTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	fresh, err := h.Teams.OrganiserLink(r.Context(), a.Event, r.PathValue("team"))
	h.writeLinkAnswer(w, fresh, err)
}
