package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
)

// ConnectorTarget stores a connected app's choice. Membership is checked here
// and again by ResolveTeamIdentity when the selected credential is used.
type ConnectorTarget struct{}

func (ConnectorTarget) Selected(ctx context.Context, q sqlkit.Querier, grantID string) (sql.NullString, error) {
	var selected sql.NullString
	err := q.QueryRowContext(ctx, `SELECT team_id FROM hack_grant_teams WHERE grant_id = $1`, grantID).Scan(&selected)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, nil
	}
	return selected, err
}

func (ConnectorTarget) Select(ctx context.Context, q sqlkit.Querier, grantID, userID, teamID string) (bool, error) {
	res, err := q.ExecContext(ctx, `
INSERT INTO hack_grant_teams (grant_id, team_id)
SELECT g.id, t.id
  FROM oauth_grants g
  JOIN event_members m ON m.user_id = g.user_id
  JOIN event_teams t ON t.event_id = m.event_id AND t.id = m.team_id
 WHERE g.id = $1::uuid AND g.user_id = $2::uuid AND g.scope = 'sites events'
   AND t.id = $3::uuid AND m.role = 'participant' AND m.approval_status = 'approved'
ON CONFLICT (grant_id) DO UPDATE SET team_id = EXCLUDED.team_id, selected_at = now()`, grantID, userID, teamID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (ConnectorTarget) Labels(ctx context.Context, q sqlkit.Querier, userID string) (map[string][]model.OAuthConnectionTeam, error) {
	rows, err := q.QueryContext(ctx, `
  SELECT DISTINCT g.client_id, e.title, t.name
  FROM oauth_grants g
  JOIN hack_grant_teams h ON h.grant_id = g.id
  JOIN event_teams t ON t.id = h.team_id
  JOIN events e ON e.id = t.event_id
  WHERE g.user_id = $1
  ORDER BY g.client_id, e.title, t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]model.OAuthConnectionTeam{}
	for rows.Next() {
		var clientID string
		var team model.OAuthConnectionTeam
		if err := rows.Scan(&clientID, &team.Event, &team.Team); err != nil {
			return nil, err
		}
		out[clientID] = append(out[clientID], team)
	}
	return out, rows.Err()
}
