package hackhttp

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"github.com/vsriram/simple-host/internal/workers"
)

type hackSponsor struct {
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	LogoData string `json:"logo_data"`
	URL      string `json:"url"`
}

type hackFAQ struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type hackScheduleItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	StartAt     string `json:"start_at"`
	EndAt       string `json:"end_at"`
	// Prompt is the forward move this row lines up with (hackScheduleMoves),
	// or empty. Organisers only: hackPublicSchedule drops it for everyone else.
	Prompt string `json:"prompt,omitempty"`
	// Fixed is never stored. A fixed row sent back in a PUT (a client that
	// wrote back what it read) is dropped: fixed rows come from settings.
	Fixed bool `json:"fixed,omitempty"`
}

type hackContent struct {
	Sponsors []hackSponsor      `json:"sponsors"`
	FAQ      []hackFAQ          `json:"faq"`
	Schedule []hackScheduleItem `json:"schedule"`
}

// hackContentView is what GET and PUT /content answer: the stored document
// plus the fixed rows derived from the event's settings (hack_schedule.go).
type hackContentView struct {
	hackContent
	FixedSchedule []hackFixedRow `json:"fixed_schedule"`
}

func (h *HackHandler) contentView(ctx context.Context, a hackAccess, c hackContent) (hackContentView, error) {
	organiser := a.member.Role == "organiser" || a.admin
	if !organiser {
		c.Schedule = hackPublicSchedule(c.Schedule)
	}
	fixed, err := h.fixedScheduleFor(ctx, a.event, &a.member, organiser)
	return hackContentView{hackContent: c, FixedSchedule: fixed}, err
}

func (h *HackHandler) registerContent(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("PUT /v1/hack/events/{slug}/announcements/{announcement}", wrap(h.changeAnnouncement))
	mux.Handle("DELETE /v1/hack/events/{slug}/announcements/{announcement}", wrap(h.changeAnnouncement))
	mux.Handle("GET /v1/hack/events/{slug}/content", wrap(h.getContent))
	mux.Handle("PUT /v1/hack/events/{slug}/content", wrap(h.putContent))
	mux.Handle("GET /v1/hack/events/{slug}/announcements", wrap(h.getAnnouncements))
	mux.Handle("POST /v1/hack/events/{slug}/announcements", wrap(h.postAnnouncement))
}

func readHackContent(ctx context.Context, q *sql.DB, eventID string) (hackContent, error) {
	raw, err := events.ReadContent(ctx, q, eventID)
	c := hackContent{Sponsors: []hackSponsor{}, FAQ: []hackFAQ{}, Schedule: []hackScheduleItem{}}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw.Sponsors, &c.Sponsors); err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw.FAQ, &c.FAQ); err != nil {
		return c, err
	}
	err = json.Unmarshal(raw.Schedule, &c.Schedule)
	return c, err
}

func (h *HackHandler) getContent(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	c, err := readHackContent(r.Context(), h.database, a.event.ID)
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

func cleanLogo(s string) bool {
	if s == "" {
		return true
	}
	const prefix = "data:image/"
	if !strings.HasPrefix(s, prefix) || len(s) > 180000 {
		return false
	}
	i := strings.Index(s, ";base64,")
	if i < 0 || (s[len(prefix):i] != "png" && s[len(prefix):i] != "jpeg" && s[len(prefix):i] != "webp") {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(s[i+8:])
	return err == nil && len(b) > 0 && len(b) <= 128<<10 && http.DetectContentType(b) == "image/"+s[len(prefix):i]
}

func (h *HackHandler) putContent(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_ended", "this event has ended")
		return
	}
	var c hackContent
	if !decodeHackJSON(w, r, &c) {
		return
	}
	editable := []hackScheduleItem{}
	for _, s := range c.Schedule {
		if !s.Fixed {
			editable = append(editable, s)
		}
	}
	c.Schedule = editable
	if len(c.Sponsors) > 20 || len(c.FAQ) > 30 || len(c.Schedule) > 100 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_content", "too many items")
		return
	}
	for i := range c.Sponsors {
		s := &c.Sponsors[i]
		var valid bool
		if s.Name, valid = checkHackLine(w, s.Name, "sponsor name", 1, 100); !valid {
			return
		}
		if s.Tier, valid = checkHackLine(w, s.Tier, "sponsor tier", 0, 60); !valid {
			return
		}
		if s.URL != "" {
			u, err := url.Parse(s.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(s.URL) > 500 {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_sponsor_url", "use an HTTPS sponsor website")
				return
			}
		}
		if !cleanLogo(s.LogoData) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_logo", "use a PNG, JPEG or WebP image under 128 KB")
			return
		}
	}
	for i := range c.FAQ {
		f := &c.FAQ[i]
		var valid bool
		if f.Question, valid = checkHackLine(w, f.Question, "question", 1, 200); !valid {
			return
		}
		if f.Answer, valid = checkHackText(w, f.Answer, "answer", 1, 2000); !valid {
			return
		}
	}
	loc, err := time.LoadLocation(a.event.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	for i := range c.Schedule {
		s := &c.Schedule[i]
		var valid bool
		if s.Title, valid = checkHackLine(w, s.Title, "schedule title", 1, 120); !valid {
			return
		}
		if s.Description, valid = checkHackText(w, s.Description, "schedule description", 0, 500); !valid {
			return
		}
		start, valid := parseEventTime(w, s.StartAt, loc, "start_at", true)
		if !valid {
			return
		}
		end, valid := parseEventTime(w, s.EndAt, loc, "end_at", true)
		if !valid {
			return
		}
		if !end.Time.After(start.Time) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_schedule", "each schedule item must end after it starts")
			return
		}
		if _, known := hackScheduleMoves[s.Prompt]; s.Prompt != "" && !known {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_schedule_prompt", "prompt must be empty or one of open, building, closed, judging, results, publish_results")
			return
		}
		s.StartAt, s.EndAt = start.Time.UTC().Format(time.RFC3339), end.Time.UTC().Format(time.RFC3339)
	}
	sponsors, _ := json.Marshal(c.Sponsors)
	faq, _ := json.Marshal(c.FAQ)
	schedule, _ := json.Marshal(c.Schedule)
	if err := h.eventsSvc().PutContent(r.Context(), a.event.ID, model.EventContent{Sponsors: sponsors, FAQ: faq, Schedule: schedule}); err != nil {
		writeContentError(w, err)
		return
	}
	view, err := h.contentView(r.Context(), a, c)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *HackHandler) getAnnouncements(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	items, err := h.eventsSvc().ListAnnouncements(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"announcements": items})
}

func (h *HackHandler) postAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
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
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	var valid bool
	if req.Title, valid = checkHackLine(w, req.Title, "title", 1, 120); !valid {
		return
	}
	if req.Body, valid = checkHackText(w, req.Body, "body", 1, 3000); !valid {
		return
	}
	if req.EmailParticipants {
		if _, ok := h.mailer.(interface {
			SendNotice(string, string, string) error
		}); !ok {
			httpx.WriteError(w, http.StatusServiceUnavailable, "email_unavailable", "participant email is unavailable")
			return
		}
		if ready, ok := h.mailer.(interface{ CanSendNotice() bool }); ok && !ready.CanSendNotice() {
			httpx.WriteError(w, http.StatusServiceUnavailable, "email_unavailable", "participant email is unavailable")
			return
		}
	}
	item, queued, organiserCopied, err := h.eventsSvc().PostAnnouncement(r.Context(), a.event.ID, req.Title, req.Body, req.EmailParticipants, a.user.ID)
	if err != nil {
		writeContentError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"announcement": item, "emails_queued": queued, "organiser_copied": organiserCopied})
}

// queueReceipt records the team's first submission. A later edit, or a
// second submit, never sends another one (the receipt table's primary key
// (event_id, team_id) caps it at one per team -- section 4.1: "moves from
// 'first time complete' to 'submitted'").
func (h *HackHandler) queueReceipt(ctx context.Context, ev model.Event, teamID, userID string) {
	h.eventsSvc().QueueReceipt(ctx, ev, teamID, userID)
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
func (h *HackHandler) StartContentDelivery(ctx context.Context) { h.ContentDeliveryOwner().Start(ctx) }
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
	n, err := db.PendingContentMail(ctx, o.h.database)
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
	// Only replace the mail dependency in this local handler view. The service
	// still controls transactions, retries, and the order of all three phases.
	h := &HackHandler{database: o.h.database, mailer: o.h.mailer,
		siteDomain: o.h.siteDomain, publicBaseURL: o.h.publicBaseURL,
		noticeTiming: o.h.noticeTiming, entries: o.h.entries}
	if sender, ok := h.mailer.(interface {
		SendNotice(string, string, string) error
	}); ok {
		h.mailer = trackedContentSender{Sender: h.mailer, notice: sender, tracker: o.tracker}
	}
	n, err := h.deliverContentMail(ctx)
	o.record("deliver content mail", n, err)
	n, err = h.eventsSvc().QueueEntryReminders(ctx)
	o.record("queue entry reminders", n, err)
	n, err = h.eventsSvc().PruneNotices(ctx)
	o.record("prune notices", n, err)
	n, err = h.deliverNoticeMailOnly(ctx)
	o.record("deliver notice mail", n, err)
	o.pending(ctx)
}

type trackedContentSender struct {
	email.Sender
	notice interface {
		SendNotice(string, string, string) error
	}
	tracker *workers.Tracker
}

func (s trackedContentSender) CanSendNotice() bool {
	if ready, ok := s.Sender.(interface{ CanSendNotice() bool }); ok {
		return ready.CanSendNotice()
	}
	return true
}
func (s trackedContentSender) SendNotice(to, subject, body string) error {
	return s.SendNoticeCtx(context.Background(), to, subject, body)
}
func (s trackedContentSender) SendNoticeCtx(ctx context.Context, to, subject, body string) error {
	if err := email.SendNoticeCtx(ctx, s.notice, to, subject, body); err != nil {
		// Provider errors can contain the recipient or credentials. Keep those
		// out of both the status and the existing service log.
		err = contentMailError(err)
		s.tracker.Fail("send mail", err)
		return err
	}
	s.tracker.Progress()
	return nil
}

func (o *contentDeliveryOwner) record(op string, n int64, err error) {
	if n > 0 {
		o.tracker.Progress()
	}
	if err != nil {
		o.tracker.Fail(op, err)
	}
}

func (h *HackHandler) deliverContentMail(ctx context.Context) (int64, error) {
	sender, ok := h.mailer.(interface {
		SendNotice(string, string, string) error
	})
	if !ok {
		return 0, nil
	}
	return h.eventsSvc().DeliverContentMail(ctx, sender, h.siteDomain, h.publicBaseURL)
}

// Editing an announcement changes the page only, without queueing mail.
func (h *HackHandler) changeAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("announcement")
	if !sites.UUIDShape.MatchString(id) {
		httpx.WriteError(w, 404, "announcement_not_found", "announcement not found")
		return
	}
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if r.Method == http.MethodPut {
		if !decodeHackJSON(w, r, &req) {
			return
		}
		var valid bool
		if req.Title, valid = checkHackLine(w, req.Title, "title", 1, 120); !valid {
			return
		}
		if req.Body, valid = checkHackText(w, req.Body, "body", 1, 3000); !valid {
			return
		}
	}
	if err := h.eventsSvc().ChangeAnnouncement(r.Context(), a.event.ID, id, req.Title, req.Body, r.Method == http.MethodDelete); err != nil {
		writeContentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Mail errors may echo recipients or provider credentials; expose only known causes.
func contentMailError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return errors.New("mail provider timeout")
	}
	if err.Error() == "RESEND_API_KEY not configured" {
		return errors.New("mail provider is not configured")
	}
	var status int
	if n, _ := fmt.Sscanf(err.Error(), "resend returned status %d", &status); n == 1 {
		return fmt.Errorf("mail provider returned HTTP %d", status)
	}
	return errors.New("mail provider delivery failed")
}
