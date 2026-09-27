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
// The row holds no API key: the admin authenticates via the env ADMIN_API_KEY.
func EnsureAdminUser(ctx context.Context, db *sql.DB) (string, error) {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (username, is_admin) VALUES ('admin', true)
		 ON CONFLICT (username) DO NOTHING`); err != nil {
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
	// By is who sent the item (the address they were signed in with), for
	// the owner's reads only; empty everywhere else.
	By string `json:"by,omitempty"`
}

// authorExpr is who sent an item, as the owner sees it: the address kept
// with it, else (older private items) the server's own _submitted_by stamp.
// An item with no recorded author has none, whatever its data says.
const authorExpr = `CASE WHEN submitted_by IS NOT NULL THEN COALESCE(submitted_email, data->>'_submitted_by', '') ELSE '' END`

// AppendCollectionItemByID appends one item to a site's named collection — a
// single INSERT (O(1), no document rewrite), so high-volume appends stay cheap
// and never clobber. by records who sent it (Actor.ID empty: nobody signed
// in). Returns sql.ErrNoRows if the site_id does not exist.
func AppendCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, data json.RawMessage, by Actor) (CollectionItem, error) {
	const q = `
		INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
		SELECT id, $2, $3::jsonb, $4, $5 FROM sites WHERE id = $1
		RETURNING id, data, created_at`
	var it CollectionItem
	err := db.QueryRowContext(ctx, q, siteID, collection, string(data), nullIfEmpty(by.ID), nullIfEmpty(by.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return it, sql.ErrNoRows
	}
	return it, err
}

// ListCollectionItemsByID returns a list's live items newest-first, paginated
// by id cursor. before == 0 means "from the newest"; otherwise return items
// with id < before. withAuthor fills By (the owner's reads only).
func ListCollectionItemsByID(ctx context.Context, db *sql.DB, siteID, collection string, limit int, before int64, withAuthor bool) ([]CollectionItem, error) {
	q := `
		SELECT id, data, created_at, ` + authorExpr + `
		FROM collection_items
		WHERE site_id = $1
		  AND collection = $2
		  AND deleted_at IS NULL
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
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt, &it.By); err != nil {
			return nil, err
		}
		if !withAuthor {
			it.By = ""
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
	// Deleted counts the items in the list's Recently deleted.
	Deleted int64 `json:"deleted"`
}

// ListCollectionSummariesByID returns every collection that has at least one
// item for siteID (live or recently deleted, so a cleared list can still be
// restored), plus every collection marked private (even while empty, so the
// owner sees the setting took), busiest first. Empty site → empty slice.
func ListCollectionSummariesByID(ctx context.Context, db *sql.DB, siteID string) ([]CollectionSummary, error) {
	const q = `
		WITH counts AS (
			SELECT collection, count(*) FILTER (WHERE deleted_at IS NULL) AS n,
			       max(created_at) FILTER (WHERE deleted_at IS NULL) AS last_at,
			       count(*) FILTER (WHERE deleted_at IS NOT NULL) AS gone
			FROM collection_items
			WHERE site_id = $1
			GROUP BY collection
		), private AS (
			SELECT collection FROM collection_settings WHERE site_id = $1 AND private
		)
		SELECT COALESCE(c.collection, p.collection), COALESCE(c.n, 0), c.last_at, p.collection IS NOT NULL, COALESCE(c.gone, 0)
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
		if err := rows.Scan(&s.Name, &s.Count, &last, &s.Private, &s.Deleted); err != nil {
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
func AppendSubmittedItemByID(ctx context.Context, db *sql.DB, siteID, collection string, data json.RawMessage, submitterID, submitterEmail string) (CollectionItem, error) {
	const q = `
		INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
		SELECT id, $2, $3::jsonb, $4, $5 FROM sites WHERE id = $1
		RETURNING id, data, created_at`
	var it CollectionItem
	err := db.QueryRowContext(ctx, q, siteID, collection, string(data), submitterID, nullIfEmpty(submitterEmail)).Scan(&it.ID, &it.Data, &it.CreatedAt)
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
			WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
		)`
	var ok bool
	err := db.QueryRowContext(ctx, q, siteID, collection).Scan(&ok)
	return ok, err
}

// CollectionNameUsed reports whether the list name was ever used on the site:
// it has items (deleted ones too) or a setting.
func CollectionNameUsed(ctx context.Context, db *sql.DB, siteID, collection string) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM collection_items WHERE site_id = $1 AND collection = $2)
		    OR EXISTS(SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = $2)`, siteID, collection).Scan(&ok)
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
		WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
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

// ForEachCollectionItemByID walks every live item in the collection
// oldest-first, unbounded, with who sent each (By). Used by the owner CSV export; the public paginated list is
// ListCollectionItemsByID. fn is called once per row; a non-nil return
// stops iteration and is returned to the caller.
func ForEachCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, fn func(CollectionItem) error) error {
	q := `
		SELECT id, data, created_at, ` + authorExpr + `
		FROM collection_items
		WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
		ORDER BY id ASC`
	rows, err := db.QueryContext(ctx, q, siteID, collection)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var it CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt, &it.By); err != nil {
			return err
		}
		if err := fn(it); err != nil {
			return err
		}
	}
	return rows.Err()
}

// UpdateCollectionItemByID rewrites one live item's data inside a
// transaction: fn gets the stored JSON and returns the new JSON; the earlier
// data goes to the item's history. Returns sql.ErrNoRows when the item is not
// a live item of that site's collection.
func UpdateCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, id int64, a Actor, fn func(json.RawMessage) (json.RawMessage, error)) (CollectionItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	var old json.RawMessage
	err = tx.QueryRowContext(ctx, `
		SELECT data FROM collection_items
		WHERE id = $1 AND site_id = $2 AND collection = $3 AND deleted_at IS NULL
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
	if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &id, OpEdit, old, a); err != nil {
		return CollectionItem{}, err
	}
	return it, tx.Commit()
}
