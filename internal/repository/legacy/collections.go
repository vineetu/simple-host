package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// RecordHistory inserts the previous value for a saved-data change.
func RecordHistory(ctx context.Context, q sqlkit.Querier, siteID, kind, name string, itemID *int64, op string, prev json.RawMessage, a model.Actor) error {
	var p any
	if prev != nil {
		p = string(prev)
	}
	var item any
	if itemID != nil {
		item = *itemID
	}
	_, err := q.ExecContext(ctx, `
	INSERT INTO data_history (site_id, kind, name, item_id, op, prev, actor_id, actor_kind, actor_email)
	VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)`, siteID, kind, name, item, op, p, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email))
	return err
}

// NameHoldsItems reports whether the name has more than max live items.
func NameHoldsItems(ctx context.Context, q sqlkit.Querier, siteID, name string, max int) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM (SELECT 1 FROM collection_items
		  WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL LIMIT $3) x`, siteID, name, max+1).Scan(&n)
	return n > max, err
}

// LivePersonEntry finds a live entry by account or normalized email.
func LivePersonEntry(ctx context.Context, q sqlkit.Querier, siteID, name, actorID, baseEmail string, exceptID int64) (int64, bool, error) {
	var id int64
	var mine bool
	err := q.QueryRowContext(ctx, `
		SELECT id, submitted_by IS NOT DISTINCT FROM $3::uuid FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL AND id <> $5
		   AND (submitted_by = $3::uuid OR ($4 <> '' AND regexp_replace(lower(submitted_email), '^([^+@]+)\+[^@]*@', '\1@') = $4))
		 ORDER BY (submitted_by IS NOT DISTINCT FROM $3::uuid) DESC, id DESC LIMIT 1`,
		siteID, name, actorID, baseEmail, exceptID).Scan(&id, &mine)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, mine, err
}

// CountCollectionItems counts live and deleted items under a declaration lock.
func CountCollectionItems(ctx context.Context, tx *sql.Tx, siteID, name string) (live, deleted int64, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE deleted_at IS NULL), count(*) FILTER (WHERE deleted_at IS NOT NULL)
		  FROM collection_items WHERE site_id = $1 AND collection = $2`, siteID, name).Scan(&live, &deleted)
	return
}

// LockDeclaration creates the declaration if needed and locks its row.
func LockDeclaration(ctx context.Context, tx *sql.Tx, siteID, name string) (kind string, private bool, err error) {
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO collection_settings (site_id, collection) VALUES ($1, $2) ON CONFLICT DO NOTHING`, siteID, name); err != nil {
		return
	}
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(kind, ''), private FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&kind, &private)
	return
}

// LockEntryDeclaration reads and locks the declaration used by an append.
func LockEntryDeclaration(ctx context.Context, tx *sql.Tx, siteID, name string) (string, bool, error) {
	var kind string
	var private bool
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(kind, ''), private FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR UPDATE`, siteID, name).Scan(&kind, &private)
	return kind, private, err
}

// InsertEntry inserts a declared collection entry in the caller's transaction.
func InsertEntry(ctx context.Context, tx *sql.Tx, siteID, name string, data json.RawMessage, a model.Actor) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
		INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
		VALUES ($1, $2, $3::jsonb, $4, $5) RETURNING id, data, created_at`,
		siteID, name, string(data), nullIfEmpty(a.ID), nullIfEmpty(a.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	return it, err
}

const authorExpr = `CASE WHEN submitted_by IS NOT NULL THEN COALESCE(submitted_email, data->>'_submitted_by', '') ELSE '' END`

// ListCollectionItemsByID returns a list's live items newest-first, paginated
// by id cursor. before == 0 means "from the newest"; otherwise return items
// with id < before. withAuthor fills By (the owner's reads only).
func ListCollectionItemsByID(ctx context.Context, db *sql.DB, siteID, collection string, limit int, before int64, withAuthor bool) ([]model.CollectionItem, error) {
	q := `
		SELECT id, data, created_at, ` + authorExpr + `, version
		FROM collection_items
		WHERE site_id = $1
		  AND collection = $2
		  AND deleted_at IS NULL
		  AND ($3::bigint = 0 OR id < $3)
		ORDER BY id DESC
		LIMIT $4`
	rows, err := db.QueryContext(ctx, q, siteID, collection, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.CollectionItem, 0, limit)
	for rows.Next() {
		var it model.CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt, &it.By, &it.Version); err != nil {
			return nil, err
		}
		if !withAuthor {
			it.By = ""
		}
		out = append(out, it)
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
// Listmodel.CollectionItemsByID. fn is called once per row; a non-nil return
// stops iteration and is returned to the caller.
func ForEachCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, fn func(model.CollectionItem) error) error {
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
		var it model.CollectionItem
		if err := rows.Scan(&it.ID, &it.Data, &it.CreatedAt, &it.By); err != nil {
			return err
		}
		if err := fn(it); err != nil {
			return err
		}
	}
	return rows.Err()
}

// GetCollectionItemByID reads one item of a site's list, live or deleted (an
// Idempotency-Key retry is answered with the item the first request made).
func GetCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, id int64) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := db.QueryRowContext(ctx, `
		SELECT id, data, created_at FROM collection_items
		 WHERE id = $1 AND site_id = $2 AND collection = $3`, id, siteID, collection).Scan(&it.ID, &it.Data, &it.CreatedAt)
	return it, err
}

// PersonalFewMax: a Personal name's count and size show only from this many
// records up (fewer are reported as "fewer than 3").
const PersonalFewMax = 3

// coarsenPersonal withholds a Personal name's count, size and last save when
// one or two people have a record (and its Recently deleted count likewise).
func coarsenPersonal(s *model.CollectionSummary) {
	if s.Kind != "mine" {
		return
	}
	s.LastAt = nil
	if s.Count > 0 && s.Count < PersonalFewMax {
		s.Count, s.Bytes, s.Few = 0, 0, true
	}
	if s.Deleted > 0 && s.Deleted < PersonalFewMax {
		s.Deleted, s.DeletedFew = 0, true
	}
}

// ListCollectionSummariesByID returns every collection that has at least one
// item for siteID (live or recently deleted, so a cleared list can still be
// restored), plus every collection marked private or declared a kind (even
// while empty, so the owner sees the setting took), busiest first. Empty
// site → empty slice.
func ListCollectionSummariesByID(ctx context.Context, db *sql.DB, siteID string) ([]model.CollectionSummary, error) {
	const q = `
		WITH counts AS (
			SELECT collection, count(*) FILTER (WHERE deleted_at IS NULL) AS n,
			       max(created_at) FILTER (WHERE deleted_at IS NULL) AS last_at,
			       count(*) FILTER (WHERE deleted_at IS NOT NULL) AS gone,
			       COALESCE(sum(octet_length(data::text)) FILTER (WHERE deleted_at IS NULL), 0) AS bytes
			FROM collection_items
			WHERE site_id = $1
			GROUP BY collection
		), settings AS (
			SELECT collection, private, COALESCE(kind, '') AS kind, one_per_person, notify
			  FROM collection_settings WHERE site_id = $1 AND (private OR kind IS NOT NULL)
		)
		SELECT COALESCE(c.collection, p.collection), COALESCE(c.n, 0), c.last_at, COALESCE(p.private, false), COALESCE(c.gone, 0),
		       COALESCE(p.kind, ''), COALESCE(p.one_per_person, false), COALESCE(p.notify, 'off'), COALESCE(c.bytes, 0)
		FROM counts c FULL OUTER JOIN settings p ON p.collection = c.collection
		ORDER BY 2 DESC, 1`
	rows, err := db.QueryContext(ctx, q, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.CollectionSummary, 0)
	for rows.Next() {
		var s model.CollectionSummary
		var last sql.NullTime
		if err := rows.Scan(&s.Name, &s.Count, &last, &s.Private, &s.Deleted, &s.Kind, &s.OnePerPerson, &s.Notify, &s.Bytes); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			s.LastAt = &t
		}
		coarsenPersonal(&s)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ErrKindChanged indicates that a collection declaration changed.
var ErrKindChanged = errors.New("the name's kind changed")

// AppendCollectionItemByID appends one item to a site's named collection — a
// single INSERT (O(1), no document rewrite), so high-volume appends stay cheap
// and never clobber. by records who sent it (Actor.ID empty: nobody signed
// in). It is for names with no kind, whose privacy the caller read as
// private: the declaration row is created when missing (so a first
// declaration running at the same time waits for this append, or this append
// for it) and held (shared) while the item goes in, and ErrKindChanged means
// the name was declared or its privacy changed meanwhile (the item would land
// under rules it was not checked against). Returns sql.ErrNoRows if the
// site_id does not exist.
func AppendCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, private bool, data json.RawMessage, by model.Actor) (model.CollectionItem, error) {
	var it model.CollectionItem
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return it, err
	}
	defer tx.Rollback()
	it, err = AppendCollectionItemTx(ctx, tx, siteID, collection, private, data, by)
	if err != nil {
		return it, err
	}
	return it, tx.Commit()
}

// AppendCollectionItemTx appends a collection item under the caller's transaction.
func AppendCollectionItemTx(ctx context.Context, tx *sql.Tx, siteID, collection string, private bool, data json.RawMessage, by model.Actor) (model.CollectionItem, error) {
	var it model.CollectionItem
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collection_settings (site_id, collection)
		SELECT id, $2 FROM sites WHERE id = $1
		ON CONFLICT DO NOTHING`, siteID, collection); err != nil {
		return it, err
	}
	var kind string
	var nowPrivate bool
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(kind, ''), private FROM collection_settings WHERE site_id = $1 AND collection = $2 FOR SHARE`, siteID, collection).Scan(&kind, &nowPrivate); err != nil {
		return it, err
	}
	if kind != "" || nowPrivate != private {
		return it, ErrKindChanged
	}
	const q = `
		INSERT INTO collection_items (site_id, collection, data, submitted_by, submitted_email)
		SELECT id, $2, $3::jsonb, $4, $5 FROM sites WHERE id = $1
		RETURNING id, data, created_at`
	err := tx.QueryRowContext(ctx, q, siteID, collection, string(data), nullIfEmpty(by.ID), nullIfEmpty(by.Email)).Scan(&it.ID, &it.Data, &it.CreatedAt)
	return it, err
}

// LockCollectionItem reads a live item under a transaction lock.
func LockCollectionItem(ctx context.Context, tx *sql.Tx, siteID, collection string, id int64) (model.CollectionItem, error) {
	var cur model.CollectionItem
	err := tx.QueryRowContext(ctx, `
		SELECT id, data, created_at, version FROM collection_items
		WHERE id = $1 AND site_id = $2 AND collection = $3 AND deleted_at IS NULL
		FOR UPDATE`, id, siteID, collection).Scan(&cur.ID, &cur.Data, &cur.CreatedAt, &cur.Version)
	return cur, err
}

// UpdateCollectionItem writes an item's new data and increments its version.
func UpdateCollectionItem(ctx context.Context, tx *sql.Tx, siteID, collection string, id int64, next json.RawMessage) (model.CollectionItem, error) {
	var it model.CollectionItem
	err := tx.QueryRowContext(ctx, `
		UPDATE collection_items SET data = $4::jsonb, version = version + 1
		WHERE id = $1 AND site_id = $2 AND collection = $3
		RETURNING id, data, created_at, version`, id, siteID, collection, string(next)).Scan(&it.ID, &it.Data, &it.CreatedAt, &it.Version)
	return it, err
}

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

// SiteDataUsage reads a site's live saved-data usage in bytes.
func SiteDataUsage(ctx context.Context, q sqlkit.Querier, siteID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT data_bytes FROM sites WHERE id = $1`, siteID).Scan(&n)
	return n, err
}

// SoftDeleteItem marks one live collection item deleted within a transaction.
func SoftDeleteItem(ctx context.Context, tx *sql.Tx, siteID, collection string, id int64) (sql.Result, error) {
	return tx.ExecContext(ctx, `
		UPDATE collection_items SET deleted_at = now()
		 WHERE id = $1 AND site_id = $2 AND collection = $3 AND deleted_at IS NULL`, id, siteID, collection)
}

// ListDeletedItems returns a list's deleted items, most recently deleted
// first. A non-zero after continues past that item (keyset on deleted_at, id).
func ListDeletedItems(ctx context.Context, database *sql.DB, siteID, collection string, limit int, after model.LegacyDeletedCursor) ([]model.LegacyDeletedItem, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, data, created_at, deleted_at, `+authorExpr+`
		  FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL
		   AND ($3::bigint = 0 OR (deleted_at, id) < ($4, $3))
		 ORDER BY deleted_at DESC, id DESC
		 LIMIT $5`, siteID, collection, after.ID, after.At, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.LegacyDeletedItem, 0)
	for rows.Next() {
		var it model.LegacyDeletedItem
		var created sql.NullTime
		if err := rows.Scan(&it.ID, &it.Data, &created, &it.DeletedAt, &it.By); err != nil {
			return nil, err
		}
		it.CreatedAt = created.Time
		out = append(out, it)
	}
	return out, rows.Err()
}
