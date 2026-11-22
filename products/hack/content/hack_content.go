package content

import (
	"context"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// hackContentView is what GET and PUT /content answer: the stored document
// plus the fixed rows derived from the event's settings (hack_schedule.go).
type hackContentView struct {
	events.Content
	FixedSchedule []core.FixedRow `json:"fixed_schedule"`
}

func (h *handler) contentView(ctx context.Context, a core.Access, c events.Content) (hackContentView, error) {
	organiser := a.Member.Role == "organiser" || a.Admin
	if !organiser {
		c.Schedule = core.PublicSchedule(c.Schedule)
	}
	fixed, err := h.FixedScheduleFor(ctx, a.Event, &a.Member, organiser)
	return hackContentView{Content: c, FixedSchedule: fixed}, err
}

func (h *handler) getContent(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	c, err := h.Events.EventContent(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	view, err := h.contentView(r.Context(), a, c)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *handler) putContent(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var in events.Content
	if !httpx.DecodeJSON(w, r, core.MaxBody, &in) {
		return
	}
	c, err := h.Events.SaveContent(r.Context(), a.Event, in)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	view, err := h.contentView(r.Context(), a, c)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *handler) getAnnouncements(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	items, err := h.Events.ListAnnouncements(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"announcements": items})
}

func (h *handler) postAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.Event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_ended", "this event has ended")
		return
	}
	var req struct {
		Title             string `json:"title"`
		Body              string `json:"body"`
		EmailParticipants bool   `json:"email_participants"`
		Pinned            bool   `json:"pinned"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	title, body, err := events.AnnouncementText(req.Title, req.Body)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	if req.EmailParticipants && !h.CanEmail {
		httpx.WriteError(w, http.StatusServiceUnavailable, "email_unavailable", "participant email is unavailable")
		return
	}
	item, queued, organiserCopied, err := h.Events.PostAnnouncement(r.Context(), a.Event.ID, title, body, req.EmailParticipants, req.Pinned, a.User.ID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"announcement": item, "emails_queued": queued, "organiser_copied": organiserCopied})
}

// previewAnnouncement renders announcement text exactly as participants will
// see it, so the composer never carries a second renderer. Nothing is stored.
func (h *handler) previewAnnouncement(w http.ResponseWriter, r *http.Request) {
	if _, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser"); !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	// The same text rules as posting, except that an empty message previews
	// as nothing.
	body, valid := checkText(w, req.Body, "body", 0, 3000)
	if !valid {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"body_html": events.RenderAnnouncement(body)})
}

// Editing an announcement changes the page only, without queueing mail.
func (h *handler) changeAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	id := r.PathValue("announcement")
	if !sites.UUIDShape.MatchString(id) {
		httpx.WriteError(w, 404, "announcement_not_found", "announcement not found")
		return
	}
	var req struct {
		Title  *string `json:"title"`
		Body   *string `json:"body"`
		Pinned *bool   `json:"pinned"`
	}
	if r.Method == http.MethodPut && !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	title, body, err := events.AnnouncementChange(req.Title, req.Body, req.Pinned)
	if r.Method == http.MethodPut && err != nil {
		core.WriteError(w, err)
		return
	}
	if err := h.Events.ChangeAnnouncement(r.Context(), a.Event.ID, id, title, body, req.Pinned, r.Method == http.MethodDelete); err != nil {
		core.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
