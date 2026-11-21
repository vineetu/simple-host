package hackteams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

func LockTeamDeadline(ctx context.Context, q *sql.Tx, teamID string) (sql.NullTime, error) {
	var t sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT deadline_override FROM event_teams WHERE id = $1 FOR UPDATE`, teamID).Scan(&t)
	return t, err
}

func SetTeamDeadline(ctx context.Context, q sqlkit.Querier, teamID string, t sql.NullTime) error {
	var arg any
	if t.Valid {
		arg = t.Time
	}
	_, err := q.ExecContext(ctx, `UPDATE event_teams SET deadline_override = $2 WHERE id = $1`, teamID, arg)
	return err
}

func ParticipantTeamCount(ctx context.Context, q sqlkit.Querier, userID string) (int, error) {
	return hackmembers.Count(ctx, q, hackmembers.Filter{UserID: userID, Role: "participant", OnTeam: true})
}

// ClearPassedTeamDeadlines drops the event's team deadlines that have passed
// (reopening an event gives its teams the event's new deadline).
func ClearPassedTeamDeadlines(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `UPDATE event_teams SET deadline_override = NULL
		 WHERE event_id = $1 AND deadline_override <= clock_timestamp()`, eventID)
	return err
}
