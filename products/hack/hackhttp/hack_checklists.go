package hackhttp

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *views) checklistService() *events.ChecklistService {
	return events.NewChecklist(h.database, h.entries, checklistTargets{h.links})
}

func (h *views) readChecklist(ctx context.Context, ev model.Event, m model.EventMember, role string) (events.Checklist, error) {
	var forward *events.ChecklistNext
	if m.Role == "organiser" && role != "judge" {
		result, err := h.entries.EventResults(ctx, ev.ID)
		if err != nil {
			return events.Checklist{}, err
		}
		participants, _, _, err := h.events.CountEventMembersLookup(ctx, ev)
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

func (h *views) checklistForView(ctx context.Context, ev model.Event, m model.EventMember) any {
	list, err := h.readChecklist(ctx, ev, m, "")
	if err != nil {
		log.Printf("hack: checklist %s: %v", ev.Slug, err)
		return nil
	}
	return list
}

func (h *HackHandler) getChecklist(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	h.links.pinDue(r.Context(), a.event.ID)
	list, err := h.views.readChecklist(r.Context(), a.event, a.member, r.URL.Query().Get("role"))
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"can": h.events.MemberAbilities(r.Context(), a.event.ID, a.member), "checklist": list})
}
func (h *HackHandler) setChecklistNotNeeded(w http.ResponseWriter, r *http.Request) {
	marked := r.Method == http.MethodPost
	h.writeChecklist(w, r, "", &marked)
}
func (h *HackHandler) acknowledgeChecklist(w http.ResponseWriter, r *http.Request) {
	h.writeChecklist(w, r, "", nil)
}
func (h *HackHandler) writeChecklist(w http.ResponseWriter, r *http.Request, version string, notNeeded *bool) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true)
	if !ok {
		return
	}
	if notNeeded == nil {
		var req struct {
			Version string `json:"version"`
		}
		if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
			return
		}
		version = req.Version
	}
	item := r.PathValue("item")
	if err := h.views.checklistService().Write(r.Context(), a.event.ID, a.user.ID, item, version, notNeeded); err != nil {
		writeHackError(w, err)
		return
	}
	role := "organiser"
	if strings.HasPrefix(item, "j.") {
		role = "judge"
	}
	h.links.pinDue(r.Context(), a.event.ID)
	list, err := h.views.readChecklist(r.Context(), a.event, a.member, role)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"can": h.events.MemberAbilities(r.Context(), a.event.ID, a.member), "checklist": list})
}

type checklistTargets struct{ links *siteLinks }

func (t checklistTargets) Resolve(id, slug string, params map[string]string) (events.Link, bool) {
	params["event_url"] = t.links.EventURL(slug)
	return events.ChecklistTargets{}.Resolve(id, slug, params)
}
