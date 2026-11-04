package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

// HomeSite loads the selected site, including offline and taken-down rows.
func HomeSite(ctx context.Context, q Querier, userID string) (Site, bool, error) {
	return sites.HomeSite(ctx, q, userID)
}

// SetHomeSite uses one statement so a concurrent delete cannot select a deleted site.
func SetHomeSite(ctx context.Context, q Querier, userID string, name *string) error {
	var res sql.Result
	var err error
	if name == nil {
		res, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1`, userID)
	} else {
		res, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = s.id FROM sites s WHERE users.id = $1 AND s.user_id = users.id AND s.name = $2 AND s.deleted_at IS NULL`, userID, *name)
	}
	return oneRow(res, err)
}
