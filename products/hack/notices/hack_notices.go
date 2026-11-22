package notices

import (
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
)

// Notify people when something waits on them. What each person sees is computed in the events service, so the website's badges and Needs you lines, GET /v1/hack/todos and the AI apps (hack_get_my_todos, hack_get_team_status) always say the same thing. Storage and the email queue: host/repository/hackevents/notices.go and notice_delivery.go.

// getTodos is GET /v1/hack/todos: what waits on the person in every event
// they are in. Events with nothing waiting are left out.
func (h *handler) getTodos(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	todos, on, possible, err := h.Events.Todos(r.Context(), user.ID, h.EntryEvaluator())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := []map[string]any{}
	for _, ev := range todos {
		out = append(out, map[string]any{"slug": ev.Slug, "title": ev.Title, "role": ev.Role, "items": core.NeedsYouView(ev.Items)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": out, "email_updates": on, "email_possible": possible})
}

// markNoticesSeen is POST /v1/hack/events/{slug}/notices/seen: the person
// dismisses their own news lines, the listed ids or all of them.
func (h *handler) markNoticesSeen(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	if a.Member.JoinedAt.IsZero() {
		core.WriteEventNotFound(w)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if len(req.IDs) > 50 {
		httpx.WriteError(w, http.StatusBadRequest, "too_many_ids", "send at most 50 ids")
		return
	}
	for _, id := range req.IDs {
		if !sites.UUIDShape.MatchString(id) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_id", "ids must be notice ids from needs_you")
			return
		}
	}
	n, err := h.Events.MarkNewsSeen(r.Context(), a.Event.ID, a.User.ID, req.IDs)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": n})
}
