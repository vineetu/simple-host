package repo

import (
	"context"
	"database/sql"
)

func ResetFixedRetention(ctx context.Context, tx *sql.Tx, id string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE sites SET keep_versions=0 WHERE id=$1`, id)
}
