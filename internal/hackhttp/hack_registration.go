package hackhttp

import (
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"net/http"
)

func (h *HackHandler) registerRegistration(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/registration", wrap(h.getRegistration))
	mux.Handle("PUT /v1/hack/events/{slug}/registration", wrap(h.putRegistration))
	mux.Handle("GET /v1/hack/events/{slug}/applications", wrap(h.getApplications))
	mux.Handle("POST /v1/hack/events/{slug}/applications/{user_id}/decision", wrap(h.decideApplication))
	mux.Handle("GET /v1/hack/events/{slug}/tracks", wrap(h.getTracks))
	mux.Handle("PUT /v1/hack/events/{slug}/tracks", wrap(h.putTracks))
	mux.Handle("PUT /v1/hack/events/{slug}/team/track", wrap(h.chooseTrack))
	mux.Handle("GET /v1/hack/events/{slug}/apply", http.HandlerFunc(h.getApply))
	mux.Handle("POST /v1/hack/events/{slug}/apply", wrap(h.postApply))
}
func (h *HackHandler) getRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	q, approval, err := h.membersSvc().Registration(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// applications_open is read-only here (People -> Sign-up's one-line
	// summary); it is changed from Settings (PATCH .../events/{slug}).
	httpx.WriteJSON(w, 200, map[string]any{"questions": q, "approval_required": approval, "applications_open": a.event.ApplicationsOpen})
}
func (h *HackHandler) putRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		Questions        []db.SignupQuestion `json:"questions"`
		ApprovalRequired bool                `json:"approval_required"`
		AdmitPending     bool                `json:"admit_pending"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	questions, err := h.membersSvc().ValidateQuestions(req.Questions)
	if err != nil {
		writeMemberError(w, err)
		return
	}
	if err := h.membersSvc().SetRegistration(r.Context(), a.event.ID, a.user.ID, questions, req.ApprovalRequired, req.AdmitPending); err != nil {
		writeMemberError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"questions": questions, "approval_required": req.ApprovalRequired})
}
func (h *HackHandler) getApplications(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	list, err := h.membersSvc().Applications(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, list)
}
func (h *HackHandler) decideApplication(w http.ResponseWriter, r *http.Request) {
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
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if err := h.membersSvc().ValidateDecision(req.Decision); err != nil {
		writeMemberError(w, err)
		return
	}
	if err := h.membersSvc().DecideApplication(r.Context(), a.event.ID, id, a.user.ID, req.Decision); err != nil {
		writeMemberError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"user_id": id, "status": req.Decision})
}
func (h *HackHandler) getTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	tracks, err := h.membersSvc().Tracks(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, tracks)
}
func (h *HackHandler) putTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		Tracks []db.EventTrack `json:"tracks"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	tracks, err := h.membersSvc().ValidateTracks(req.Tracks)
	if err != nil {
		writeMemberError(w, err)
		return
	}
	out, err := h.membersSvc().SetTracks(r.Context(), a.event.ID, tracks)
	if err != nil {
		writeMemberError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, out)
}
func (h *HackHandler) chooseTrack(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if err := h.membersSvc().ValidateTrackStage(a.event.Stage); err != nil {
		writeMemberError(w, err)
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	var req struct {
		Track string `json:"track"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	slug, err := h.membersSvc().ValidateTrackChoice(req.Track)
	if err != nil {
		writeMemberError(w, err)
		return
	}
	track, err := h.membersSvc().ChooseTrack(r.Context(), a.event.ID, team.ID, slug)
	if err != nil {
		writeMemberError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"track": track})
}
