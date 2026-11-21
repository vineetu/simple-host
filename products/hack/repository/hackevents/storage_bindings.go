package hackevents

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// StorageBindings contains trusted facts about a site and a caller in its event.
type StorageBindings struct {
	Team, SiteTeam, Stage     sql.NullString
	Organiser, BeforeDeadline bool
}

func ReadStorageBindings(ctx context.Context, q sqlkit.Querier, accountID, siteName, userID string) (StorageBindings, error) {
	var b StorageBindings
	err := q.QueryRowContext(ctx, `
		SELECT CASE WHEN m.role = 'participant' AND m.approval_status = 'approved' THEN mt.slug END,
		       COALESCE(m.role = 'organiser', false), t.slug, e.stage,
		       (`+EffectiveDeadlineSQL+` IS NULL OR clock_timestamp() < `+EffectiveDeadlineSQL+`)
		  FROM events e
		  LEFT JOIN event_teams t ON t.event_id = e.id AND t.slug = $2
		  LEFT JOIN event_members m ON m.event_id = e.id AND m.user_id = NULLIF($3, '')::uuid
		  LEFT JOIN event_teams mt ON mt.event_id = e.id AND mt.id = m.team_id
		 WHERE e.account_id = $1::uuid AND (t.id IS NOT NULL OR e.id::text = $2)`, accountID, siteName, userID).
		Scan(&b.Team, &b.Organiser, &b.SiteTeam, &b.Stage, &b.BeforeDeadline)
	if errors.Is(err, sql.ErrNoRows) {
		return StorageBindings{}, nil
	}
	return b, err
}
