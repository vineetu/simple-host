package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/db"
)

func nullableString(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func (h *HackHandler) registerJudgingOptions(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/assignments", wrap(h.getManualAssignments))
	mux.Handle("PUT /v1/hack/events/{slug}/assignments", wrap(h.putManualAssignments))
	mux.Handle("GET /v1/hack/events/{slug}/judging/panels", wrap(h.getTrackPanels))
	mux.Handle("PUT /v1/hack/events/{slug}/judging/panels", wrap(h.putTrackPanels))
	mux.Handle("GET /v1/hack/events/{slug}/judging/preview", wrap(h.previewJudging))
}

func (h *HackHandler) previewJudging(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := computeResultsWithOptions(r.Context(), h.database, a.event)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"results": resultsSnapshotJSON(rows), "deciding_mode": a.event.ScoreMode})
}

func judgingOptionsTx(w http.ResponseWriter, r *http.Request, h *HackHandler, eventID string) (*sql.Tx, db.Event, bool) {
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return nil, db.Event{}, false
	}
	ev, err := db.GetEventForUpdate(r.Context(), tx, eventID)
	if err != nil {
		tx.Rollback()
		writeInternal(w)
		return nil, db.Event{}, false
	}
	if ev.Stage == "archived" {
		tx.Rollback()
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return nil, db.Event{}, false
	}
	if writeIfJudgingLocked(w, ev) {
		tx.Rollback()
		return nil, db.Event{}, false
	}
	return tx, ev, true
}

func (h *HackHandler) getManualAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := db.ListEventAssignments(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"assignments": rows})
}

func (h *HackHandler) putManualAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Assignments []db.JudgeAssignment `json:"assignments"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if len(req.Assignments) > 500 {
		writeHackErr(w, 400, "too_many_assignments", "up to 500 assignments are allowed")
		return
	}
	tx, ev, ok := judgingOptionsTx(w, r, h, a.event.ID)
	if !ok {
		return
	}
	defer tx.Rollback()
	if ev.JudgeAssignmentMode != "manual" {
		writeHackErr(w, 409, "invalid_assignment_mode", "switch to manual assignment first")
		return
	}
	seen := map[string]bool{}
	for _, row := range req.Assignments {
		if !uuidShape.MatchString(row.JudgeID) || !uuidShape.MatchString(row.TeamID) {
			writeHackErr(w, 400, "invalid_assignment", "use event judge and team ids")
			return
		}
		key := judgeTeamKey(row.JudgeID, row.TeamID)
		if seen[key] {
			writeHackErr(w, 400, "duplicate_assignment", "each judge and team pair appears once")
			return
		}
		seen[key] = true
		judge, err := db.EventJudgeInEvent(r.Context(), tx, ev.ID, row.JudgeID)
		if err != nil {
			writeInternal(w)
			return
		}
		if !judge {
			writeHackErr(w, 404, "judge_not_found", "judge not found in this event")
			return
		}
		if _, err := db.EventTeamInEvent(r.Context(), tx, ev.ID, row.TeamID); errors.Is(err, sql.ErrNoRows) {
			writeHackErr(w, 404, "team_not_found", "team not found in this event")
			return
		} else if err != nil {
			writeInternal(w)
			return
		}
	}
	conflicts, err := db.ListConflictPairs(r.Context(), tx, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	for _, p := range conflicts {
		if seen[judgeTeamKey(p.JudgeID, p.TeamID)] {
			writeHackErr(w, 409, "judge_conflict", "a conflicted judge cannot be assigned to that team")
			return
		}
	}
	if err := db.ReplaceAssignments(r.Context(), tx, ev.ID, req.Assignments); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	rows, err := db.ListEventAssignments(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"assignments": rows})
}

func (h *HackHandler) getTrackPanels(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := db.ListTrackPanels(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"panels": rows})
}

func (h *HackHandler) putTrackPanels(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Panels []db.TrackPanelMember `json:"panels"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if len(req.Panels) > 240 {
		writeHackErr(w, 400, "too_many_panel_members", "up to 240 track panel memberships are allowed")
		return
	}
	tx, ev, ok := judgingOptionsTx(w, r, h, a.event.ID)
	if !ok {
		return
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, p := range req.Panels {
		if !uuidShape.MatchString(p.TrackID) || !uuidShape.MatchString(p.JudgeID) {
			writeHackErr(w, 400, "invalid_panel", "use event track and judge ids")
			return
		}
		key := p.TrackID + "\x00" + p.JudgeID
		if seen[key] {
			writeHackErr(w, 400, "duplicate_panel_member", "each judge appears once per track")
			return
		}
		seen[key] = true
		track, err := db.EventTrackInEvent(r.Context(), tx, ev.ID, p.TrackID)
		if err != nil {
			writeInternal(w)
			return
		}
		if !track {
			writeHackErr(w, 404, "track_not_found", "track not found in this event")
			return
		}
		judge, err := db.EventJudgeInEvent(r.Context(), tx, ev.ID, p.JudgeID)
		if err != nil {
			writeInternal(w)
			return
		}
		if !judge {
			writeHackErr(w, 404, "judge_not_found", "judge not found in this event")
			return
		}
	}
	if err := db.ReplaceTrackPanels(r.Context(), tx, ev.ID, req.Panels); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	rows, err := db.ListTrackPanels(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"panels": rows})
}
