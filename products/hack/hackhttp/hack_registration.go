package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *participationHTTP) getRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	q, approval, err := h.members.Registration(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// applications_open is read-only here (People -> Sign-up's one-line
	// summary); it is changed from Settings (PATCH .../events/{slug}).
	httpx.WriteJSON(w, 200, map[string]any{"questions": q, "approval_required": approval, "applications_open": a.event.ApplicationsOpen})
}
func (h *participationHTTP) putRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		Questions        []model.SignupQuestion `json:"questions"`
		ApprovalRequired bool                   `json:"approval_required"`
		AdmitPending     bool                   `json:"admit_pending"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	questions, err := h.members.ValidateQuestions(req.Questions)
	if err != nil {
		writeHackError(w, err)
		return
	}
	if err := h.members.SetRegistration(r.Context(), a.event.ID, a.user.ID, questions, req.ApprovalRequired, req.AdmitPending); err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"questions": questions, "approval_required": req.ApprovalRequired})
}
func (h *participationHTTP) getApplications(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	list, err := h.members.Applications(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, list)
}
func (h *participationHTTP) decideApplication(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(id) {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if err := h.members.ValidateDecision(req.Decision); err != nil {
		writeHackError(w, err)
		return
	}
	if err := h.members.DecideApplication(r.Context(), a.event.ID, id, a.user.ID, req.Decision); err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"user_id": id, "status": req.Decision})
}
func (h *participationHTTP) getTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	tracks, err := h.members.Tracks(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, tracks)
}
func (h *participationHTTP) putTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		Tracks []model.EventTrack `json:"tracks"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	tracks, err := h.members.ValidateTracks(req.Tracks)
	if err != nil {
		writeHackError(w, err)
		return
	}
	out, err := h.members.SetTracks(r.Context(), a.event.ID, tracks)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, out)
}
func (h *participationHTTP) chooseTrack(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		Track string `json:"track"`
		Team  string `json:"team"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	var team model.EventTeam
	if a.member.Role == "organiser" {
		var err error
		team, err = h.teams.TeamBySlug(r.Context(), a.event.ID, req.Team)
		if err != nil {
			httpx.WriteError(w, 404, "team_not_found", "team not found")
			return
		}
	} else {
		if err := h.members.ValidateTrackStage(a.event.Stage); err != nil {
			writeHackError(w, err)
			return
		}
		team, ok = h.participantTeam(w, r, a)
		if !ok {
			return
		}
	}
	slug, err := h.members.ValidateTrackChoice(req.Track)
	if err != nil {
		writeHackError(w, err)
		return
	}
	track, err := h.members.ChooseTrack(r.Context(), a.event.ID, team.ID, slug, a.member.Role == "organiser")
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"track": track})
}
