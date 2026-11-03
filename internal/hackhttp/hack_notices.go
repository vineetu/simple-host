package hackhttp

import (
	"context"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/sites"
)

// Notify people when something waits on them (2026-11-02, audit item 7).
// What each person sees is computed in the events service, so the website's
// badges and Needs you lines, GET /v1/hack/todos and the AI apps
// (hack_get_my_todos, hack_get_team_status) always say the same thing.
// Storage and the email queue: internal/db/hack_notices.go.

func (h *HackHandler) registerNotices(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/todos", wrap(h.getTodos))
	mux.Handle("POST /v1/hack/events/{slug}/notices/seen", wrap(h.markNoticesSeen))
}

// getTodos is GET /v1/hack/todos: what waits on the person in every event
// they are in. Events with nothing waiting are left out.
func (h *HackHandler) getTodos(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	todos, on, possible, err := h.eventsSvc().Todos(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := []map[string]any{}
	for _, ev := range todos {
		out = append(out, map[string]any{"slug": ev.Slug, "title": ev.Title, "role": ev.Role, "items": needsYouView(ev.Items)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": out, "email_updates": on, "email_possible": possible})
}

// markNoticesSeen is POST /v1/hack/events/{slug}/notices/seen: the person
// dismisses their own news lines, the listed ids or all of them.
func (h *HackHandler) markNoticesSeen(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	if a.member.JoinedAt.IsZero() {
		writeEventNotFound(w)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if r.ContentLength != 0 && !decodeHackJSON(w, r, &req) {
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
	n, err := h.eventsSvc().MarkNewsSeen(r.Context(), a.event.ID, a.user.ID, req.IDs)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": n})
}

// noticeMailPolicy is the sweep's timing; tests shorten it.
type noticeMailPolicy struct{ delay, window time.Duration }

func (h *HackHandler) noticePolicy() noticeMailPolicy {
	if h.noticeTiming != nil {
		return *h.noticeTiming
	}
	delay, window := events.NoticeMailTiming()
	return noticeMailPolicy{delay: delay, window: window}
}

// deliverNoticeMail sends what is due: per person and kind, one email that
// carries everything of that kind still true now, at most one every 15
// minutes. Runs on the content-mail ticker.
func (h *HackHandler) deliverNoticeMail(ctx context.Context) {
	h.eventsSvc().PruneNotices(ctx)
	sender, ok := h.mailer.(interface {
		SendNotice(string, string, string) error
	})
	if !ok {
		return
	}
	if ready, ok := h.mailer.(interface{ CanSendNotice() bool }); ok && !ready.CanSendNotice() {
		return
	}
	pol := h.noticePolicy()
	h.eventsSvc().DeliverNoticeMail(ctx, sender, pol.delay, pol.window, h.publicBaseURL, h.entries)
}

func (h *HackHandler) needsYou(ctx context.Context, ev model.Event, m model.EventMember) []map[string]any {
	return needsYouView(h.eventsSvc().NeedsYou(ctx, ev, m))
}

func needsYouView(needs []events.Need) []map[string]any {
	items := make([]map[string]any, 0, len(needs))
	for _, n := range needs {
		item := map[string]any{"kind": n.Kind, "id": nil, "count": n.Count, "text": n.Text, "section": n.Section, "at": nil, "dismissable": n.ID != ""}
		if n.ID != "" {
			item["id"] = n.ID
		}
		if !n.At.IsZero() {
			item["at"] = n.At.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items
}
