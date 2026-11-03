package db

import (
	"context"
)

// TODO(4.3): Host dependency
// HackNotificationSite resolves the person's team site or organiser website.
// Event websites use an event slug, never a team selection or holding account.
func HackNotificationSite(ctx context.Context, q Querier, member, name, event string, teamEvent ...string) (Site, error) {
	var id, scope string
	if len(teamEvent) > 0 {
		scope = teamEvent[0]
	}
	err := q.QueryRowContext(ctx, `WITH matching AS (SELECT s.id FROM sites s
 JOIN events e ON e.account_id=s.user_id
 JOIN event_members m ON m.event_id=e.id AND m.user_id=$1
 LEFT JOIN event_teams t ON t.event_id=e.id AND t.slug=s.name
 WHERE s.deleted_at IS NULL AND ($4='' OR e.slug=$4) AND
 (($3='' AND s.name=$2 AND m.role='participant' AND m.approval_status='approved' AND m.team_id=t.id)
 OR ($3<>'' AND e.slug=$3 AND s.name=e.id::text AND m.role='organiser'))) SELECT id FROM matching WHERE (SELECT count(*) FROM matching)=1`, member, name, event, scope).Scan(&id)
	if err != nil {
		return Site{}, err
	}
	return GetSiteByID(ctx, q, id)
}
