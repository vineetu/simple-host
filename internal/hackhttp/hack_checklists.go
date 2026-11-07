package hackhttp

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *HackHandler) registerChecklists(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/checklist", wrap(h.getChecklist))
	mux.Handle("POST /v1/hack/events/{slug}/checklist/not-needed/{item}", wrap(h.setChecklistNotNeeded))
	mux.Handle("DELETE /v1/hack/events/{slug}/checklist/not-needed/{item}", wrap(h.setChecklistNotNeeded))
	mux.Handle("POST /v1/hack/events/{slug}/checklist/acknowledgements/{item}", wrap(h.acknowledgeChecklist))
}

func (h *HackHandler) checklistService() *events.ChecklistService {
	return events.NewChecklist(h.database, h.entries, nil)
}

func (h *HackHandler) readChecklist(ctx context.Context, ev model.Event, m model.EventMember, role string) (events.Checklist, error) {
	var forward *events.ChecklistNext
	if m.Role == "organiser" {
		result, err := h.entries.EventResults(ctx, ev.ID)
		if err != nil {
			return events.Checklist{}, err
		}
		participants, _, _, err := h.eventsSvc().CountEventMembersLookup(ctx, ev.ID)
		if err != nil {
			return events.Checklist{}, err
		}
		step := nextStepJSON(ev, result, participants)
		if kind, _ := step["kind"].(string); kind != "none" {
			stage, _ := step["stage"].(string)
			label, _ := step["label"].(string)
			passed, _ := step["deadline_passed"].(bool)
			forward = &events.ChecklistNext{Kind: kind, Stage: stage, Label: label, ConfirmationRequired: true, BlockedReason: step["blocked"], DeadlinePassed: passed, Cue: h.scheduleCue(ctx, ev, step)}
		}
	}
	return h.checklistService().Read(ctx, ev.ID, m.UserID, role, m.JoinedAt.IsZero(), forward, time.Now())
}

// checklistForView keeps the event-view integration to a single additive call.
func (h *HackHandler) checklistForView(ctx context.Context, ev model.Event, m model.EventMember) any {
	list, err := h.readChecklist(ctx, ev, m, "")
	if err != nil {
		log.Printf("hack: checklist %s: %v", ev.Slug, err)
		return nil
	}
	return list
}

func (h *HackHandler) getChecklist(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	h.pinDue(r.Context(), a.event.ID)
	list, err := h.readChecklist(r.Context(), a.event, a.member, r.URL.Query().Get("role"))
	if err != nil {
		writeChecklistError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"checklist": list})
}
func writeChecklistError(w http.ResponseWriter, err error) {
	if e, ok := err.(*events.Error); ok {
		status := http.StatusBadRequest
		switch e.Code {
		case "event_closed", "stale_checklist_version":
			status = http.StatusConflict
		case "checklist_role_forbidden", "event_taken_down":
			status = http.StatusForbidden
		case "event_not_found":
			status = http.StatusNotFound
		}
		httpx.WriteError(w, status, e.Code, e.Message)
		return
	}
	httpx.WriteInternal(w)
}
func (h *HackHandler) setChecklistNotNeeded(w http.ResponseWriter, r *http.Request) {
	marked := r.Method == http.MethodPost
	h.writeChecklist(w, r, "", &marked)
}
func (h *HackHandler) acknowledgeChecklist(w http.ResponseWriter, r *http.Request) {
	h.writeChecklist(w, r, "", nil)
}
func (h *HackHandler) writeChecklist(w http.ResponseWriter, r *http.Request, version string, notNeeded *bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true)
	if !ok {
		return
	}
	if notNeeded == nil {
		var req struct {
			Version string `json:"version"`
		}
		if !decodeHackJSON(w, r, &req) {
			return
		}
		version = req.Version
	}
	item := r.PathValue("item")
	if err := h.checklistService().Write(r.Context(), a.event.ID, a.user.ID, item, version, notNeeded); err != nil {
		writeChecklistError(w, err)
		return
	}
	role := "organiser"
	if strings.HasPrefix(item, "j.") {
		role = "judge"
	}
	h.pinDue(r.Context(), a.event.ID)
	list, err := h.readChecklist(r.Context(), a.event, a.member, role)
	if err != nil {
		writeChecklistError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"checklist": list})
}
