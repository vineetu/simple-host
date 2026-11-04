package hackteams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
)

// OrphanTeamSites lists live sites of events' holding accounts whose team no
// longer exists (the team was removed or emptied): the sweep takes them to
// Recently deleted.
func OrphanTeamSites(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, accountID string) ([]model.Site, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.active_version
		  FROM sites s JOIN events e ON e.account_id = s.user_id
		 WHERE s.deleted_at IS NULL
		   AND ($1 = '' OR s.user_id::text = $1)
		   AND s.name <> e.id::text
		   AND NOT EXISTS (SELECT 1 FROM event_teams t WHERE t.event_id = e.id AND t.slug = s.name)`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Site
	for rows.Next() {
		var s model.Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.ActiveVersion); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
