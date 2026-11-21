package hackteams

import (
	"context"
	"database/sql"
	"slices"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

func TeamRemovalNotice(ctx context.Context, q sqlkit.Querier, eventID, userID string, out *string) error {
	m, err := hackmembers.Member(ctx, q, eventID, userID, hackmembers.NoLock)
	*out = m.TeamRemovalNotice
	return err
}

func LookingFor(ctx context.Context, q sqlkit.Querier, teamID string, out *string) error {
	return q.QueryRowContext(ctx, `SELECT looking_for FROM event_teams WHERE id = $1`, teamID).Scan(out)
}

func AdminSuccessor(ctx context.Context, q sqlkit.Querier, teamID, userID string, out *string) error {
	tenure, err := hackmembers.TeamByTenure(ctx, q, teamID)
	if err != nil {
		return err
	}
	ids := slices.DeleteFunc(tenure, func(id string) bool { return id == userID })
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
