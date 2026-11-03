package hackevents

import (
	"context"
	"database/sql"
)

// LockContentStage locks the event while its content is changed.
func LockContentStage(ctx context.Context, tx *sql.Tx, id string, compact bool) (string, error) {
	var stage string
	query := `SELECT stage FROM events WHERE id = $1 FOR UPDATE`
	if compact {
		query = `SELECT stage FROM events WHERE id=$1 FOR UPDATE`
	}
	err := tx.QueryRowContext(ctx, query, id).Scan(&stage)
	return stage, err
}

// ChangeAnnouncement edits or removes one announcement.
func ChangeAnnouncement(ctx context.Context, tx *sql.Tx, eventID, id, title, body string, remove bool) (sql.Result, error) {
	if remove {
		return tx.ExecContext(ctx, `DELETE FROM event_announcements WHERE event_id=$1 AND id=$2`, eventID, id)
	}
	return tx.ExecContext(ctx, `UPDATE event_announcements SET title=$3,body=$4 WHERE event_id=$1 AND id=$2`, eventID, id, title, body)
}
