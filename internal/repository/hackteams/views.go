package hackteams

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// TeamRemovalNotice reads a participant's removal notice.
func TeamRemovalNotice(ctx context.Context, q sqlkit.Querier, eventID, userID string, out *string) error {
	return q.QueryRowContext(ctx, `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, userID).Scan(out)
}

// LookingFor reads a team's finding note.
func LookingFor(ctx context.Context, q sqlkit.Querier, teamID string, out *string) error {
	return q.QueryRowContext(ctx, `SELECT looking_for FROM event_teams WHERE id = $1`, teamID).Scan(out)
}

// AdminSuccessor reads the next team administrator.
func AdminSuccessor(ctx context.Context, q sqlkit.Querier, teamID, userID string, out *string) error {
	return q.QueryRowContext(ctx, `
			SELECT display_name FROM event_members WHERE team_id = $1 AND user_id <> $2
			 ORDER BY team_joined_at NULLS LAST, user_id LIMIT 1`, teamID, userID).Scan(out)
}

// PendingRequests reads the team's request count.
func PendingRequests(ctx context.Context, q sqlkit.Querier, teamID string, out *int) error {
	return q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_team_requests WHERE team_id = $1 AND kind = 'request'`, teamID).Scan(out)
}
