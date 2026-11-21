package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"time"

	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	rules "github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

func (s *Service) PutContent(ctx context.Context, id string, c model.EventContent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stage, err := hackevents.LockContentStage(ctx, tx, id, false)
	if err != nil {
		return err
	}
	if stage == "archived" {
		return &Error{Code: "event_ended", Message: "this event has ended"}
	}
	if err = hackevents.PutEventContent(ctx, tx, id, c); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) PostAnnouncement(ctx context.Context, id, title, body string, email, pin bool, userID string) (model.EventAnnouncement, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventAnnouncement{}, 0, false, err
	}
	defer tx.Rollback()
	stage, err := hackevents.LockContentStage(ctx, tx, id, false)
	if err != nil {
		return model.EventAnnouncement{}, 0, false, err
	}
	if stage == "archived" {
		return model.EventAnnouncement{}, 0, false, &Error{Code: "event_ended", Message: "this event has ended"}
	}
	item, queued, copied, err := hackevents.CreateEventAnnouncement(ctx, tx, id, title, body, email, userID)
	if err != nil {
		return item, queued, copied, err
	}
	if pin {
		if _, err = hackevents.SetAnnouncementPin(ctx, tx, id, item.ID, true); err != nil {
			return item, queued, copied, err
		}
		item.Pinned = true
	}
	if err = tx.Commit(); err != nil {
		return item, queued, copied, err
	}
	item.BodyHTML = rules.AnnouncementHTML(item.Body)
	return item, queued, copied, nil
}

func (s *Service) ChangeAnnouncement(ctx context.Context, eventID, id, title, body string, pinned *bool, remove bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stage, err := hackevents.LockContentStage(ctx, tx, eventID, true)
	if err != nil {
		return err
	}
	if stage == "archived" {
		return &Error{Code: "event_ended", Message: "this event has ended"}
	}
	var n int64
	if remove || title != "" || pinned == nil {
		result, err := hackevents.ChangeAnnouncement(ctx, tx, eventID, id, title, body, remove)
		if err != nil {
			return err
		}
		if n, err = result.RowsAffected(); err != nil {
			return err
		}
	}
	if !remove && pinned != nil {
		if n, err = hackevents.SetAnnouncementPin(ctx, tx, eventID, id, *pinned); err != nil {
			return err
		}
	}
	if n == 0 {
		return &Error{Code: "announcement_not_found", Message: "announcement not found"}
	}
	return tx.Commit()
}

type NoticeSender interface {
	SendNotice(string, string, string) error
}

func ReadContent(ctx context.Context, q sqlkit.Querier, id string) (model.EventContent, error) {
	return hackevents.GetEventContent(ctx, q, id)
}

func (s *Service) ListAnnouncements(ctx context.Context, id string) ([]model.EventAnnouncement, error) {
	items, err := hackevents.ListEventAnnouncements(ctx, s.db, id)
	for i := range items {
		items[i].BodyHTML = rules.AnnouncementHTML(items[i].Body)
	}
	return items, err
}

func RenderAnnouncement(body string) string { return rules.AnnouncementHTML(body) }

func (s *Service) QueueReceipt(ctx context.Context, ev model.Event, teamID, userID string) {
	e, err := entryrepo.GetEventEntry(ctx, s.db, teamID)
	if err != nil || !e.SubmittedAt.Valid {
		return
	}
	if err := hackevents.QueueEntryReceipt(ctx, s.db, ev.ID, teamID, userID); err != nil {
		log.Printf("hack: queue entry receipt %s: %v", ev.Slug, err)
	}
}

func (s *Service) DeliverContentMail(ctx context.Context, sender NoticeSender, siteDomain, publicBaseURL string) (written int64, resultErr error) {
	for i := 0; i < 100; i++ {
		more, n, err := s.deliverContentMailOne(ctx, sender, siteDomain, publicBaseURL)
		written += n
		if err != nil || !more {
			return written, err
		}
	}
	return written, nil
}

// deliverContentMailOne claims the next due mail, sends it and records the outcome in the same transaction: sent, or deferred for a retry.
func (s *Service) deliverContentMailOne(ctx context.Context, sender NoticeSender, siteDomain, publicBaseURL string) (bool, int64, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	txCtx, cancelTx := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTx()
	stopCancel := context.AfterFunc(ctx, cancelTx)
	defer stopCancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		log.Printf("hack: content mail: %v", err)
		return false, 0, err
	}
	defer tx.Rollback()
	m, err := hackevents.NextEventMail(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		log.Printf("hack: content mail: %v", err)
		return false, 0, err
	}
	subject, body, htmlBody := contentMailMessage(m, siteDomain, publicBaseURL)
	// Once sending starts, worker cancellation must not roll back a successful provider response before its durable mark is committed.
	if !stopCancel() || ctx.Err() != nil {
		return false, 0, ctx.Err()
	}
	sendErr := sendContentMail(ctx, sender, m.Recipient, subject, body, htmlBody)
	ctx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	stopFinish := context.AfterFunc(ctx, cancelTx)
	defer stopFinish()
	if err := recordContentMail(ctx, tx, m, sendErr); err != nil {
		return false, 0, err
	}
	return true, 1, nil
}

// contentMailMessage writes an announcement or entry receipt's subject, plain body and (announcements only) HTML body.
func contentMailMessage(m hackevents.EventMail, siteDomain, publicBaseURL string) (subject, body, htmlBody string) {
	if m.Kind != "announcement" {
		return m.EventTitle + ": entry received",
			fmt.Sprintf("Your team's entry for %s was received.\n\nTeam: %s\nReview it at %s/e/%s/team\n", m.EventTitle, m.TeamName, publicBaseURL, m.EventSlug), ""
	}
	page := "https://" + m.EventSlug + "." + siteDomain + "/"
	return m.EventTitle + ": " + m.Title,
		rules.AnnouncementText(m.Body) + "\n\nEvent page: " + page + "\n",
		announcementEmailDocument(m.EventTitle, m.Title, m.Body, page)
}

// sendContentMail sends the HTML version when there is one and the sender can, else the plain one.
func sendContentMail(ctx context.Context, sender NoticeSender, to, subject, body, htmlBody string) error {
	if rich, ok := sender.(interface {
		SendRichNoticeCtx(context.Context, string, string, string, string) error
	}); ok && htmlBody != "" {
		return rich.SendRichNoticeCtx(ctx, to, subject, body, htmlBody)
	}
	return sendMailNotice(ctx, sender, to, subject, body)
}

// recordContentMail marks the mail sent, or defers it for a retry when sending failed, and commits.
func recordContentMail(ctx context.Context, tx *sql.Tx, m hackevents.EventMail, sendErr error) error {
	record, step, commit := hackevents.MarkEventMailSent, "mark", "commit"
	if sendErr != nil {
		log.Printf("hack: content mail delivery failed: %v", sendErr)
		record, step, commit = hackevents.DeferEventMail, "defer", "defer commit"
	}
	if err := record(ctx, tx, m); err != nil {
		log.Printf("hack: content mail %s: %v", step, err)
		return err
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: content mail %s: %v", commit, err)
		return err
	}
	return nil
}

func CreateEventAnnouncement(ctx context.Context, q *sql.Tx, eventID, title, body string, email bool, userID string) (model.EventAnnouncement, int64, bool, error) {
	return hackevents.CreateEventAnnouncement(ctx, q, eventID, title, body, email, userID)
}

// Senders that predate the context-aware capability are still supported; the production sender and worker wrapper implement it.
func sendMailNotice(ctx context.Context, sender NoticeSender, to, subject, body string) error {
	if s, ok := sender.(interface {
		SendNoticeCtx(context.Context, string, string, string) error
	}); ok {
		return s.SendNoticeCtx(ctx, to, subject, body)
	}
	return sender.SendNotice(to, subject, body)
}

func announcementEmailDocument(eventTitle, title, body, page string) string {
	const font = "font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;"
	esc := html.EscapeString
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>` +
		`<body style="margin:0;padding:0;background:#f4efdf;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr><td align="center" style="padding:24px 12px;">` +
		`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background:#ffffff;border-radius:12px;"><tr><td style="padding:28px 28px 8px;` + font + `">` +
		`<div style="font-size:13px;color:#615b51;margin:0 0 6px;">` + esc(eventTitle) + `</div>` +
		`<h1 style="font-size:21px;line-height:1.3;margin:0 0 18px;color:#1a1a1a;">` + esc(title) + `</h1>` +
		rules.AnnouncementEmailHTML(body) +
		`</td></tr><tr><td style="padding:4px 28px 28px;` + font + `font-size:13px;color:#615b51;">` +
		`<a href="` + esc(page) + `" style="color:#a33f31;">Event page</a></td></tr></table></td></tr></table></body></html>`
}
