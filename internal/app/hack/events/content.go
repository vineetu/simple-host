package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	model "github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
	sqlkit "github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// PutContent stores the content after locking and checking the event.
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

// PostAnnouncement publishes an announcement and queues its mail.
func (s *Service) PostAnnouncement(ctx context.Context, id, title, body string, email bool, userID string) (model.EventAnnouncement, int64, bool, error) {
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
	err = tx.Commit()
	return item, queued, copied, err
}

// ChangeAnnouncement edits or deletes an announcement under the event lock.
func (s *Service) ChangeAnnouncement(ctx context.Context, eventID, id, title, body string, remove bool) error {
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
	result, err := hackevents.ChangeAnnouncement(ctx, tx, eventID, id, title, body, remove)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return &Error{Code: "announcement_not_found", Message: "announcement not found"}
	}
	return tx.Commit()
}

// NoticeSender delivers one notification email.
type NoticeSender interface {
	SendNotice(string, string, string) error
}

// ReadContent reads the stored content document.
func ReadContent(ctx context.Context, q sqlkit.Querier, id string) (model.EventContent, error) {
	return hackevents.GetEventContent(ctx, q, id)
}

// ListAnnouncements reads the event announcements.
func (s *Service) ListAnnouncements(ctx context.Context, id string) ([]model.EventAnnouncement, error) {
	return hackevents.ListEventAnnouncements(ctx, s.db, id)
}

// QueueReceipt queues the first submitted entry receipt.
func (s *Service) QueueReceipt(ctx context.Context, ev model.Event, teamID, userID string) {
	e, err := entryrepo.GetEventEntry(ctx, s.db, teamID)
	if err != nil || !e.SubmittedAt.Valid {
		return
	}
	if err := hackevents.QueueEntryReceipt(ctx, s.db, ev.ID, teamID, userID); err != nil {
		log.Printf("hack: queue entry receipt %s: %v", ev.Slug, err)
	}
}

// DeliverContentMail sends due announcements and entry receipts.
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
		tx.Rollback()
		return false, 0, nil
	}
	if err != nil {
		tx.Rollback()
		log.Printf("hack: content mail: %v", err)
		return false, 0, err
	}
	var subject, body string
	if m.Kind == "announcement" {
		subject = m.EventTitle + ": " + m.Title
		body = m.Body + "\n\nEvent page: https://" + m.EventSlug + "." + siteDomain + "/\n"
	} else {
		subject = m.EventTitle + ": entry received"
		body = fmt.Sprintf("Your team's entry for %s was received.\n\nTeam: %s\nReview it at %s/e/%s/team\n", m.EventTitle, m.TeamName, publicBaseURL, m.EventSlug)
	}
	// Once sending starts, worker cancellation must not roll back a
	// successful provider response before its durable mark is committed.
	if !stopCancel() || ctx.Err() != nil {
		return false, 0, ctx.Err()
	}
	sendErr := sendMailNotice(ctx, sender, m.Recipient, subject, body)
	ctx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	stopFinish := context.AfterFunc(ctx, cancelTx)
	defer stopFinish()
	if err := sendErr; err != nil {
		log.Printf("hack: content mail delivery failed: %v", err)
		if deferErr := hackevents.DeferEventMail(ctx, tx, m); deferErr != nil {
			tx.Rollback()
			log.Printf("hack: content mail defer: %v", deferErr)
			return false, 0, deferErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			log.Printf("hack: content mail defer commit: %v", commitErr)
			return false, 0, commitErr
		}
		return true, 1, nil
	}
	if err := hackevents.MarkEventMailSent(ctx, tx, m); err != nil {
		tx.Rollback()
		log.Printf("hack: content mail mark: %v", err)
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: content mail commit: %v", err)
		return false, 0, err
	}
	return true, 1, nil
}

// CreateEventAnnouncement creates an announcement in the caller’s transaction.
func CreateEventAnnouncement(ctx context.Context, q *sql.Tx, eventID, title, body string, email bool, userID string) (model.EventAnnouncement, int64, bool, error) {
	return hackevents.CreateEventAnnouncement(ctx, q, eventID, title, body, email, userID)
}

// Legacy senders remain supported; the production sender and worker wrapper
// implement the context-aware capability.
func sendMailNotice(ctx context.Context, sender NoticeSender, to, subject, body string) error {
	if s, ok := sender.(interface {
		SendNoticeCtx(context.Context, string, string, string) error
	}); ok {
		return s.SendNoticeCtx(ctx, to, subject, body)
	}
	return sender.SendNotice(to, subject, body)
}
