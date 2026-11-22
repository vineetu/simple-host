package judging

import (
	"database/sql"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

func nullableString(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func (h *handler) previewJudging(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.Entries.LiveResults(r.Context(), a.Event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// A judging preview is read-only maths, never published, so it carries
	// no entry cards.
	httpx.WriteJSON(w, 200, map[string]any{"results": entries.SnapshotRows(rows, nil), "deciding_mode": a.Event.ScoreMode})
}

func (h *handler) getManualAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.Entries.EventAssignments(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"assignments": rows})
}

func (h *handler) putManualAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Assignments []model.JudgeAssignment `json:"assignments"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if len(req.Assignments) > 500 {
		httpx.WriteError(w, 400, "too_many_assignments", "up to 500 assignments are allowed")
		return
	}
	rows, err := h.Entries.PutManualAssignments(r.Context(), a.Event.ID, req.Assignments)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"assignments": rows})
}

func (h *handler) getTrackPanels(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.Entries.TrackPanels(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"panels": rows})
}

func (h *handler) putTrackPanels(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Panels []entries.TrackPanelMember `json:"panels"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if len(req.Panels) > 240 {
		httpx.WriteError(w, 400, "too_many_panel_members", "up to 240 track panel memberships are allowed")
		return
	}
	rows, err := h.Entries.PutTrackPanels(r.Context(), a.Event.ID, req.Panels)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"panels": rows})
}
