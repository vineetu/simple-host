package hackhttp

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/workers"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

type (
	hackContent      = events.Content
	hackSponsor      = events.Sponsor
	hackFAQ          = events.FAQ
	hackScheduleItem = events.ScheduleItem
)

// hackContentView is what GET and PUT /content answer: the stored document
// plus the fixed rows derived from the event's settings (hack_schedule.go).
type hackContentView struct {
	hackContent
	FixedSchedule []hackFixedRow `json:"fixed_schedule"`
}

func (h *publicEventHTTP) contentView(ctx context.Context, a hackAccess, c hackContent) (hackContentView, error) {
	organiser := a.member.Role == "organiser" || a.admin
	if !organiser {
		c.Schedule = hackPublicSchedule(c.Schedule)
	}
	fixed, err := h.fixedScheduleFor(ctx, a.event, &a.member, organiser)
	return hackContentView{hackContent: c, FixedSchedule: fixed}, err
}

func (h *publicEventHTTP) getContent(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	c, err := h.events.EventContent(r.Context(), a.event.ID)
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

func (h *publicEventHTTP) putContent(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var in hackContent
	if !httpx.DecodeJSON(w, r, hackMaxBody, &in) {
		return
	}
	c, err := h.events.SaveContent(r.Context(), a.event, in)
	if err != nil {
		writeHackError(w, err)
		return
	}
	view, err := h.contentView(r.Context(), a, c)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *publicEventHTTP) getAnnouncements(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	items, err := h.events.ListAnnouncements(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"announcements": items})
}

func (h *publicEventHTTP) postAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_ended", "this event has ended")
		return
	}
	var req struct {
		Title             string `json:"title"`
		Body              string `json:"body"`
		EmailParticipants bool   `json:"email_participants"`
		Pinned            bool   `json:"pinned"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	title, body, err := events.AnnouncementText(req.Title, req.Body)
	if err != nil {
		writeHackError(w, err)
		return
	}
	if _, ok := noticeSender(h.mailer); req.EmailParticipants && !ok {
		httpx.WriteError(w, http.StatusServiceUnavailable, "email_unavailable", "participant email is unavailable")
		return
	}
	item, queued, organiserCopied, err := h.events.PostAnnouncement(r.Context(), a.event.ID, title, body, req.EmailParticipants, req.Pinned, a.user.ID)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"announcement": item, "emails_queued": queued, "organiser_copied": organiserCopied})
}

// previewAnnouncement renders announcement text exactly as participants will
// see it, so the composer never carries a second renderer. Nothing is stored.
func (h *publicEventHTTP) previewAnnouncement(w http.ResponseWriter, r *http.Request) {
	if _, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser"); !ok {
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	// The same text rules as posting, except that an empty message previews
	// as nothing.
	body, valid := checkHackText(w, req.Body, "body", 0, 3000)
	if !valid {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"body_html": events.RenderAnnouncement(body)})
}

type contentDeliveryOwner struct {
	h *HackHandler
	*workers.Runner
	tracker *workers.Tracker
	once    sync.Once
}

func (h *HackHandler) ContentDeliveryOwner() workers.Owner {
	h.contentOnce.Do(func() {
		t := workers.NewTracker("hack.content_delivery")
		h.contentOwner = &contentDeliveryOwner{h: h, Runner: workers.NewRunner(t), tracker: t}
	})
	return h.contentOwner
}
func (o *contentDeliveryOwner) Start(ctx context.Context) {
	o.once.Do(func() {
		o.Go(ctx, func(ctx context.Context) {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				o.run(ctx)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	})
}
func (o *contentDeliveryOwner) pending(ctx context.Context) (int64, bool) {
	n, err := o.h.events.PendingContentMail(ctx)
	if err != nil {
		o.tracker.Fail("count pending mail", err)
		return 0, false
	}
	o.tracker.SetPending(n)
	return n, true
}
func (o *contentDeliveryOwner) run(ctx context.Context) {
	o.tracker.Attempt()
	o.pending(ctx)
	// Only replace the mail dependency in this local handler view.
	h, mailer := o.h, o.h.mailer
	if sender, ok := mailer.(interface {
		SendNotice(string, string, string) error
	}); ok {
		mailer = events.TrackContentSender(mailer, sender, o.tracker)
	}
	n, err := h.deliverContentMail(ctx, mailer)
	o.record("deliver content mail", n, err)
	n, err = h.events.QueueEntryReminders(ctx)
	o.record("queue entry reminders", n, err)
	n, err = h.events.QueueJudgeReminders(ctx, h.entries)
	o.record("queue judge reminders", n, err)
	n, err = h.events.PruneNotices(ctx)
	o.record("prune notices", n, err)
	n, err = h.deliverNoticeMailOnly(ctx, mailer)
	o.record("deliver notice mail", n, err)
	o.pending(ctx)
}

func (o *contentDeliveryOwner) record(op string, n int64, err error) {
	if n > 0 {
		o.tracker.Progress()
	}
	if err != nil {
		o.tracker.Fail(op, err)
	}
}

// deliverContentMail sends due announcement mail through mailer.
func (h *HackHandler) deliverContentMail(ctx context.Context, mailer events.Mailer) (int64, error) {
	sender, ok := mailer.(interface {
		SendNotice(string, string, string) error
	})
	if !ok {
		return 0, nil
	}
	return h.events.DeliverContentMail(ctx, sender, h.links.siteDomain, h.links.publicBaseURL)
}

// Editing an announcement changes the page only, without queueing mail.
func (h *publicEventHTTP) changeAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := organiserWrite(w, r, h.members)
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
	if r.Method == http.MethodPut && !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	title, body, err := events.AnnouncementChange(req.Title, req.Body, req.Pinned)
	if r.Method == http.MethodPut && err != nil {
		writeHackError(w, err)
		return
	}
	if err := h.events.ChangeAnnouncement(r.Context(), a.event.ID, id, title, body, req.Pinned, r.Method == http.MethodDelete); err != nil {
		writeHackError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
