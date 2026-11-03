package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
	"log"
)

// PutContent stores the content after locking and checking the event.
func (s *Service) PutContent(ctx context.Context, id string, c db.EventContent) error {
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
	if err = db.PutEventContent(ctx, tx, id, c); err != nil {
		return err
	}
	return tx.Commit()
}

// PostAnnouncement publishes an announcement and queues its mail.
func (s *Service) PostAnnouncement(ctx context.Context, id, title, body string, email bool, userID string) (db.EventAnnouncement, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventAnnouncement{}, 0, false, err
	}
	defer tx.Rollback()
	stage, err := hackevents.LockContentStage(ctx, tx, id, false)
	if err != nil {
		return db.EventAnnouncement{}, 0, false, err
	}
	if stage == "archived" {
		return db.EventAnnouncement{}, 0, false, &Error{Code: "event_ended", Message: "this event has ended"}
	}
	item, queued, copied, err := db.CreateEventAnnouncement(ctx, tx, id, title, body, email, userID)
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
func ReadContent(ctx context.Context, q db.Querier, id string) (db.EventContent, error) {
	return db.GetEventContent(ctx, q, id)
}

// ListAnnouncements reads the event announcements.
func (s *Service) ListAnnouncements(ctx context.Context, id string) ([]db.EventAnnouncement, error) {
	return db.ListEventAnnouncements(ctx, s.db, id)
}

// QueueReceipt queues the first submitted entry receipt.
func (s *Service) QueueReceipt(ctx context.Context, ev db.Event, teamID, userID string) {
	e, err := db.GetEventEntry(ctx, s.db, teamID)
	if err != nil || !e.SubmittedAt.Valid {
		return
	}
	if err := db.QueueEntryReceipt(ctx, s.db, ev.ID, teamID, userID); err != nil {
		log.Printf("hack: queue entry receipt %s: %v", ev.Slug, err)
	}
}

// DeliverContentMail sends due announcements and entry receipts.
func (s *Service) DeliverContentMail(ctx context.Context, sender NoticeSender, siteDomain, publicBaseURL string) {
	for i := 0; i < 100; i++ {
		tx, err := s.db.BeginTx(ctx, nil)
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
			body = m.Body + "\n\nEvent page: https://" + m.EventSlug + "." + siteDomain + "/\n"
		} else {
			subject = m.EventTitle + ": entry received"
			body = fmt.Sprintf("Your team's entry for %s was received.\n\nTeam: %s\nReview it at %s/e/%s/team\n", m.EventTitle, m.TeamName, publicBaseURL, m.EventSlug)
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
