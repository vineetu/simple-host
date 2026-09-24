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

// CollectionSummary is one named collection on a site: how many rows it
// holds, when the most recent write landed (null for a private collection that
// has nothing in it yet) and whether it is private.
type CollectionSummary struct {
	Name    string     `json:"name"`
	Count   int64      `json:"count"`
	LastAt  *time.Time `json:"last_at"`
	Private bool       `json:"private"`
}

// ListCollectionSummariesByID returns every collection that has at least one
// item for siteID, plus every collection marked private (even while empty, so
// the owner sees the setting took), busiest first. Empty site → empty slice.
func ListCollectionSummariesByID(ctx context.Context, db *sql.DB, siteID string) ([]CollectionSummary, error) {
	const q = `
		WITH counts AS (
			SELECT collection, count(*) AS n, max(created_at) AS last_at
			FROM collection_items
			WHERE site_id = $1
			GROUP BY collection
		), private AS (
			SELECT collection FROM collection_settings WHERE site_id = $1 AND private
		)
		SELECT COALESCE(c.collection, p.collection), COALESCE(c.n, 0), c.last_at, p.collection IS NOT NULL
		FROM counts c FULL OUTER JOIN private p ON p.collection = c.collection
		ORDER BY 2 DESC, 1`
	rows, err := db.QueryContext(ctx, q, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollectionSummary, 0)
	for rows.Next() {
		var s CollectionSummary
		var last sql.NullTime
		if err := rows.Scan(&s.Name, &s.Count, &last, &s.Private); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			s.LastAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// IsCollectionPrivate reports whether the owner marked this collection
// private. No settings row means public (the default).
func IsCollectionPrivate(ctx context.Context, db *sql.DB, siteID, collection string) (bool, error) {
	var private bool
	err := db.QueryRowContext(ctx,
		`SELECT private FROM collection_settings WHERE site_id = $1 AND collection = $2`,
		siteID, collection).Scan(&private)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return private, err
}

// SetCollectionPrivate records the owner's privacy choice for one collection.
// The collection need not have any items yet (set it before the form goes live).
func SetCollectionPrivate(ctx context.Context, db *sql.DB, siteID, collection string, private bool) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO collection_settings (site_id, collection, private, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (site_id, collection) DO UPDATE SET private = EXCLUDED.private, updated_at = now()`,
		siteID, collection, private)
	return err
}

// AppendSubmittedItemByID appends to a private collection, recording which
// account submitted it. submitterID comes from the visitor session, never the
// request body.
func AppendSubmittedItemByID(ctx context.Context, db *sql.DB, siteID, collection string, data json.RawMessage, submitterID string) (CollectionItem, error) {
	const q = `
		INSERT INTO collection_items (site_id, collection, data, submitted_by)
		SELECT id, $2, $3::jsonb, $4 FROM sites WHERE id = $1
		RETURNING id, data, created_at`
	var it CollectionItem
	err := db.QueryRowContext(ctx, q, siteID, collection, string(data), submitterID).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, sql.ErrNoRows
	}
	return it, err
}

// CollectionExistsByID reports whether siteID has any rows in collection.
func CollectionExistsByID(ctx context.Context, db *sql.DB, siteID, collection string) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1 FROM collection_items
			WHERE site_id = $1 AND collection = $2
		)`
	var ok bool
	err := db.QueryRowContext(ctx, q, siteID, collection).Scan(&ok)
	return ok, err
}

// ListCollectionKeysByID returns the sorted union of JSON object keys
// across every item in the collection. Non-object values contribute no keys.
func ListCollectionKeysByID(ctx context.Context, db *sql.DB, siteID, collection string) ([]string, error) {
	const q = `
		SELECT DISTINCT k
		FROM collection_items,
		     LATERAL jsonb_object_keys(
		       CASE WHEN jsonb_typeof(data) = 'object' THEN data ELSE '{}'::jsonb END
		     ) AS k
		WHERE site_id = $1 AND collection = $2
		ORDER BY 1`
	rows, err := db.QueryContext(ctx, q, siteID, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ForEachCollectionItemByID walks every item in the collection oldest-first,
// unbounded. Used by the owner CSV export; the public paginated list is
// ListCollectionItemsByID. fn is called once per row; a non-nil return
// stops iteration and is returned to the caller.
func ForEachCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, fn func(CollectionItem) error) error {
	const q = `
		SELECT id, data, created_at
		FROM collection_items
		WHERE site_id = $1 AND collection = $2
		ORDER BY id ASC`
	rows, err := db.QueryContext(ctx, q, siteID, collection)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
			return err
		}
		if err := fn(it); err != nil {
			return err
		}
	}
	return rows.Err()
}

// UpdateCollectionItemByID rewrites one item's data inside a transaction: fn
// gets the stored JSON and returns the new JSON. Returns sql.ErrNoRows when
// the item is not in that site's collection.
func UpdateCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, id int64, fn func(json.RawMessage) (json.RawMessage, error)) (CollectionItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	var old json.RawMessage
	err = tx.QueryRowContext(ctx, `
		SELECT data FROM collection_items
		WHERE id = $1 AND site_id = $2 AND collection = $3
		FOR UPDATE`, id, siteID, collection).Scan(&old)
	if err != nil {
		return CollectionItem{}, err
	}
	next, err := fn(old)
	if err != nil {
		return CollectionItem{}, err
	}
	var it CollectionItem
	err = tx.QueryRowContext(ctx, `
		UPDATE collection_items SET data = $4::jsonb
		WHERE id = $1 AND site_id = $2 AND collection = $3
		RETURNING id, data, created_at`, id, siteID, collection, string(next)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if err != nil {
		return CollectionItem{}, err
	}
	return it, tx.Commit()
}

// DeleteCollectionItemByID removes one item for good. ok=false when it was not
// in that site's collection.
func DeleteCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, id int64) (bool, error) {
	res, err := db.ExecContext(ctx, `
		DELETE FROM collection_items WHERE id = $1 AND site_id = $2 AND collection = $3`, id, siteID, collection)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
