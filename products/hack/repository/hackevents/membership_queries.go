package hackevents

import (
	"context"
	"database/sql"
)

// LockStage locks the event and reads its stage.
func LockStage(ctx context.Context, tx *sql.Tx, eventID string, stage *string) error {
	return tx.QueryRowContext(ctx, `SELECT stage FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(stage)
}

// AdmitPending approves pending participants who joined via link.
func AdmitPending(ctx context.Context, tx *sql.Tx, eventID, userID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE event_members SET approval_status='approved', approval_decided_at=now(), approval_decided_by=$2 WHERE event_id=$1 AND role='participant' AND approval_status='pending' AND joined_via='link'`, eventID, userID)
}

// LockEventShared takes a shared lock on the event.
func LockEventShared(ctx context.Context, tx *sql.Tx, eventID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `SELECT 1 FROM events WHERE id=$1 FOR SHARE`, eventID)
}

// LockTeam locks the team row.
func LockTeam(ctx context.Context, tx *sql.Tx, teamID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, teamID)
}

// Handover updates the event creator.
func Handover(ctx context.Context, tx *sql.Tx, eventID, userID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE events SET created_by = $2, updated_at = now() WHERE id = $1`, eventID, userID)
}

// DeleteInvites deletes the event’s organiser invitations.
func DeleteInvites(ctx context.Context, tx *sql.Tx, eventID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id = $1`, eventID)
}

// TeamExists checks whether the team exists.
func TeamExists(ctx context.Context, tx *sql.Tx, teamID string, exists *bool) error {
	return tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, teamID).Scan(exists)
}

// LockApplicationEvent locks the event and reads its stage, application availability and takedown state.
func LockApplicationEvent(ctx context.Context, tx *sql.Tx, eventID string, stage *string, open, takenDown *bool) error {
	return tx.QueryRowContext(ctx, `SELECT stage, applications_open AND directory_listed, taken_down_at IS NOT NULL FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(stage, open, takenDown)
}
