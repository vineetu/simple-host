package sites

import (
	"context"
	"database/sql"
)

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
