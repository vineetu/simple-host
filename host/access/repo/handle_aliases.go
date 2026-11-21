package repo

import (
	"context"
	"database/sql"
)

// ListHandleAliases returns the account's earlier handles.
func ListHandleAliases(ctx context.Context, database *sql.DB, userID string) ([]string, error) {
	return scanStrings(ctx, database, `SELECT handle FROM handle_aliases WHERE user_id = $1 ORDER BY created_at, handle`, userID)
}
