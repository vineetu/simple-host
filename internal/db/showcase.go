package db

import (
	"context"
	"database/sql"
)

type ShowcasePreference struct {
	Pinned bool `json:"pinned"`
	Order  int  `json:"order"`
}

func GetShowcaseBio(ctx context.Context, q Querier, userID string) (string, error) {
	var bio string
	err := q.QueryRowContext(ctx, `SELECT showcase_bio FROM users WHERE id=$1`, userID).Scan(&bio)
	return bio, err
}

func SetShowcaseBio(ctx context.Context, q Querier, userID, bio string) error {
	res, err := q.ExecContext(ctx, `UPDATE users SET showcase_bio=$2 WHERE id=$1`, userID, bio)
	return oneRow(res, err)
}

// Empty userID is the operator's all-sites listing; otherwise only that owner's preferences.
func ShowcasePreferences(ctx context.Context, q *sql.DB, userID string) (map[string]ShowcasePreference, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,showcase_pinned,showcase_order FROM sites WHERE deleted_at IS NULL AND ($1='' OR user_id::text=$1)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ShowcasePreference{}
	for rows.Next() {
		var id string
		var p ShowcasePreference
		if err := rows.Scan(&id, &p.Pinned, &p.Order); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

func GetShowcasePreference(ctx context.Context, q Querier, userID, name string) (ShowcasePreference, error) {
	var p ShowcasePreference
	err := q.QueryRowContext(ctx, `SELECT showcase_pinned,showcase_order FROM sites WHERE user_id=$1 AND name=$2 AND deleted_at IS NULL`, userID, name).Scan(&p.Pinned, &p.Order)
	return p, err
}

func SetShowcasePreference(ctx context.Context, q Querier, userID, name string, pinned *bool, order *int) (ShowcasePreference, error) {
	var p ShowcasePreference
	err := q.QueryRowContext(ctx, `UPDATE sites SET showcase_pinned=COALESCE($3,showcase_pinned),showcase_order=COALESCE($4,showcase_order),updated_at=now() WHERE user_id=$1 AND name=$2 AND deleted_at IS NULL RETURNING showcase_pinned,showcase_order`, userID, name, pinned, order).Scan(&p.Pinned, &p.Order)
	return p, err
}
