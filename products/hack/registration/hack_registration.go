package registration

import (
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *handler) getRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	q, approval, err := h.Members.Registration(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// applications_open is read-only here (People -> Sign-up's one-line
	// summary); it is changed from Settings (PATCH .../events/{slug}).
	httpx.WriteJSON(w, 200, map[string]any{"questions": q, "approval_required": approval, "applications_open": a.Event.ApplicationsOpen})
}
func (h *handler) putRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	var req struct {
		Questions        []model.SignupQuestion `json:"questions"`
		ApprovalRequired bool                   `json:"approval_required"`
		AdmitPending     bool                   `json:"admit_pending"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	questions, err := h.Members.ValidateQuestions(req.Questions)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	if err := h.Members.SetRegistration(r.Context(), a.Event.ID, a.User.ID, questions, req.ApprovalRequired, req.AdmitPending); err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"questions": questions, "approval_required": req.ApprovalRequired})
}
func (h *handler) getApplications(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	list, err := h.Members.Applications(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, list)
}
func (h *handler) decideApplication(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(id) {
		core.WriteEventNotFound(w)
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if err := h.Members.ValidateDecision(req.Decision); err != nil {
		core.WriteError(w, err)
		return
	}
	if err := h.Members.DecideApplication(r.Context(), a.Event.ID, id, a.User.ID, req.Decision); err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"user_id": id, "status": req.Decision})
}
func (h *handler) getTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	tracks, err := h.Members.Tracks(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, tracks)
}
func (h *handler) putTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	var req struct {
		Tracks []model.EventTrack `json:"tracks"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	tracks, err := h.Members.ValidateTracks(req.Tracks)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	out, err := h.Members.SetTracks(r.Context(), a.Event.ID, tracks)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, out)
}
func (h *handler) chooseTrack(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		Track string `json:"track"`
		Team  string `json:"team"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	var team model.EventTeam
	if a.Member.Role == "organiser" {
		var err error
		team, err = h.Teams.TeamBySlug(r.Context(), a.Event.ID, req.Team)
		if err != nil {
			httpx.WriteError(w, 404, "team_not_found", "team not found")
			return
		}
	} else {
		if err := h.Members.ValidateTrackStage(a.Event.Stage); err != nil {
			core.WriteError(w, err)
			return
		}
		team, ok = core.ParticipantTeam(w, r, h.Teams, a)
		if !ok {
			return
		}
	}
	slug, err := h.Members.ValidateTrackChoice(req.Track)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	track, err := h.Members.ChooseTrack(r.Context(), a.Event.ID, team.ID, slug, a.Member.Role == "organiser")
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"track": track})
}
