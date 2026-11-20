package hackteams

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
)

// LockEventDeadline locks and reads the event deadline.
func LockEventDeadline(ctx context.Context, q *sql.Tx, eventID string) (sql.NullTime, error) {
	var t sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT submission_deadline FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&t)
	return t, err
}

// LockTeamDeadline locks and reads the team's override.
func LockTeamDeadline(ctx context.Context, q *sql.Tx, teamID string) (sql.NullTime, error) {
	var t sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT deadline_override FROM event_teams WHERE id = $1 FOR UPDATE`, teamID).Scan(&t)
	return t, err
}

// SetTeamDeadline writes the team's override.
func SetTeamDeadline(ctx context.Context, q sqlkit.Querier, teamID string, t sql.NullTime) error {
	var arg any
	if t.Valid {
		arg = t.Time
	}
	_, err := q.ExecContext(ctx, `UPDATE event_teams SET deadline_override = $2 WHERE id = $1`, teamID, arg)
	return err
}

// ParticipantTeamCount counts a person's team memberships.
func ParticipantTeamCount(ctx context.Context, q sqlkit.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM event_members m JOIN events e ON e.id = m.event_id
		 WHERE m.user_id = $1 AND m.role = 'participant' AND m.team_id IS NOT NULL`, userID).Scan(&n)
	return n, err
}

// OpenEventSlugs reads events needing certificate requests.
func OpenEventSlugs(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (*sql.Rows, error) {
	return q.QueryContext(ctx, `SELECT slug FROM events WHERE stage IN ('open','building','closed') AND taken_down_at IS NULL`)
}
