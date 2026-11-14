package hackevents

import (
	"context"
	"encoding/json"
	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// HackNotificationSite resolves the person's team site or organiser website.
// Event websites use an event slug, never a team selection or holding account.
func HackNotificationSiteID(ctx context.Context, q sqlkit.Querier, member, name, event string, teamEvent ...string) (string, error) {
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
		return "", err
	}
	return id, nil
}

func SiteEventSlug(ctx context.Context, q sqlkit.Querier, account string) (string, error) {
	var slug string
	err := q.QueryRowContext(ctx, `SELECT slug FROM events WHERE account_id=$1`, account).Scan(&slug)
	return slug, err
}

// NotificationLabels returns labels only for the group's own website, not team sites.
func NotificationLabels(ctx context.Context, q sqlkit.Querier, siteIDs []string) (map[string]string, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT COALESCE(jsonb_object_agg(s.id::text,e.slug), '{}'::jsonb) FROM sites s JOIN events e ON e.account_id=s.user_id WHERE e.id::text=s.name AND s.id=ANY($1::uuid[])`, pq.Array(siteIDs)).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var labels map[string]string
	err = json.Unmarshal(raw, &labels)
	return labels, err
}
