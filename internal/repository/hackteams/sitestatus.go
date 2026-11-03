package hackteams

import (
	"context"
	"database/sql"
	"github.com/lib/pq"
	"time"
)

// Grant is a connector grant row.
type Grant struct {
	UserID, Scope, ClientName string
	SelectedTeamID            sql.NullString
	LastUsedAt                time.Time
}

// EventsGrants reads grants in latest-use order.
func EventsGrants(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, userIDs []string, resource string) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT g.user_id, g.scope, g.selected_team_id, g.last_used_at, c.client_name
		  FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id
		 WHERE g.user_id = ANY($1::uuid[]) AND g.resource = $2
		 ORDER BY g.last_used_at DESC`, pq.Array(userIDs), resource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.UserID, &g.Scope, &g.SelectedTeamID, &g.LastUsedAt, &g.ClientName); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// OtherTeamDisplay reads the selected team's event and name.
func OtherTeamDisplay(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, teamID string) (eventTitle, teamName string, ok bool) {
	err := q.QueryRowContext(ctx, `
		SELECT e.title, t.name FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&eventTitle, &teamName)
	return eventTitle, teamName, err == nil
}
