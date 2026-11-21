package hackentries

import (
	"context"
	"database/sql"
	"errors"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// QueueEntryReceipt queues the "entry received" email to the person who
// submitted, when their account has an address.
func QueueEntryReceipt(ctx context.Context, q sqlkit.Querier, eventID, teamID, userID string) error {
	u, err := accounts.UserByID(ctx, q, userID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !hackevents.HasAddress(u) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO event_entry_receipts (event_id, team_id, user_id, recipient)
		VALUES ($1, $2, $3, $4) ON CONFLICT (event_id, team_id) DO NOTHING`, eventID, teamID, userID, u.Username)
	return err
}

// NextReceiptMail holds one due entry receipt until the caller commits after
// sending. The sweep sends one email per transaction, so this is one
// statement reading the event's title and the team's name beside the
// receipt (a listed exception in scripts/split/table-owner-exceptions.txt).
func NextReceiptMail(ctx context.Context, tx *sql.Tx) (model.EventMail, error) {
	m := model.EventMail{Kind: "receipt"}
	err := tx.QueryRowContext(ctx, `SELECT r.team_id, r.event_id, r.recipient, e.title, e.slug, t.name
		FROM event_entry_receipts r JOIN events e ON e.id = r.event_id JOIN event_teams t ON t.id = r.team_id
		WHERE r.sent_at IS NULL AND (r.attempted_at IS NULL OR r.attempted_at <= now() - interval '1 minute')
		ORDER BY r.attempted_at NULLS FIRST, r.event_id, r.team_id LIMIT 1 FOR UPDATE OF r SKIP LOCKED`).
		Scan(&m.TeamID, &m.EventID, &m.Recipient, &m.EventTitle, &m.EventSlug, &m.TeamName)
	return m, err
}

// DeferReceiptMail leaves a failed receipt pending but lets later ones go first.
func DeferReceiptMail(ctx context.Context, tx *sql.Tx, m model.EventMail) error {
	_, err := tx.ExecContext(ctx, `UPDATE event_entry_receipts SET attempted_at = now()
		WHERE event_id = $1 AND team_id = $2`, m.EventID, m.TeamID)
	return err
}

func MarkReceiptMailSent(ctx context.Context, tx *sql.Tx, m model.EventMail) error {
	_, err := tx.ExecContext(ctx, `UPDATE event_entry_receipts SET sent_at = now()
		WHERE event_id = $1 AND team_id = $2`, m.EventID, m.TeamID)
	return err
}

// PendingReceipts counts the entry receipts still to send, delayed retries included.
func PendingReceipts(ctx context.Context, q sqlkit.Querier) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_entry_receipts WHERE sent_at IS NULL`).Scan(&n)
	return n, err
}

// DropLeaver drops what entries keep for someone leaving the event: their
// unsent entry receipts and, when scores is set (an organiser removed as
// co-organiser), the scores they gave.
func DropLeaver(ctx context.Context, q sqlkit.Querier, eventID, userID string, scores bool) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM event_entry_receipts WHERE event_id=$1 AND user_id=$2 AND sent_at IS NULL`, eventID, userID); err != nil || !scores {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, userID)
	return err
}

// ScreenshotBytes is the size of the event's entry screenshots.
func ScreenshotBytes(ctx context.Context, q sqlkit.Querier, eventID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(screenshot)),0) FROM event_entries WHERE event_id=$1`, eventID).Scan(&n)
	return n, err
}
