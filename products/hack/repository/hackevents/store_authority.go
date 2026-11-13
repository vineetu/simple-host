package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

func HackSiteOwnerOnSite(ctx context.Context, q sqlkit.Querier, siteID, userID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sites s
			  JOIN events e ON e.account_id = s.user_id
			  JOIN event_teams t ON t.event_id = e.id AND t.slug = s.name
			  JOIN event_members m ON m.event_id = e.id AND m.team_id = t.id
			 WHERE s.id = $1::uuid AND s.deleted_at IS NULL AND s.suspended_at IS NULL
			   AND m.user_id = $2::uuid AND m.role = 'participant' AND m.approval_status = 'approved'
			   AND e.taken_down_at IS NULL AND e.stage NOT IN ('judging', 'results', 'archived') AND t.site_taken_down_at IS NULL
			   AND (`+EffectiveDeadlineSQL+` IS NULL OR clock_timestamp() < `+EffectiveDeadlineSQL+`)
		) OR EXISTS (
			SELECT 1 FROM sites s
			  JOIN events e ON e.account_id = s.user_id AND e.id::text = s.name
			  JOIN event_members m ON m.event_id = e.id
			 WHERE s.id = $1::uuid AND s.deleted_at IS NULL AND s.suspended_at IS NULL
			   AND m.user_id = $2::uuid AND m.role = 'organiser'
			   AND e.taken_down_at IS NULL AND e.stage <> 'archived'
		)`, siteID, userID).Scan(&ok)
	return ok, err
}
