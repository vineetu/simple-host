package hackteams

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// The two reads here run on every storage request of an event's site
// (app/storagebind), so each is one statement joining the event row and the
// caller's membership beside the team rows: listed exceptions in
// scripts/split/table-owner-exceptions.txt.

// effectiveDeadlineSQL is the team deadline rule over events e and team t.
var effectiveDeadlineSQL = hackevents.EffectiveDeadline("e.submission_deadline")

// SiteBindings are the trusted facts about one of an event account's sites
// (the event's own site, named by the event id, or a team's, named by its
// slug) and the caller in that event.
type SiteBindings struct {
	// Team: the caller's team slug, when they are an approved participant.
	Team, SiteTeam, Stage     sql.NullString
	Organiser, BeforeDeadline bool
}

// ReadSiteBindings reads SiteBindings; all empty for a site no event holds.
// userID may be empty (a visitor not signed in).
func ReadSiteBindings(ctx context.Context, q sqlkit.Querier, accountID, siteName, userID string) (SiteBindings, error) {
	var b SiteBindings
	err := q.QueryRowContext(ctx, `
		SELECT CASE WHEN m.role = 'participant' AND m.approval_status = 'approved' THEN mt.slug END,
		       COALESCE(m.role = 'organiser', false), t.slug, e.stage,
		       (`+effectiveDeadlineSQL+` IS NULL OR clock_timestamp() < `+effectiveDeadlineSQL+`)
		  FROM events e
		  LEFT JOIN event_teams t ON t.event_id = e.id AND t.slug = $2
		  LEFT JOIN event_members m ON m.event_id = e.id AND m.user_id = NULLIF($3, '')::uuid
		  LEFT JOIN event_teams mt ON mt.event_id = e.id AND mt.id = m.team_id
		 WHERE e.account_id = $1::uuid AND (t.id IS NOT NULL OR e.id::text = $2)`, accountID, siteName, userID).
		Scan(&b.Team, &b.Organiser, &b.SiteTeam, &b.Stage, &b.BeforeDeadline)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteBindings{}, nil
	}
	return b, err
}

// OwnerOnSite narrows who is owner of an event's site for its saved data,
// among those the platform lets act for it: an approved teammate while the
// team may still change its site (event running, before judging, nothing
// taken down, before the deadline), or an organiser of the running event on
// the event's own site.
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
			   AND (`+effectiveDeadlineSQL+` IS NULL OR clock_timestamp() < `+effectiveDeadlineSQL+`)
		) OR EXISTS (
			SELECT 1 FROM events e
			  JOIN event_members m ON m.event_id = e.id
			 WHERE e.account_id = $1::uuid AND e.id::text = $2
			   AND m.user_id = $3::uuid AND m.role = 'organiser'
			   AND e.taken_down_at IS NULL AND e.stage <> 'archived'
		)`, accountID, strings.ToLower(siteName), userID).Scan(&ok)
	return ok, err
}
