package hackteams

import (
	"context"
	"database/sql"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
)

func TeamRemovalNotice(ctx context.Context, q sqlkit.Querier, eventID, userID string, out *string) error {
	return q.QueryRowContext(ctx, `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, userID).Scan(out)
}

func LookingFor(ctx context.Context, q sqlkit.Querier, teamID string, out *string) error {
	return q.QueryRowContext(ctx, `SELECT looking_for FROM event_teams WHERE id = $1`, teamID).Scan(out)
}

func AdminSuccessor(ctx context.Context, q sqlkit.Querier, teamID, userID string, out *string) error {
	ids, err := userIDs(ctx, q, `
			SELECT user_id FROM event_members WHERE team_id = $1 AND user_id <> $2
			 ORDER BY team_joined_at NULLS LAST, user_id`, teamID, userID)
	if err != nil {
		return err
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if u, ok := users[id]; ok {
			*out = u.DisplayName.String
			return nil
		}
	}
	return sql.ErrNoRows
}

func PendingRequests(ctx context.Context, q sqlkit.Querier, teamID string, out *int) error {
	return q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_team_requests WHERE team_id = $1 AND kind = 'request'`, teamID).Scan(out)
}
