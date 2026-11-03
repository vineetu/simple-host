package hackevents

import (
	"context"
	"database/sql"
)

// LockStage executes the membership query.
func LockStage(ctx context.Context, tx *sql.Tx, eventID string, stage *string) error {
	return tx.QueryRowContext(ctx, `SELECT stage FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(stage)
}

// AdmitPending executes the membership query.
func AdmitPending(ctx context.Context, tx *sql.Tx, eventID, userID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE event_members SET approval_status='approved', approval_decided_at=now(), approval_decided_by=$2 WHERE event_id=$1 AND role='participant' AND approval_status='pending' AND joined_via='link'`, eventID, userID)
}

// LockEventShared executes the membership query.
func LockEventShared(ctx context.Context, tx *sql.Tx, eventID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `SELECT 1 FROM events WHERE id=$1 FOR SHARE`, eventID)
}

// LockTeam executes the membership query.
func LockTeam(ctx context.Context, tx *sql.Tx, teamID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, teamID)
}

// Handover executes the membership query.
func Handover(ctx context.Context, tx *sql.Tx, eventID, userID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE events SET created_by = $2, updated_at = now() WHERE id = $1`, eventID, userID)
}

// DeleteInvites executes the membership query.
func DeleteInvites(ctx context.Context, tx *sql.Tx, eventID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id = $1`, eventID)
}

// TeamExists executes the membership query.
func TeamExists(ctx context.Context, tx *sql.Tx, teamID string, exists *bool) error {
	return tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, teamID).Scan(exists)
}

// LockApplicationEvent executes the membership query.
func LockApplicationEvent(ctx context.Context, tx *sql.Tx, eventID string, stage *string, open, takenDown *bool) error {
	return tx.QueryRowContext(ctx, `SELECT stage, applications_open AND directory_listed, taken_down_at IS NOT NULL FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(stage, open, takenDown)
}
