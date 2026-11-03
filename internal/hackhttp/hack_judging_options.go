package hackhttp

import (
	"database/sql"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
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
		httpx.WriteInternal(w)
		return
	}
	// A judging preview is read-only maths, never published, so it carries
	// no entry cards (nil cards map -- resultsSnapshotJSON reads a nil map
	// fine, same as "no card for this team").
	httpx.WriteJSON(w, 200, map[string]any{"results": resultsSnapshotJSON(rows, nil), "deciding_mode": a.event.ScoreMode})
}

func writeJudgingWorkflowError(w http.ResponseWriter, err error) {
	e, ok := err.(*entries.Error)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	status := 400
	switch e.Code {
	case "event_closed", "scores_locked", "invalid_assignment_mode", "judge_conflict":
		status = 409
	case "judge_not_found", "team_not_found", "track_not_found":
		status = 404
	}
	httpx.WriteError(w, status, e.Code, e.Message)
}

func (h *HackHandler) getManualAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.entries.EventAssignments(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"assignments": rows})
}

func (h *HackHandler) putManualAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Assignments []model.JudgeAssignment `json:"assignments"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if len(req.Assignments) > 500 {
		httpx.WriteError(w, 400, "too_many_assignments", "up to 500 assignments are allowed")
		return
	}
	rows, err := h.entries.PutManualAssignments(r.Context(), a.event.ID, req.Assignments)
	if err != nil {
		writeJudgingWorkflowError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"assignments": rows})
}

func (h *HackHandler) getTrackPanels(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.entries.TrackPanels(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"panels": rows})
}

func (h *HackHandler) putTrackPanels(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Panels []entries.TrackPanelMember `json:"panels"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if len(req.Panels) > 240 {
		httpx.WriteError(w, 400, "too_many_panel_members", "up to 240 track panel memberships are allowed")
		return
	}
	rows, err := h.entries.PutTrackPanels(r.Context(), a.event.ID, req.Panels)
	if err != nil {
		writeJudgingWorkflowError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"panels": rows})
}
