package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Saved-data history and undo (saved-data redesign step 1, 2026-09-27).
//
// Every change to a site's saved-data document (kind "state") and every edit,
// delete or restore of a list item (kind "list") writes one data_history row
// holding the value from before the change, the op, who made it and when.
// Deleting an item or clearing a list only marks the items deleted
// (collection_items.deleted_at); they come back with a restore until the undo
// window passes and the sweep removes them for good.

// History kinds.
const (
	HistoryState = "state"
	HistoryList  = "list"
)

// History ops.
const (
	OpReplace  = "replace"  // PUT of the whole document
	OpChange   = "change"   // PATCH ops on the document
	OpEdit     = "edit"     // an item's fields changed
	OpDelete   = "delete"   // one item deleted
	OpClear    = "clear"    // the item went with the whole list
	OpUndelete = "undelete" // a deleted item came back
	OpRestore  = "restore"  // an earlier version put back
)

// Actor is who made a change. ID is "" when nobody was signed in.
type Actor struct {
	ID    string
	Kind  string // owner | admin | visitor | anonymous
	Email string
}

// ErrNoEarlierValue: the history row holds nothing to put back.
var ErrNoEarlierValue = errors.New("this change has no earlier value to put back")

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// HistoryEntry is one change, newest first in lists. Value is the saved data
// as it was just before the change (only filled when one entry is read).
type HistoryEntry struct {
	ID     int64           `json:"id"`
	ItemID *int64          `json:"item_id,omitempty"`
	Op     string          `json:"op"`
	By     string          `json:"by,omitempty"`
	ByKind string          `json:"by_kind"`
	At     time.Time       `json:"at"`
	Size   int64           `json:"size"`
	Value  json.RawMessage `json:"value,omitempty"`
}

const insertHistory = `
	INSERT INTO data_history (site_id, kind, name, item_id, op, prev, actor_id, actor_kind, actor_email)
	VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)`

func recordHistory(ctx context.Context, q Querier, siteID, kind, name string, itemID *int64, op string, prev json.RawMessage, a Actor) error {
	var p any
	if prev != nil {
		p = string(prev)
	}
	var item any
	if itemID != nil {
		item = *itemID
	}
	_, err := q.ExecContext(ctx, insertHistory, siteID, kind, name, item, op, p, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email))
	return err
}

// RecordStateChange records that the document went from prev to next. A write
// that leaves the document as it was records nothing.
func RecordStateChange(ctx context.Context, q Querier, siteID, op string, prev, next json.RawMessage, a Actor) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO data_history (site_id, kind, name, op, prev, actor_id, actor_kind, actor_email)
		SELECT $1, 'state', '', $2, $3::jsonb, $5, $6, $7
		 WHERE $3::jsonb IS DISTINCT FROM $4::jsonb`,
		siteID, op, string(prev), string(next), nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email))
	return err
}

// WriteSiteState replaces the document and records the change, in one
// transaction. expected >= 0 makes it a compare-and-swap on the version
// (ErrStateVersionConflict when it moved). Returns the new version.
func WriteSiteState(ctx context.Context, database *sql.DB, siteID string, state json.RawMessage, expected int, op string, a Actor) (int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	prev, ver, err := GetSiteStateForUpdateByID(ctx, tx, siteID)
	if err != nil {
		return 0, err
	}
	if expected >= 0 && ver != expected {
		return 0, ErrStateVersionConflict
	}
	next, err := SetSiteStateByID(ctx, tx, siteID, state)
	if err != nil {
		return 0, err
	}
	if err := RecordStateChange(ctx, tx, siteID, op, prev, state, a); err != nil {
		return 0, err
	}
	return next, tx.Commit()
}

// ListHistory returns one target's changes newest first: the document
// (kind state) or every item of one list (kind list). before > 0 pages back.
func ListHistory(ctx context.Context, database *sql.DB, siteID, kind, name string, limit int, before int64) ([]HistoryEntry, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, item_id, op, COALESCE(actor_email, ''), actor_kind, created_at,
		       COALESCE(octet_length(prev::text), 0)
		  FROM data_history
		 WHERE site_id = $1 AND kind = $2 AND name = $3 AND ($4 = 0 OR id < $4)
		 ORDER BY id DESC
		 LIMIT $5`, siteID, kind, name, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HistoryEntry, 0)
	for rows.Next() {
		var e HistoryEntry
		var item sql.NullInt64
		if err := rows.Scan(&e.ID, &item, &e.Op, &e.By, &e.ByKind, &e.At, &e.Size); err != nil {
			return nil, err
		}
		if item.Valid {
			v := item.Int64
			e.ItemID = &v
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetHistoryEntry reads one change with its earlier value. sql.ErrNoRows when
// it is not this target's.
func GetHistoryEntry(ctx context.Context, q Querier, siteID, kind, name string, id int64) (HistoryEntry, error) {
	var e HistoryEntry
	var item sql.NullInt64
	var prev []byte
	err := q.QueryRowContext(ctx, `
		SELECT id, item_id, op, COALESCE(actor_email, ''), actor_kind, created_at,
		       COALESCE(octet_length(prev::text), 0), prev
		  FROM data_history
		 WHERE id = $1 AND site_id = $2 AND kind = $3 AND name = $4`, id, siteID, kind, name).
		Scan(&e.ID, &item, &e.Op, &e.By, &e.ByKind, &e.At, &e.Size, &prev)
	if err != nil {
		return e, err
	}
	if item.Valid {
		v := item.Int64
		e.ItemID = &v
	}
	if prev != nil {
		e.Value = json.RawMessage(prev)
	}
	return e, nil
}

// RestoreStateVersion puts the document back as it was before change id. The
// restore is itself a change, so it can be undone too.
func RestoreStateVersion(ctx context.Context, database *sql.DB, siteID string, id int64, a Actor) (json.RawMessage, int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	e, err := GetHistoryEntry(ctx, tx, siteID, HistoryState, "", id)
	if err != nil {
		return nil, 0, err
	}
	if e.Value == nil {
		return nil, 0, ErrNoEarlierValue
	}
	cur, _, err := GetSiteStateForUpdateByID(ctx, tx, siteID)
	if err != nil {
		return nil, 0, err
	}
	ver, err := SetSiteStateByID(ctx, tx, siteID, e.Value)
	if err != nil {
		return nil, 0, err
	}
	if err := RecordStateChange(ctx, tx, siteID, OpRestore, cur, e.Value, a); err != nil {
		return nil, 0, err
	}
	var stored []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(state, 'null'::jsonb) FROM sites WHERE id = $1`, siteID).Scan(&stored); err != nil {
		return nil, 0, err
	}
	return json.RawMessage(stored), ver, tx.Commit()
}

// HasStateHistory reports whether the document has any recorded change.
func HasStateHistory(ctx context.Context, database *sql.DB, siteID string) (bool, error) {
	var ok bool
	err := database.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM data_history WHERE site_id = $1 AND kind = 'state' AND name = '')`, siteID).Scan(&ok)
	return ok, err
}

// ---- list items -------------------------------------------------------------

// SoftDeleteItem marks one live item deleted. ok=false when it is not a live
// item of that list.
func SoftDeleteItem(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE collection_items SET deleted_at = now()
		 WHERE id = $1 AND site_id = $2 AND collection = $3 AND deleted_at IS NULL`, id, siteID, collection)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &id, OpDelete, nil, a); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SoftClearCollection marks every live item of one list deleted and returns
// how many. The list's private/public setting stays.
func SoftClearCollection(ctx context.Context, database *sql.DB, siteID, collection string, a Actor) (int64, error) {
	var n int64
	err := database.QueryRowContext(ctx, `
		WITH gone AS (
			UPDATE collection_items SET deleted_at = now()
			 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NULL
			RETURNING id
		), hist AS (
			INSERT INTO data_history (site_id, kind, name, item_id, op, actor_id, actor_kind, actor_email)
			SELECT $1, 'list', $2, id, 'clear', $3, $4, $5 FROM gone
		)
		SELECT count(*) FROM gone`, siteID, collection, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email)).Scan(&n)
	return n, err
}

// UndeleteItems brings back one deleted item (id > 0) or every deleted item
// of the list (id == 0) and returns how many came back.
func UndeleteItems(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor) (int64, error) {
	var n int64
	err := database.QueryRowContext(ctx, `
		WITH back AS (
			UPDATE collection_items SET deleted_at = NULL
			 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL AND ($3 = 0 OR id = $3)
			RETURNING id
		), hist AS (
			INSERT INTO data_history (site_id, kind, name, item_id, op, actor_id, actor_kind, actor_email)
			SELECT $1, 'list', $2, id, 'undelete', $4, $5, $6 FROM back
		)
		SELECT count(*) FROM back`, siteID, collection, id, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email)).Scan(&n)
	return n, err
}

// RestoreItemVersion undoes list change id: a delete or clear brings the item
// back; an edit or an earlier restore puts back the item's data from before
// it (and brings the item back if it was deleted since).
func RestoreItemVersion(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor) (CollectionItem, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	e, err := GetHistoryEntry(ctx, tx, siteID, HistoryList, collection, id)
	if err != nil {
		return CollectionItem{}, err
	}
	if e.ItemID == nil {
		return CollectionItem{}, sql.ErrNoRows
	}
	itemID := *e.ItemID
	var cur json.RawMessage
	var deleted sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT data, deleted_at FROM collection_items
		 WHERE id = $1 AND site_id = $2 AND collection = $3 FOR UPDATE`, itemID, siteID, collection).Scan(&cur, &deleted); err != nil {
		return CollectionItem{}, err
	}
	switch {
	case e.Op == OpDelete || e.Op == OpClear:
		if deleted.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE collection_items SET deleted_at = NULL WHERE id = $1`, itemID); err != nil {
				return CollectionItem{}, err
			}
			if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &itemID, OpUndelete, nil, a); err != nil {
				return CollectionItem{}, err
			}
		}
	case e.Value == nil:
		return CollectionItem{}, ErrNoEarlierValue
	default:
		if _, err := tx.ExecContext(ctx, `UPDATE collection_items SET data = $2::jsonb, deleted_at = NULL WHERE id = $1`, itemID, string(e.Value)); err != nil {
			return CollectionItem{}, err
		}
		if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &itemID, OpRestore, cur, a); err != nil {
			return CollectionItem{}, err
		}
	}
	var it CollectionItem
	if err := tx.QueryRowContext(ctx, `SELECT id, data, created_at FROM collection_items WHERE id = $1`, itemID).Scan(&it.ID, &it.Data, &it.CreatedAt); err != nil {
		return CollectionItem{}, err
	}
	return it, tx.Commit()
}

// DeletedItem is an item in a list's Recently deleted.
type DeletedItem struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	DeletedAt time.Time       `json:"deleted_at"`
	By        string          `json:"by,omitempty"`
}

// ListDeletedItems returns a list's deleted items, most recently deleted
// first. before > 0 pages back by id.
func ListDeletedItems(ctx context.Context, database *sql.DB, siteID, collection string, limit int, before int64) ([]DeletedItem, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, data, created_at, deleted_at, `+authorExpr+`
		  FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL AND ($3 = 0 OR id < $3)
		 ORDER BY deleted_at DESC, id DESC
		 LIMIT $4`, siteID, collection, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DeletedItem, 0)
	for rows.Next() {
		var it DeletedItem
		var created sql.NullTime
		if err := rows.Scan(&it.ID, &it.Data, &created, &it.DeletedAt, &it.By); err != nil {
			return nil, err
		}
		it.CreatedAt = created.Time
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- keeping history bounded -------------------------------------------------

// PurgeSavedData removes, for good, history older than undoDays and items
// deleted more than undoDays ago (their history goes with them).
func PurgeSavedData(ctx context.Context, database *sql.DB, undoDays int) (history, items int64, err error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM collection_items WHERE deleted_at IS NOT NULL AND deleted_at < now() - make_interval(days => $1)`, undoDays)
	if err != nil {
		return 0, 0, err
	}
	items, _ = res.RowsAffected()
	res, err = database.ExecContext(ctx, `
		DELETE FROM data_history WHERE created_at < now() - make_interval(days => $1)`, undoDays)
	if err != nil {
		return 0, items, err
	}
	history, _ = res.RowsAffected()
	return history, items, nil
}

// SitesOverHistoryCap lists the sites whose history holds more than capBytes.
func SitesOverHistoryCap(ctx context.Context, database *sql.DB, capBytes int64) ([]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT site_id::text FROM data_history
		 GROUP BY site_id HAVING sum(COALESCE(pg_column_size(prev), 0)) > $1`, capBytes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ThinSiteHistory brings one site's history under capBytes by dropping the
// oldest versions first, except that the first change of each target (the
// document, or one item) on each day is always kept: a flood of writes can
// cost detail, never the copy from before it. Returns how many rows went.
func ThinSiteHistory(ctx context.Context, database *sql.DB, siteID string, capBytes int64) (int64, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM data_history WHERE id IN (
			SELECT id FROM (
				SELECT id, keeper,
				       sum(sz) OVER (ORDER BY keeper DESC, id DESC ROWS UNBOUNDED PRECEDING) AS running
				  FROM (
					SELECT id, COALESCE(pg_column_size(prev), 0) AS sz,
					       row_number() OVER (
					         PARTITION BY kind, name, item_id, (created_at AT TIME ZONE 'UTC')::date
					         ORDER BY id) = 1 AS keeper
					  FROM data_history WHERE site_id = $1
				  ) s
			) t
			WHERE NOT keeper AND running > $2
		)`, siteID, capBytes)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SiteDataBytes is what a site's saved data takes, as stored: the document,
// every list item (deleted ones too, until they are purged) and the history.
func SiteDataBytes(ctx context.Context, database *sql.DB, siteID string) (int64, error) {
	var n int64
	err := database.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT pg_column_size(state) FROM sites WHERE id = $1), 0)
		     + COALESCE((SELECT sum(pg_column_size(data)) FROM collection_items WHERE site_id = $1), 0)
		     + COALESCE((SELECT sum(pg_column_size(prev)) FROM data_history WHERE site_id = $1), 0)`, siteID).Scan(&n)
	return n, err
}
