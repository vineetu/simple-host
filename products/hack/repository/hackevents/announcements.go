package hackevents

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type EventContent = model.EventContent

func GetEventContent(ctx context.Context, q sqlkit.Querier, eventID string) (EventContent, error) {
	c := EventContent{Sponsors: []byte("[]"), FAQ: []byte("[]"), Schedule: []byte("[]")}
	err := q.QueryRowContext(ctx, `SELECT sponsors, faq, schedule FROM event_content WHERE event_id = $1`, eventID).Scan(&c.Sponsors, &c.FAQ, &c.Schedule)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	return c, err
}

func PutEventContent(ctx context.Context, q sqlkit.Querier, eventID string, c EventContent) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_content (event_id, sponsors, faq, schedule)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO UPDATE SET sponsors = EXCLUDED.sponsors, faq = EXCLUDED.faq,
		schedule = EXCLUDED.schedule, updated_at = now()`, eventID, string(c.Sponsors), string(c.FAQ), string(c.Schedule))
	return err
}

type EventAnnouncement = model.EventAnnouncement

// ListEventAnnouncements reports EmailsSent as deliveries actually sent (not merely queued), from event_announcement_deliveries.
func ListEventAnnouncements(ctx context.Context, q sqlkit.Querier, eventID string) ([]EventAnnouncement, error) {
	rows, err := queryContext(ctx, q, `SELECT a.id, a.title, a.body, a.created_at,
		(SELECT COUNT(*) FROM event_announcement_deliveries d WHERE d.announcement_id = a.id AND d.sent_at IS NOT NULL),
		a.pinned_at IS NOT NULL
		FROM event_announcements a
		WHERE a.event_id = $1 ORDER BY (a.pinned_at IS NULL), a.created_at DESC, a.id DESC LIMIT 50`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventAnnouncement{}
	for rows.Next() {
		var a EventAnnouncement
		if err := rows.Scan(&a.ID, &a.Title, &a.Body, &a.CreatedAt, &a.EmailsSent, &a.Pinned); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAnnouncementTimes is when the latest 20 announcements were posted, newest first, as RFC 3339 UTC strings.
func ListAnnouncementTimes(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	rows, err := queryContext(ctx, q, `SELECT created_at FROM event_announcements WHERE event_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 20`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t.UTC().Format(time.RFC3339Nano))
	}
	return out, rows.Err()
}

// CreateEventAnnouncement queues one delivery row per participant (the
// event's approved participants, from the caller) with an email-shaped
// username, plus one more for organiserUserID.
func CreateEventAnnouncement(ctx context.Context, tx *sql.Tx, eventID, title, body string, emailParticipants bool, organiserUserID string, participants []string) (EventAnnouncement, int64, bool, error) {
	var a EventAnnouncement
	err := tx.QueryRowContext(ctx, `INSERT INTO event_announcements (event_id, title, body)
		VALUES ($1, $2, $3) RETURNING id, title, body, created_at`, eventID, title, body).
		Scan(&a.ID, &a.Title, &a.Body, &a.CreatedAt)
	if err != nil || !emailParticipants {
		return a, 0, false, err
	}
	users, err := accounts.UsersByIDs(ctx, tx, append(participants, organiserUserID))
	if err != nil {
		return a, 0, false, err
	}
	var to, recipients []string
	for id, u := range users {
		if HasAddress(u) {
			to, recipients = append(to, id), append(recipients, u.Username)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO event_announcement_deliveries (announcement_id, user_id, recipient)
		SELECT $1::uuid, x.user_id, x.recipient FROM unnest($2::uuid[], $3::text[]) AS x(user_id, recipient)
		ON CONFLICT (announcement_id, user_id) DO NOTHING`, a.ID, pq.Array(to), pq.Array(recipients)); err != nil {
		return a, 0, false, err
	}
	var queued int64
	var organiserCopied bool
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(bool_or(user_id = $2), false)
		FROM event_announcement_deliveries WHERE announcement_id = $1`, a.ID, organiserUserID).
		Scan(&queued, &organiserCopied)
	return a, queued, organiserCopied, err
}

func SetAnnouncementPin(ctx context.Context, tx *sql.Tx, eventID, id string, pinned bool) (int64, error) {
	if pinned {
		if _, err := tx.ExecContext(ctx, `UPDATE event_announcements SET pinned_at = NULL
			WHERE event_id = $1 AND id <> $2 AND pinned_at IS NOT NULL`, eventID, id); err != nil {
			return 0, err
		}
		res, err := tx.ExecContext(ctx, `UPDATE event_announcements SET pinned_at = COALESCE(pinned_at, now())
			WHERE event_id = $1 AND id = $2`, eventID, id)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	res, err := tx.ExecContext(ctx, `UPDATE event_announcements SET pinned_at = NULL WHERE event_id = $1 AND id = $2`, eventID, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type EventMail = model.EventMail

// NextAnnouncementMail holds one due announcement email until the caller
// commits after sending.
func NextAnnouncementMail(ctx context.Context, tx *sql.Tx) (EventMail, error) {
	var m EventMail
	err := tx.QueryRowContext(ctx, `SELECT 'announcement', d.announcement_id, a.event_id,
		d.recipient, e.title, e.slug, a.title, a.body
		FROM event_announcement_deliveries d JOIN event_announcements a ON a.id = d.announcement_id
		JOIN events e ON e.id = a.event_id
		WHERE d.sent_at IS NULL AND (d.attempted_at IS NULL OR d.attempted_at <= now() - interval '1 minute')
		ORDER BY d.attempted_at NULLS FIRST, a.created_at, d.user_id LIMIT 1 FOR UPDATE OF d SKIP LOCKED`).
		Scan(&m.Kind, &m.AnnouncementID, &m.EventID, &m.Recipient, &m.EventTitle, &m.EventSlug, &m.Title, &m.Body)
	return m, err
}

// DeferAnnouncementMail leaves a failed delivery pending but makes later rows eligible before this one is retried on a future sweep.
func DeferAnnouncementMail(ctx context.Context, tx *sql.Tx, m EventMail) error {
	_, err := tx.ExecContext(ctx, `UPDATE event_announcement_deliveries SET attempted_at = now()
		WHERE announcement_id = $1 AND recipient = $2`, m.AnnouncementID, m.Recipient)
	return err
}

func MarkAnnouncementMailSent(ctx context.Context, tx *sql.Tx, m EventMail) error {
	_, err := tx.ExecContext(ctx, `UPDATE event_announcement_deliveries SET sent_at = now()
		WHERE announcement_id = $1 AND recipient = $2`, m.AnnouncementID, m.Recipient)
	return err
}
