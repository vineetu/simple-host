package operations

import (
	"context"
	"database/sql"
)

func ResetFixedRetention(ctx context.Context, tx *sql.Tx, id string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE sites SET keep_versions=0 WHERE id=$1`, id)
}
func OperatorHandleAliases(ctx context.Context, database *sql.DB, userID string) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT handle FROM handle_aliases WHERE user_id=$1 ORDER BY created_at,handle`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
