package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
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
}

type hackContent struct {
	Sponsors []hackSponsor      `json:"sponsors"`
	FAQ      []hackFAQ          `json:"faq"`
	Schedule []hackScheduleItem `json:"schedule"`
}

func (h *HackHandler) registerContent(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/content", wrap(h.getContent))
	mux.Handle("PUT /v1/hack/events/{slug}/content", wrap(h.putContent))
	mux.Handle("GET /v1/hack/events/{slug}/announcements", wrap(h.getAnnouncements))
	mux.Handle("POST /v1/hack/events/{slug}/announcements", wrap(h.postAnnouncement))
}

func readHackContent(ctx context.Context, q db.Querier, eventID string) (hackContent, error) {
	raw, err := db.GetEventContent(ctx, q, eventID)
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
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, c)
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
		writeHackErr(w, http.StatusConflict, "event_ended", "this event has ended")
		return
	}
	var c hackContent
	if !decodeHackJSON(w, r, &c) {
		return
	}
	if len(c.Sponsors) > 20 || len(c.FAQ) > 30 || len(c.Schedule) > 100 {
		writeHackErr(w, http.StatusBadRequest, "invalid_content", "too many items")
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
				writeHackErr(w, http.StatusBadRequest, "invalid_sponsor_url", "use an HTTPS sponsor website")
				return
			}
		}
		if !cleanLogo(s.LogoData) {
			writeHackErr(w, http.StatusBadRequest, "invalid_logo", "use a PNG, JPEG or WebP image under 128 KB")
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
			writeHackErr(w, http.StatusBadRequest, "invalid_schedule", "each schedule item must end after it starts")
			return
		}
		s.StartAt, s.EndAt = start.Time.UTC().Format(time.RFC3339), end.Time.UTC().Format(time.RFC3339)
	}
	sponsors, _ := json.Marshal(c.Sponsors)
	faq, _ := json.Marshal(c.FAQ)
	schedule, _ := json.Marshal(c.Schedule)
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	var stage string
	if err := tx.QueryRowContext(r.Context(), `SELECT stage FROM events WHERE id = $1 FOR UPDATE`, a.event.ID).Scan(&stage); err != nil {
		writeInternal(w)
		return
	}
	if stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_ended", "this event has ended")
		return
	}
	if err := db.PutEventContent(r.Context(), tx, a.event.ID, db.EventContent{Sponsors: sponsors, FAQ: faq, Schedule: schedule}); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *HackHandler) getAnnouncements(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	items, err := db.ListEventAnnouncements(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcements": items})
}

func (h *HackHandler) postAnnouncement(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_ended", "this event has ended")
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
			writeHackErr(w, http.StatusServiceUnavailable, "email_unavailable", "participant email is unavailable")
			return
		}
		if ready, ok := h.mailer.(interface{ CanSendNotice() bool }); ok && !ready.CanSendNotice() {
			writeHackErr(w, http.StatusServiceUnavailable, "email_unavailable", "participant email is unavailable")
			return
		}
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	// Recheck the stage in the same transaction that publishes the message.
	var stage string
	if err := tx.QueryRowContext(r.Context(), `SELECT stage FROM events WHERE id = $1 FOR UPDATE`, a.event.ID).Scan(&stage); err != nil {
		writeInternal(w)
		return
	}
	if stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_ended", "this event has ended")
		return
	}
	item, queued, err := db.CreateEventAnnouncement(r.Context(), tx, a.event.ID, req.Title, req.Body, req.EmailParticipants)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"announcement": item, "emails_queued": queued})
}

// queueReceipt records the first complete entry from a team. A later edit
// changes the entry but does not send another first-submission receipt.
func (h *HackHandler) queueReceipt(ctx context.Context, ev db.Event, teamID, userID string) {
	e, err := db.GetEventEntry(ctx, h.database, teamID)
	if err != nil || !e.Exists || strings.TrimSpace(e.Title) == "" {
		return
	}
	complete, _ := entryCompleteness(entryRequiredList(ev.EntryRequired), e)
	if !complete {
		return
	}
	if err := db.QueueEntryReceipt(ctx, h.database, ev.ID, teamID, userID); err != nil {
		log.Printf("hack: queue entry receipt %s: %v", ev.Slug, err)
	}
}

func (h *HackHandler) StartContentDelivery(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			h.deliverContentMail(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (h *HackHandler) deliverContentMail(ctx context.Context) {
	sender, ok := h.mailer.(interface {
		SendNotice(string, string, string) error
	})
	if !ok {
		return
	}
	for i := 0; i < 100; i++ {
		tx, err := h.database.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("hack: content mail: %v", err)
			return
		}
		m, err := db.NextEventMail(ctx, tx)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return
		}
		if err != nil {
			tx.Rollback()
			log.Printf("hack: content mail: %v", err)
			return
		}
		var subject, body string
		if m.Kind == "announcement" {
			subject = m.EventTitle + ": " + m.Title
			body = m.Body + "\n\nEvent page: " + h.EventURL(m.EventSlug) + "\n"
		} else {
			subject = m.EventTitle + ": entry received"
			body = fmt.Sprintf("Your team's entry for %s was received.\n\nTeam: %s\nReview it at %s/e/%s/team\n", m.EventTitle, m.TeamName, h.publicBaseURL, m.EventSlug)
		}
		if err := sender.SendNotice(m.Recipient, subject, body); err != nil {
			log.Printf("hack: content mail delivery failed: %v", err)
			if deferErr := db.DeferEventMail(ctx, tx, m); deferErr != nil {
				tx.Rollback()
				log.Printf("hack: content mail defer: %v", deferErr)
				return
			}
			if commitErr := tx.Commit(); commitErr != nil {
				log.Printf("hack: content mail defer commit: %v", commitErr)
				return
			}
			continue
		}
		if err := db.MarkEventMailSent(ctx, tx, m); err != nil {
			tx.Rollback()
			log.Printf("hack: content mail mark: %v", err)
			return
		}
		if err := tx.Commit(); err != nil {
			log.Printf("hack: content mail commit: %v", err)
			return
		}
	}
}
