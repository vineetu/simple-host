package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type EventContent struct {
	Sponsors []byte
	FAQ      []byte
	Schedule []byte
}

func GetEventContent(ctx context.Context, q Querier, eventID string) (EventContent, error) {
	c := EventContent{Sponsors: []byte("[]"), FAQ: []byte("[]"), Schedule: []byte("[]")}
	err := q.QueryRowContext(ctx, `SELECT sponsors, faq, schedule FROM event_content WHERE event_id = $1`, eventID).Scan(&c.Sponsors, &c.FAQ, &c.Schedule)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	return c, err
}

func PutEventContent(ctx context.Context, q Querier, eventID string, c EventContent) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_content (event_id, sponsors, faq, schedule)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO UPDATE SET sponsors = EXCLUDED.sponsors, faq = EXCLUDED.faq,
		schedule = EXCLUDED.schedule, updated_at = now()`, eventID, string(c.Sponsors), string(c.FAQ), string(c.Schedule))
	return err
}

type EventAnnouncement struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func ListEventAnnouncements(ctx context.Context, q Querier, eventID string) ([]EventAnnouncement, error) {
	rows, err := queryContext(ctx, q, `SELECT id, title, body, created_at FROM event_announcements
		WHERE event_id = $1 ORDER BY created_at DESC, id DESC LIMIT 50`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventAnnouncement{}
	for rows.Next() {
		var a EventAnnouncement
		if err := rows.Scan(&a.ID, &a.Title, &a.Body, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func CreateEventAnnouncement(ctx context.Context, tx *sql.Tx, eventID, title, body string, emailParticipants bool) (EventAnnouncement, int64, error) {
	var a EventAnnouncement
	err := tx.QueryRowContext(ctx, `INSERT INTO event_announcements (event_id, title, body)
		VALUES ($1, $2, $3) RETURNING id, title, body, created_at`, eventID, title, body).
		Scan(&a.ID, &a.Title, &a.Body, &a.CreatedAt)
	if err != nil || !emailParticipants {
		return a, 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO event_announcement_deliveries (announcement_id, user_id, recipient)
		SELECT $1, m.user_id, u.username FROM event_members m JOIN users u ON u.id = m.user_id
		WHERE m.event_id = $2 AND m.role = 'participant' AND u.username LIKE '%@%'`, a.ID, eventID)
	if err != nil {
		return a, 0, err
	}
	n, err := res.RowsAffected()
	return a, n, err
}

func QueueEntryReceipt(ctx context.Context, q Querier, eventID, teamID, userID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_entry_receipts (event_id, team_id, user_id, recipient)
		SELECT $1, $2, $3, username FROM users WHERE id = $3 AND username LIKE '%@%'
		ON CONFLICT (event_id, team_id) DO NOTHING`, eventID, teamID, userID)
	return err
}

type EventMail struct {
	Kind, EventID, TeamID, AnnouncementID, Recipient, EventTitle, EventSlug, TeamName, Title, Body string
}

// NextEventMail holds one pending row until the caller commits after sending.
// SKIP LOCKED lets separate app processes dispatch without sending a row twice.
func NextEventMail(ctx context.Context, tx *sql.Tx) (EventMail, error) {
	var m EventMail
	err := tx.QueryRowContext(ctx, `SELECT 'announcement', d.announcement_id, '', a.event_id,
		d.recipient, e.title, e.slug, '', a.title, a.body
		FROM event_announcement_deliveries d JOIN event_announcements a ON a.id = d.announcement_id
		JOIN events e ON e.id = a.event_id
		WHERE d.sent_at IS NULL ORDER BY a.created_at, d.user_id LIMIT 1 FOR UPDATE OF d SKIP LOCKED`).
		Scan(&m.Kind, &m.AnnouncementID, &m.TeamID, &m.EventID, &m.Recipient, &m.EventTitle, &m.EventSlug, &m.TeamName, &m.Title, &m.Body)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	err = tx.QueryRowContext(ctx, `SELECT 'receipt', '', r.team_id, r.event_id,
		r.recipient, e.title, e.slug, t.name, '', ''
		FROM event_entry_receipts r JOIN events e ON e.id = r.event_id
		JOIN event_teams t ON t.id = r.team_id
		WHERE r.sent_at IS NULL ORDER BY t.created_at, r.team_id LIMIT 1 FOR UPDATE OF r SKIP LOCKED`).
		Scan(&m.Kind, &m.AnnouncementID, &m.TeamID, &m.EventID, &m.Recipient, &m.EventTitle, &m.EventSlug, &m.TeamName, &m.Title, &m.Body)
	return m, err
}

func MarkEventMailSent(ctx context.Context, tx *sql.Tx, m EventMail) error {
	var err error
	if m.Kind == "announcement" {
		_, err = tx.ExecContext(ctx, `UPDATE event_announcement_deliveries SET sent_at = now()
			WHERE announcement_id = $1 AND recipient = $2`, m.AnnouncementID, m.Recipient)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE event_entry_receipts SET sent_at = now()
			WHERE event_id = $1 AND team_id = $2`, m.EventID, m.TeamID)
	}
	return err
}
