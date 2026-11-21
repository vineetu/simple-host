package hackteams

import (
	"context"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// LockTeamRow locks one team row FOR UPDATE in the caller's transaction (no
// error when the team is gone). Every workflow that changes a team or what is
// stored for it takes this lock, after the event and member rows.
func LockTeamRow(ctx context.Context, q sqlkit.Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, teamID)
	return err
}

// LockEventTeams locks every team of the event FOR UPDATE in id order, the
// order every whole-event workflow uses.
func LockEventTeams(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id = $1 ORDER BY id FOR UPDATE`, eventID)
	return err
}

// TeamExists is whether the team row is still there.
func TeamExists(ctx context.Context, q sqlkit.Querier, teamID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, teamID).Scan(&ok)
	return ok, err
}

// CountEventTeams is how many teams the event has.
func CountEventTeams(ctx context.Context, q sqlkit.Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_teams WHERE event_id = $1`, eventID).Scan(&n)
	return n, err
}

// CountTeamsByEvent is how many teams each of these events has; an event
// with none is absent.
func CountTeamsByEvent(ctx context.Context, q sqlkit.Querier, eventIDs []string) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT event_id, COUNT(*) FROM event_teams WHERE event_id = ANY($1::uuid[]) GROUP BY event_id`, pq.Array(eventIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
