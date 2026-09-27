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
// holding the value from before the change, the op, who made it and when. A
// PATCH keeps only what it changed (statediff.go). Deleting an item or
// clearing a list only marks the items deleted (collection_items.deleted_at);
// they come back with a restore until the undo window passes and the sweep
// removes them for good, or the owner deletes them for good sooner.

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

// ErrSiteFull: the write would grow the site's live saved data (page data and
// list items; not history, not Recently deleted) past its cap. Writes that do
// not grow it are never refused.
var ErrSiteFull = errors.New("site saved data is full")

// OperatorLabel is how the platform admin's moderation shows to a site's
// owner, in place of the admin account's address.
const OperatorLabel = "Simple Host (operator)"

// byExpr is who made a history change, as the owner sees it.
const byExpr = `CASE WHEN actor_kind = 'admin' THEN '` + OperatorLabel + `' ELSE COALESCE(actor_email, '') END`

// rowsQuerier is a *sql.DB or a *sql.Tx.
type rowsQuerier interface {
	Querier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- the per-site size ------------------------------------------------------

// SiteDataUsage is a site's live saved data in bytes (the document plus live
// list items), kept by triggers in the same transaction as every write
// (db/migrations/sd1-saved-data-safety2-limits.sql): one row read.
func SiteDataUsage(ctx context.Context, q Querier, siteID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT data_bytes FROM sites WHERE id = $1`, siteID).Scan(&n)
	return n, err
}

// HasRoom reports whether growing siteID's live saved data by growth bytes
// stays within maxBytes. A write that does not grow it (growth <= 0) always
// has room; maxBytes <= 0 is no cap.
func HasRoom(ctx context.Context, q Querier, siteID string, growth, maxBytes int64) (bool, error) {
	if growth <= 0 || maxBytes <= 0 {
		return true, nil
	}
	used, err := SiteDataUsage(ctx, q, siteID)
	if err != nil {
		return false, err
	}
	return used+growth <= maxBytes, nil
}

// roomAfter checks, inside a write's transaction, that a write which grew the
// site left it within maxBytes (ErrSiteFull otherwise; the caller rolls back).
func roomAfter(ctx context.Context, q Querier, siteID string, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	used, err := SiteDataUsage(ctx, q, siteID)
	if err != nil {
		return err
	}
	if used > maxBytes {
		return ErrSiteFull
	}
	return nil
}

// lockState reads the document, its version and its stored size with the
// site row locked.
func lockState(ctx context.Context, q Querier, siteID string) (json.RawMessage, int, int64, error) {
	var state []byte
	var version int
	var size int64
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(state, 'null'::jsonb), state_version, state_bytes FROM sites WHERE id = $1 FOR UPDATE`, siteID).
		Scan(&state, &version, &size)
	if err != nil {
		return nil, 0, 0, err
	}
	return json.RawMessage(state), version, size, nil
}

// setStateWithin writes the document (row locked by lockState) and returns
// ErrSiteFull when the document grew and the site is now past maxBytes. The
// caller rolls back on error.
func setStateWithin(ctx context.Context, q Querier, siteID string, state json.RawMessage, oldSize, maxBytes int64) (int, error) {
	var version int
	var size, total int64
	err := q.QueryRowContext(ctx, `
		UPDATE sites SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		 WHERE id = $1
		RETURNING state_version, state_bytes, data_bytes`, siteID, string(state)).Scan(&version, &size, &total)
	if err != nil {
		return 0, err
	}
	if maxBytes > 0 && size > oldSize && total > maxBytes {
		return 0, ErrSiteFull
	}
	return version, nil
}

// ---- the document -----------------------------------------------------------

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

// RecordStateChange records that the document went from prev to next, with a
// full copy of prev. A write that leaves the document as it was records
// nothing.
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
// (ErrStateVersionConflict when it moved). ErrSiteFull when the document grew
// and the site is past maxBytes. Returns the new version.
func WriteSiteState(ctx context.Context, database *sql.DB, siteID string, state json.RawMessage, expected int, op string, a Actor, maxBytes int64) (int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	prev, ver, size, err := lockState(ctx, tx, siteID)
	if err != nil {
		return 0, err
	}
	if expected >= 0 && ver != expected {
		return 0, ErrStateVersionConflict
	}
	next, err := setStateWithin(ctx, tx, siteID, state, size, maxBytes)
	if err != nil {
		return 0, err
	}
	if err := RecordStateChange(ctx, tx, siteID, op, prev, state, a); err != nil {
		return 0, err
	}
	return next, tx.Commit()
}

// StatePatch is one PATCH of the document: Before is the document as read
// (decoded), After the same with the ops applied, Next After as saved.
type StatePatch struct {
	Before any
	After  any
	Next   json.RawMessage
}

// PatchSiteState runs a PATCH in one transaction. apply gets the document,
// read with the site row locked, and returns the patch or an error, which is
// passed back as is. The change goes to history as a reverse diff, or as a
// full copy on the day's first change, when none of the last snapshotEvery
// changes is one, or when the diff would be about as large. ErrSiteFull when
// the document grew and the site is past maxBytes. Returns the new version.
func PatchSiteState(ctx context.Context, database *sql.DB, siteID string, a Actor, maxBytes int64, snapshotEvery int, apply func(cur json.RawMessage) (StatePatch, error)) (int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cur, _, size, err := lockState(ctx, tx, siteID)
	if err != nil {
		return 0, err
	}
	p, err := apply(cur)
	if err != nil {
		return 0, err
	}
	ver, err := setStateWithin(ctx, tx, siteID, p.Next, size, maxBytes)
	if err != nil {
		return 0, err
	}
	if err := recordStatePatch(ctx, tx, siteID, cur, p, snapshotEvery, a); err != nil {
		return 0, err
	}
	return ver, tx.Commit()
}

func recordStatePatch(ctx context.Context, tx *sql.Tx, siteID string, prev json.RawMessage, p StatePatch, snapshotEvery int, a Actor) error {
	d, err := reverseDiff(p.Before, p.After)
	if err != nil {
		return err
	}
	if len(d) == 0 {
		return nil
	}
	diff, err := json.Marshal(d)
	if err != nil {
		return err
	}
	full := len(diff)*2 >= len(prev)
	if !full {
		var fullToday, fullRecent bool
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(bool_or(is_full AND today), false), COALESCE(bool_or(is_full), false) FROM (
				SELECT prev IS NOT NULL AS is_full,
				       (created_at AT TIME ZONE 'UTC')::date = (now() AT TIME ZONE 'UTC')::date AS today
				  FROM data_history WHERE site_id = $1 AND kind = 'state' AND name = ''
				 ORDER BY id DESC LIMIT $2) t`, siteID, snapshotEvery).Scan(&fullToday, &fullRecent)
		if err != nil {
			return err
		}
		full = !fullToday || !fullRecent
	}
	if full {
		return recordHistory(ctx, tx, siteID, HistoryState, "", nil, OpChange, prev, a)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO data_history (site_id, kind, name, op, diff, actor_id, actor_kind, actor_email)
		VALUES ($1, 'state', '', $2, $3::jsonb, $4, $5, $6)`,
		siteID, OpChange, string(diff), nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email))
	return err
}

// stateBefore rebuilds the document as it was just before state change id:
// the row's own full copy, else the nearest newer full copy (or the current
// document) with each newer reverse diff applied, newest first. q must give
// one consistent view (the site row locked, or a snapshot).
func stateBefore(ctx context.Context, q rowsQuerier, siteID string, id int64) (json.RawMessage, error) {
	var hasPrev, hasDiff bool
	if err := q.QueryRowContext(ctx, `
		SELECT prev IS NOT NULL, diff IS NOT NULL FROM data_history
		 WHERE id = $1 AND site_id = $2 AND kind = 'state' AND name = ''`, id, siteID).Scan(&hasPrev, &hasDiff); err != nil {
		return nil, err
	}
	if !hasPrev && !hasDiff {
		return nil, ErrNoEarlierValue
	}
	var fullID sql.NullInt64
	if err := q.QueryRowContext(ctx, `
		SELECT min(id) FROM data_history
		 WHERE site_id = $1 AND kind = 'state' AND name = '' AND id >= $2 AND prev IS NOT NULL`, siteID, id).Scan(&fullID); err != nil {
		return nil, err
	}
	var base []byte
	upper := int64(1<<63 - 1)
	if fullID.Valid {
		if err := q.QueryRowContext(ctx, `SELECT prev FROM data_history WHERE id = $1`, fullID.Int64).Scan(&base); err != nil {
			return nil, err
		}
		if fullID.Int64 == id {
			return json.RawMessage(base), nil
		}
		upper = fullID.Int64
	} else if err := q.QueryRowContext(ctx, `SELECT COALESCE(state, 'null'::jsonb) FROM sites WHERE id = $1`, siteID).Scan(&base); err != nil {
		return nil, err
	}
	doc, err := decodeNumbers(base)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT diff FROM data_history
		 WHERE site_id = $1 AND kind = 'state' AND name = '' AND id >= $2 AND id < $3
		 ORDER BY id DESC`, siteID, id, upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var d []diffEntry
		if raw == nil || json.Unmarshal(raw, &d) != nil {
			return nil, errBrokenHistory
		}
		if doc, err = applyReverse(doc, d); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out, err := json.Marshal(doc)
	return json.RawMessage(out), err
}

// ListHistory returns one target's changes newest first: the document
// (kind state) or every item of one list (kind list). before > 0 pages back.
func ListHistory(ctx context.Context, database *sql.DB, siteID, kind, name string, limit int, before int64) ([]HistoryEntry, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, item_id, op, `+byExpr+`, actor_kind, created_at,
		       COALESCE(octet_length(prev::text), octet_length(diff::text), 0)
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

// GetHistoryEntry reads one change with its earlier value (the document's is
// rebuilt from history when the row keeps only a diff). sql.ErrNoRows when it
// is not this target's. q must give one consistent view: a transaction
// holding the site row, or ReadHistoryEntry's snapshot.
func GetHistoryEntry(ctx context.Context, q rowsQuerier, siteID, kind, name string, id int64) (HistoryEntry, error) {
	var e HistoryEntry
	var item sql.NullInt64
	var prev []byte
	err := q.QueryRowContext(ctx, `
		SELECT id, item_id, op, `+byExpr+`, actor_kind, created_at,
		       COALESCE(octet_length(prev::text), octet_length(diff::text), 0), prev
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
	switch {
	case prev != nil:
		e.Value = json.RawMessage(prev)
	case kind == HistoryState:
		v, err := stateBefore(ctx, q, siteID, id)
		if err != nil && !errors.Is(err, ErrNoEarlierValue) {
			return e, err
		}
		e.Value = v
	}
	return e, nil
}

// ReadHistoryEntry is GetHistoryEntry in one read-only snapshot.
func ReadHistoryEntry(ctx context.Context, database *sql.DB, siteID, kind, name string, id int64) (HistoryEntry, error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return HistoryEntry{}, err
	}
	defer tx.Rollback()
	return GetHistoryEntry(ctx, tx, siteID, kind, name, id)
}

// RestoreStateVersion puts the document back as it was before change id. The
// restore is itself a change, so it can be undone too. ErrSiteFull when that
// grows the site past maxBytes.
func RestoreStateVersion(ctx context.Context, database *sql.DB, siteID string, id int64, a Actor, maxBytes int64) (json.RawMessage, int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	// Lock first: the rebuild reads history and the document as they are.
	cur, _, size, err := lockState(ctx, tx, siteID)
	if err != nil {
		return nil, 0, err
	}
	e, err := GetHistoryEntry(ctx, tx, siteID, HistoryState, "", id)
	if err != nil {
		return nil, 0, err
	}
	if e.Value == nil {
		return nil, 0, ErrNoEarlierValue
	}
	ver, err := setStateWithin(ctx, tx, siteID, e.Value, size, maxBytes)
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
// of the list (id == 0) and returns how many came back. ErrSiteFull when
// they would take the site past maxBytes.
func UndeleteItems(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor, maxBytes int64) (int64, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int64
	err = tx.QueryRowContext(ctx, `
		WITH back AS (
			UPDATE collection_items SET deleted_at = NULL
			 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL AND ($3 = 0 OR id = $3)
			RETURNING id
		), hist AS (
			INSERT INTO data_history (site_id, kind, name, item_id, op, actor_id, actor_kind, actor_email)
			SELECT $1, 'list', $2, id, 'undelete', $4, $5, $6 FROM back
		)
		SELECT count(*) FROM back`, siteID, collection, id, nullIfEmpty(a.ID), a.Kind, nullIfEmpty(a.Email)).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// RestoreItemVersion undoes list change id: a delete or clear brings the item
// back; an edit or an earlier restore puts back the item's data from before
// it (and brings the item back if it was deleted since). ErrSiteFull when
// that grows the site past maxBytes.
func RestoreItemVersion(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor, maxBytes int64) (CollectionItem, error) {
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
	grew := false
	switch {
	case e.Op == OpDelete || e.Op == OpClear:
		if deleted.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE collection_items SET deleted_at = NULL WHERE id = $1`, itemID); err != nil {
				return CollectionItem{}, err
			}
			if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &itemID, OpUndelete, nil, a); err != nil {
				return CollectionItem{}, err
			}
			grew = true
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
		grew = deleted.Valid || len(e.Value) > len(cur)
	}
	if grew {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
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

// DeletedCursor is where a page of Recently deleted ended: the last item's
// deletion time and id (the page is ordered by both, newest first).
type DeletedCursor struct {
	At time.Time
	ID int64
}

// ListDeletedItems returns a list's deleted items, most recently deleted
// first. A non-zero after continues past that item (keyset on deleted_at, id).
func ListDeletedItems(ctx context.Context, database *sql.DB, siteID, collection string, limit int, after DeletedCursor) ([]DeletedItem, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, data, created_at, deleted_at, `+authorExpr+`
		  FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL
		   AND ($3 = 0 OR (deleted_at, id) < ($4, $3))
		 ORDER BY deleted_at DESC, id DESC
		 LIMIT $5`, siteID, collection, after.ID, after.At, limit)
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

// ---- deleting for good (owner) ------------------------------------------------

// PurgeDeletedItems removes, for good, one item of a list's Recently deleted
// (id > 0) or all of them (id == 0), with their history. Live items are never
// touched. Returns how many went.
func PurgeDeletedItems(ctx context.Context, database *sql.DB, siteID, collection string, id int64) (int64, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM collection_items
		 WHERE site_id = $1 AND collection = $2 AND deleted_at IS NOT NULL AND ($3 = 0 OR id = $3)`, siteID, collection, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearSiteHistory removes, for good, every earlier version of a site's saved
// data and list items. The data itself and Recently deleted stay. Returns how
// many changes went.
func ClearSiteHistory(ctx context.Context, database *sql.DB, siteID string) (int64, error) {
	res, err := database.ExecContext(ctx, `DELETE FROM data_history WHERE site_id = $1`, siteID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- keeping history bounded -------------------------------------------------

// PurgeSavedData removes, for good, history older than undoDays and items
// deleted more than undoDays ago (their history goes with them). A change to
// the document is kept past undoDays while an earlier change to it is still
// kept: that one may be a diff, rebuilt only through every newer change, and
// created_at (its transaction's start) can be out of id order when two writes
// waited on the same row lock.
func PurgeSavedData(ctx context.Context, database *sql.DB, undoDays int) (history, items int64, err error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM collection_items WHERE deleted_at IS NOT NULL AND deleted_at < now() - make_interval(days => $1)`, undoDays)
	if err != nil {
		return 0, 0, err
	}
	items, _ = res.RowsAffected()
	res, err = database.ExecContext(ctx, `
		DELETE FROM data_history h
		 WHERE h.created_at < now() - make_interval(days => $1)
		   AND (h.kind <> 'state' OR NOT EXISTS (
		        SELECT 1 FROM data_history k
		         WHERE k.site_id = h.site_id AND k.kind = 'state' AND k.name = h.name
		           AND k.id < h.id AND k.created_at >= now() - make_interval(days => $1)))`, undoDays)
	if err != nil {
		return 0, items, err
	}
	history, _ = res.RowsAffected()
	return history, items, nil
}

// historySize is what one history row holds: the text of its value or diff,
// the same measure the sites.history_bytes trigger keeps
// (sd1-saved-data-safety3-history-bytes.sql).
const historySize = `COALESCE(octet_length(prev::text), octet_length(diff::text), 0)`

// HistoryOverCap reports whether a site's history holds more than capBytes
// (sites.history_bytes: one row read).
func HistoryOverCap(ctx context.Context, q Querier, siteID string, capBytes int64) (bool, error) {
	var over bool
	err := q.QueryRowContext(ctx, `SELECT history_bytes > $2 FROM sites WHERE id = $1`, siteID, capBytes).Scan(&over)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return over, err
}

// SitesOverHistoryCap lists the sites whose history holds more than capBytes.
func SitesOverHistoryCap(ctx context.Context, database *sql.DB, capBytes int64) ([]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id::text FROM sites WHERE history_bytes > $1`, capBytes)
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
// cost detail, never the copy from before it. For the document the day's
// first full copy is the one kept (a diff alone cannot be put back without
// the newer changes). Rows that hold no value (a delete, a clear, a bring
// back) take no space and stay as the record of who did it. Returns how many
// rows went.
func ThinSiteHistory(ctx context.Context, database *sql.DB, siteID string, capBytes int64) (int64, error) {
	res, err := database.ExecContext(ctx, `
		DELETE FROM data_history WHERE id IN (
			SELECT id FROM (
				SELECT id, keeper, sz,
				       sum(sz) OVER (ORDER BY keeper DESC, id DESC ROWS UNBOUNDED PRECEDING) AS running
				  FROM (
					SELECT id, `+historySize+` AS sz,
					       (kind <> 'state' OR prev IS NOT NULL) AND row_number() OVER (
					         PARTITION BY kind, name, item_id, (created_at AT TIME ZONE 'UTC')::date,
					                      (kind <> 'state' OR prev IS NOT NULL)
					         ORDER BY id) = 1 AS keeper
					  FROM data_history WHERE site_id = $1
				  ) s
			) t
			WHERE NOT keeper AND sz > 0 AND running > $2
		)`, siteID, capBytes)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- what a person did on other people's sites (Download my data) -------------

// ChangeElsewhere is one change this person made to saved data on a site
// that is not theirs, while signed in there: where, what, when. Never the
// value (that is the site owner's data).
type ChangeElsewhere struct {
	SiteID string
	Kind   string
	Name   string
	ItemID *int64
	Op     string
	At     time.Time
}

// ListChangesElsewhere returns every recorded change this person made to
// other people's saved data, oldest first.
func ListChangesElsewhere(ctx context.Context, database *sql.DB, userID string) ([]ChangeElsewhere, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT h.site_id::text, h.kind, h.name, h.item_id, h.op, h.created_at
		  FROM data_history h JOIN sites s ON s.id = h.site_id
		 WHERE h.actor_id = $1 AND s.user_id IS DISTINCT FROM $1
		 ORDER BY h.created_at, h.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeElsewhere
	for rows.Next() {
		var c ChangeElsewhere
		var item sql.NullInt64
		if err := rows.Scan(&c.SiteID, &c.Kind, &c.Name, &item, &c.Op, &c.At); err != nil {
			return nil, err
		}
		if item.Valid {
			v := item.Int64
			c.ItemID = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
