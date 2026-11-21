package hackevents

import (
	"context"
	"database/sql"
)

func LockStage(ctx context.Context, tx *sql.Tx, eventID string, stage *string) error {
	return tx.QueryRowContext(ctx, `SELECT stage FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(stage)
}

func LockEventShared(ctx context.Context, tx *sql.Tx, eventID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `SELECT 1 FROM events WHERE id=$1 FOR SHARE`, eventID)
}

func Handover(ctx context.Context, tx *sql.Tx, eventID, userID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE events SET created_by = $2, updated_at = now() WHERE id = $1`, eventID, userID)
}

func DeleteInvites(ctx context.Context, tx *sql.Tx, eventID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id = $1`, eventID)
}

// LockApplicationEvent locks the event and reads its stage, application availability and takedown state.
func LockApplicationEvent(ctx context.Context, tx *sql.Tx, eventID string, stage *string, open, takenDown *bool) error {
	return tx.QueryRowContext(ctx, `SELECT stage, applications_open AND directory_listed, taken_down_at IS NOT NULL FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(stage, open, takenDown)
}

// LockEventRow locks the event row FOR UPDATE (no error when it is gone).
func LockEventRow(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM events WHERE id = $1 FOR UPDATE`, eventID)
	return err
}
