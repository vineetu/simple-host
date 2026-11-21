package repo

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// Data's part of erasing an account (host/ops ErasePerson).

// LockSavedData locks the saved data of these sites in the order a save
// takes it (declarations, then items), before the site rows are locked, so
// a save under way finishes first and one that starts later waits.
func LockSavedData(ctx context.Context, tx *sql.Tx, siteIDs []string) error {
	for _, q := range []string{
		`SELECT count(*) FROM (SELECT 1 FROM collection_settings WHERE site_id = ANY($1::uuid[]) ORDER BY site_id, collection FOR UPDATE) x`,
		`SELECT count(*) FROM (SELECT 1 FROM collection_items WHERE site_id = ANY($1::uuid[]) ORDER BY id FOR UPDATE) x`,
	} {
		var n int64
		if err := tx.QueryRowContext(ctx, q, pq.Array(siteIDs)).Scan(&n); err != nil {
			return err
		}
	}
	return nil
}

// EraseSubmittedItems deletes what the person submitted to anyone's list
// (their own sites' items go with the sites). The account row's delete
// would only unlink them (submitted_by SET NULL).
func EraseSubmittedItems(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM collection_items WHERE submitted_by = $1`, userID)
	return err
}
