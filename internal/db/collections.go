package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// EnsureAdminUser makes sure a real `admin` row exists (so the admin identity
// has a genuine UUID and can own sites — the synthetic ID:"admin" used to
// violate the sites.user_id foreign key). Idempotent; returns the admin UUID.
// apiKey is only used on first creation (the admin authenticates via the env
// ADMIN_API_KEY, not this row).
func EnsureAdminUser(ctx context.Context, db *sql.DB, apiKey string) (string, error) {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (username, api_key, is_admin) VALUES ('admin', $1, true)
		 ON CONFLICT (username) DO NOTHING`, apiKey); err != nil {
		return "", err
	}
	var id string
	err := db.QueryRowContext(ctx, `SELECT id::text FROM users WHERE username = 'admin'`).Scan(&id)
	return id, err
}

// CollectionItem is one row in an append-only collection.
type CollectionItem struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// AppendCollectionItem appends one item to a site's named collection — a single
// INSERT (O(1), no document rewrite), so high-volume appends stay cheap and
// never clobber. Returns sql.ErrNoRows if the site doesn't exist.
func AppendCollectionItem(ctx context.Context, db *sql.DB, siteName, collection string, data json.RawMessage) (CollectionItem, error) {
	const q = `
		INSERT INTO collection_items (site_id, collection, data)
		SELECT id, $2, $3::jsonb FROM sites WHERE name = $1
		RETURNING id, data, created_at`
	var it CollectionItem
	err := db.QueryRowContext(ctx, q, siteName, collection, string(data)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, sql.ErrNoRows
	}
	return it, err
}

// AppendCollectionItemByID is the site_id-keyed variant of AppendCollectionItem.
// Returns sql.ErrNoRows if the site_id does not exist.
func AppendCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, data json.RawMessage) (CollectionItem, error) {
	const q = `
		INSERT INTO collection_items (site_id, collection, data)
		SELECT id, $2, $3::jsonb FROM sites WHERE id = $1
		RETURNING id, data, created_at`
	var it CollectionItem
	err := db.QueryRowContext(ctx, q, siteID, collection, string(data)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, sql.ErrNoRows
	}
	return it, err
}

// ListCollectionItems returns items newest-first, paginated by id cursor.
// before == 0 means "from the newest"; otherwise return items with id < before.
func ListCollectionItems(ctx context.Context, db *sql.DB, siteName, collection string, limit int, before int64) ([]CollectionItem, error) {
	const q = `
		SELECT id, data, created_at
		FROM collection_items
		WHERE site_id = (SELECT id FROM sites WHERE name = $1)
		  AND collection = $2
		  AND ($3 = 0 OR id < $3)
		ORDER BY id DESC
		LIMIT $4`
	rows, err := db.QueryContext(ctx, q, siteName, collection, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollectionItem, 0, limit)
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListCollectionItemsByID is the site_id-keyed variant of ListCollectionItems.
// before == 0 means "from the newest"; otherwise return items with id < before.
func ListCollectionItemsByID(ctx context.Context, db *sql.DB, siteID, collection string, limit int, before int64) ([]CollectionItem, error) {
	const q = `
		SELECT id, data, created_at
		FROM collection_items
		WHERE site_id = $1
		  AND collection = $2
		  AND ($3 = 0 OR id < $3)
		ORDER BY id DESC
		LIMIT $4`
	rows, err := db.QueryContext(ctx, q, siteID, collection, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollectionItem, 0, limit)
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
