package checklist

import (
	"net/http"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
)

func (h *handler) getChecklist(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	h.Links.PinDue(r.Context(), a.Event.ID)
	list, err := h.ReadChecklist(r.Context(), a.Event, a.Member, r.URL.Query().Get("role"))
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"can": h.Events.MemberAbilities(r.Context(), a.Event.ID, a.Member), "checklist": list})
}
func (h *handler) setChecklistNotNeeded(w http.ResponseWriter, r *http.Request) {
	marked := r.Method == http.MethodPost
	h.writeChecklist(w, r, "", &marked)
}
func (h *handler) acknowledgeChecklist(w http.ResponseWriter, r *http.Request) {
	h.writeChecklist(w, r, "", nil)
}
func (h *handler) writeChecklist(w http.ResponseWriter, r *http.Request, version string, notNeeded *bool) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true)
	if !ok {
		return
	}
	if notNeeded == nil {
		var req struct {
			Version string `json:"version"`
		}
		if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
			return
		}
		version = req.Version
	}
	item := r.PathValue("item")
	if err := h.ChecklistService().Write(r.Context(), a.Event.ID, a.User.ID, item, version, notNeeded); err != nil {
		core.WriteError(w, err)
		return
	}
	role := "organiser"
	if strings.HasPrefix(item, "j.") {
		role = "judge"
	}
	h.Links.PinDue(r.Context(), a.Event.ID)
	list, err := h.ReadChecklist(r.Context(), a.Event, a.Member, role)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"can": h.Events.MemberAbilities(r.Context(), a.Event.ID, a.Member), "checklist": list})
}
