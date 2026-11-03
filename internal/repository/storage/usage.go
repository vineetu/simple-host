package storage

import (
	"context"
	"database/sql"
)

// KVUsage reads the durable bytes used by a site's KV entries.
func KVUsage(ctx context.Context, db *sql.DB, siteID string, used *int64) error {
	return db.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(key)+octet_length(value::text)),0) FROM site_storage_kv WHERE site_id=$1`, siteID).Scan(used)
}
