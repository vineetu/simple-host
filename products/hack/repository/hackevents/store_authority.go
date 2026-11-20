package hackevents

import (
	"context"
	"strings"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// OwnerOnSite narrows who is owner of an event's site for its saved data,
// among those the platform lets act for it: on a team's site a current
// approved teammate until the team's deadline, while neither the event nor
// the site is taken down; on the event website an organiser until the event
// ends.
func OwnerOnSite(ctx context.Context, q sqlkit.Querier, accountID, siteName, userID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM events e
			  JOIN event_teams t ON t.event_id = e.id AND t.slug = $2
			  JOIN event_members m ON m.event_id = e.id AND m.team_id = t.id
			 WHERE e.account_id = $1::uuid
			   AND m.user_id = $3::uuid AND m.role = 'participant' AND m.approval_status = 'approved'
			   AND e.taken_down_at IS NULL AND e.stage NOT IN ('judging', 'results', 'archived') AND t.site_taken_down_at IS NULL
			   AND (`+EffectiveDeadlineSQL+` IS NULL OR clock_timestamp() < `+EffectiveDeadlineSQL+`)
		) OR EXISTS (
			SELECT 1 FROM events e
			  JOIN event_members m ON m.event_id = e.id
			 WHERE e.account_id = $1::uuid AND e.id::text = $2
			   AND m.user_id = $3::uuid AND m.role = 'organiser'
			   AND e.taken_down_at IS NULL AND e.stage <> 'archived'
		)`, accountID, strings.ToLower(siteName), userID).Scan(&ok)
	return ok, err
}
