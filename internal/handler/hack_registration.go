package handler

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var hackTrackSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

func (h *HackHandler) registerRegistration(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/registration", wrap(h.getRegistration))
	mux.Handle("PUT /v1/hack/events/{slug}/registration", wrap(h.putRegistration))
	mux.Handle("GET /v1/hack/events/{slug}/applications", wrap(h.getApplications))
	mux.Handle("POST /v1/hack/events/{slug}/applications/{user_id}/decision", wrap(h.decideApplication))
	mux.Handle("GET /v1/hack/events/{slug}/tracks", wrap(h.getTracks))
	mux.Handle("PUT /v1/hack/events/{slug}/tracks", wrap(h.putTracks))
	mux.Handle("PUT /v1/hack/events/{slug}/team/track", wrap(h.chooseTrack))
}
func (h *HackHandler) getRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	q, approval, err := db.RegistrationSettings(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"questions": q, "approval_required": approval})
}
func validateSignupQuestions(w http.ResponseWriter, in []db.SignupQuestion) ([]db.SignupQuestion, bool) {
	if len(in) > 8 {
		writeHackErr(w, 400, "too_many_questions", "up to eight sign-up questions are allowed")
		return nil, false
	}
	out := make([]db.SignupQuestion, 0, len(in))
	seen := map[string]bool{}
	for i, q := range in {
		id := strings.TrimSpace(q.ID)
		if id == "" {
			id = "q" + strconv.Itoa(i+1)
		}
		if !hackTrackSlug.MatchString(id) || seen[id] {
			writeHackErr(w, 400, "invalid_question_id", "question ids must be unique lowercase names")
			return nil, false
		}
		seen[id] = true
		prompt, ok := checkHackLine(w, q.Prompt, "question", 1, 160)
		if !ok {
			return nil, false
		}
		out = append(out, db.SignupQuestion{ID: id, Prompt: prompt, Required: q.Required})
	}
	return out, true
}
func (h *HackHandler) putRegistration(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		Questions        []db.SignupQuestion `json:"questions"`
		ApprovalRequired bool                `json:"approval_required"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	questions, ok := validateSignupQuestions(w, req.Questions)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	var stage string
	if err := tx.QueryRowContext(r.Context(), `SELECT stage FROM events WHERE id=$1 FOR UPDATE`, a.event.ID).Scan(&stage); err != nil {
		writeInternal(w)
		return
	}
	if stage == "archived" {
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	if err := db.SetRegistrationSettings(r.Context(), tx, a.event.ID, questions, req.ApprovalRequired); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"questions": questions, "approval_required": req.ApprovalRequired})
}
func (h *HackHandler) getApplications(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	list, err := db.ListRegistrationApplications(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, list)
}
func (h *HackHandler) decideApplication(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !uuidShape.MatchString(id) {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Decision != "approved" && req.Decision != "rejected" {
		writeHackErr(w, 400, "invalid_decision", "decision must be approved or rejected")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	var stage string
	if err := tx.QueryRowContext(r.Context(), `SELECT stage FROM events WHERE id=$1 FOR UPDATE`, a.event.ID).Scan(&stage); err != nil {
		writeInternal(w)
		return
	}
	if stage == "archived" {
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	updated, err := db.DecideRegistration(r.Context(), tx, a.event.ID, id, a.user.ID, req.Decision)
	if err != nil {
		writeInternal(w)
		return
	}
	if !updated {
		writeHackErr(w, 409, "application_not_pending", "only pending applications can be decided")
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"user_id": id, "status": req.Decision})
}
func (h *HackHandler) getTracks(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	tracks, err := db.ListEventTracks(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, tracks)
}
func validateTracks(w http.ResponseWriter, in []db.EventTrack) ([]db.EventTrack, bool) {
	if len(in) > 12 {
		writeHackErr(w, 400, "too_many_tracks", "up to twelve tracks are allowed")
		return nil, false
	}
	out := make([]db.EventTrack, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		slug := strings.TrimSpace(t.Slug)
		if !hackTrackSlug.MatchString(slug) || seen[slug] {
			writeHackErr(w, 400, "invalid_track_slug", "track slugs must be unique lowercase names")
			return nil, false
		}
		seen[slug] = true
		name, ok := checkHackLine(w, t.Name, "track_name", 1, 80)
		if !ok {
			return nil, false
		}
		challenge, ok := checkHackText(w, t.Challenge, "challenge", 0, 3000)
		if !ok {
			return nil, false
		}
		prize, ok := checkHackText(w, t.Prize, "track_prize", 0, 1000)
		if !ok {
			return nil, false
		}
		out = append(out, db.EventTrack{Slug: slug, Name: name, Challenge: challenge, Prize: prize})
	}
	return out, true
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
	tracks, ok := validateTracks(w, req.Tracks)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	var stage string
	if err = tx.QueryRowContext(r.Context(), `SELECT stage FROM events WHERE id=$1 FOR UPDATE`, a.event.ID).Scan(&stage); err != nil {
		writeInternal(w)
		return
	}
	if stage == "archived" {
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	if err = db.ReplaceEventTracks(r.Context(), tx, a.event.ID, tracks); err != nil {
		writeInternal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	out, err := db.ListEventTracks(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, out)
}
func (h *HackHandler) chooseTrack(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, 409, "teams_locked", "team choices are closed")
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
	slug := strings.TrimSpace(req.Track)
	if slug != "" && !hackTrackSlug.MatchString(slug) {
		writeHackErr(w, 400, "invalid_track", "choose a listed track")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `SELECT 1 FROM events WHERE id=$1 FOR SHARE`, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	st, err := db.TeamWriteStateFor(r.Context(), tx, team.ID, true)
	if err != nil {
		writeInternal(w)
		return
	}
	if st.WriteRefusal() != "" {
		writeHackErr(w, 409, "team_choice_closed", "team choices are closed")
		return
	}
	if _, err = db.SetTeamTrack(r.Context(), tx, a.event.ID, team.ID, slug); errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, 404, "track_not_found", "track not found")
		return
	} else if err != nil {
		writeInternal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	track, err := db.TeamTrack(r.Context(), h.database, team.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"track": track})
}
