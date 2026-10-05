package db

import (
	"context"
	"database/sql"
)

// HomeSite loads the selected site, including offline and taken-down rows.
func HomeSite(ctx context.Context, q Querier, userID string) (Site, bool, error) {
	var id sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT home_site_id FROM users WHERE id = $1`, userID).Scan(&id); err != nil {
		return Site{}, false, err
	}
	if !id.Valid {
		return Site{}, false, nil
	}
	s, err := GetSiteByID(ctx, q, id.String)
	if err == sql.ErrNoRows || (err == nil && s.Deleted) {
		_, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1 AND home_site_id = $2`, userID, id.String)
		return Site{}, false, err
	}
	return s, err == nil, err
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
