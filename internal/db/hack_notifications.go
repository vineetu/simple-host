package db

import (
	"context"
	"database/sql"
)

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

// HackNotificationAllowed rechecks delivery access without the team deadline.
// New subscriptions use the same write window as storage ownership. Organiser
// deliveries end with the event; team deliveries continue after submission.
func HackNotificationAllowed(ctx context.Context, q Querier, site, member string, creating bool) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sites s
 JOIN users holder ON holder.id=s.user_id
 JOIN events e ON e.account_id=s.user_id
 JOIN event_members m ON m.event_id=e.id AND m.user_id=$2
 JOIN users u ON u.id=m.user_id
 LEFT JOIN event_teams t ON t.event_id=e.id AND t.slug=s.name
 WHERE s.id=$1 AND s.deleted_at IS NULL AND s.suspended_at IS NULL AND s.offline_at IS NULL
 AND holder.suspended_at IS NULL AND u.suspended_at IS NULL AND NOT u.event_account
 AND e.taken_down_at IS NULL AND
 ((m.role='participant' AND m.approval_status='approved' AND m.team_id=t.id AND t.site_taken_down_at IS NULL
 AND (NOT $3 OR (e.stage<>'archived' AND (`+EffectiveDeadlineSQL+` IS NULL OR clock_timestamp()<`+EffectiveDeadlineSQL+`))))
 OR (m.role='organiser' AND s.name=e.id::text AND e.stage<>'archived')))`, site, member, creating).Scan(&ok)
	return ok, err
}

// Lock membership before the site lock, so removal and subscription creation
// cannot cross and leave a subscription behind after membership is lost.
func lockNotificationMember(ctx context.Context, tx *sql.Tx, site, member string) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT m.user_id FROM event_members m
 JOIN events e ON e.id=m.event_id JOIN sites s ON s.user_id=e.account_id
 WHERE s.id=$1 AND m.user_id=$2 FOR SHARE OF m`, site, member).Scan(&id)
}

// HackNotificationContextExists checks access before returning an empty list
// for a team or organiser website that has not been published yet.
func HackNotificationContextExists(ctx context.Context, q Querier, member, name, event, scope string) (bool, error) {
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT count(*) = 1 FROM events e
 JOIN event_members m ON m.event_id=e.id AND m.user_id=$1
 LEFT JOIN event_teams t ON t.event_id=e.id AND t.id=m.team_id
 WHERE ($4='' OR e.slug=$4) AND
 (($3='' AND t.slug=$2 AND m.role='participant' AND m.approval_status='approved')
 OR ($3<>'' AND e.slug=$3 AND m.role='organiser'))`, member, name, event, scope).Scan(&allowed)
	return allowed, err
}
