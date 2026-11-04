package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/legacy"
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
	HistoryState   = "state"
	HistoryList    = "list"
	HistoryStorage = "storage"
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

// ErrNoEarlierValue: the history row holds nothing to put back.
var ErrNoEarlierValue = model.ErrNoEarlierValue

// ErrSiteFull: the write would grow the site's live saved data (page data and
// list items; not history, not Recently deleted) past its cap. Writes that do
// not grow it are never refused.
var ErrSiteFull = model.ErrSiteFull

// OperatorLabel is how the platform admin's moderation shows to a site's
// owner, in place of the admin account's address.
const OperatorLabel = "Simple Host (operator)"

// rowsQuerier is a *sql.DB or a *sql.Tx.
type rowsQuerier interface {
	Querier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ---- the per-site size ------------------------------------------------------

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
func lockState(ctx context.Context, q *sql.Tx, siteID string) (json.RawMessage, int, int64, error) {
	return legacy.LockState(ctx, q, siteID)
}

// setStateWithin writes the document (row locked by lockState) and returns
// ErrSiteFull when the document grew and the site is now past maxBytes. The
// caller rolls back on error.
func setStateWithin(ctx context.Context, q *sql.Tx, siteID string, state json.RawMessage, oldSize, maxBytes int64) (int, error) {
	version, size, total, err := legacy.UpdateLockedState(ctx, q, siteID, state)
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
type HistoryEntry = model.HistoryEntry

func recordHistory(ctx context.Context, q Querier, siteID, kind, name string, itemID *int64, op string, prev json.RawMessage, a Actor) error {
	return legacy.RecordHistory(ctx, q, siteID, kind, name, itemID, op, prev, a)
}

// RecordStateChange records that the document went from prev to next, with a
// full copy of prev. A write that leaves the document as it was records
// nothing.
func RecordStateChange(ctx context.Context, q Querier, siteID, op string, prev, next json.RawMessage, a Actor) error {
	return legacy.RecordStateChange(ctx, q, siteID, op, prev, next, a)
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

type StatePatch = model.StatePatch

// stateBefore rebuilds the document as it was just before state change id:
// the row's own full copy, else the nearest newer full copy (or the current
// document) with each newer reverse diff applied, newest first. q must give
// one consistent view (the site row locked, or a snapshot).
func stateBefore(ctx context.Context, q rowsQuerier, siteID string, id int64) (json.RawMessage, error) {
	hasPrev, hasDiff, err := legacy.HistoryValueFlags(ctx, q, siteID, id)
	if err != nil {
		return nil, err
	}
	if !hasPrev && !hasDiff {
		return nil, ErrNoEarlierValue
	}
	fullID, err := legacy.NextFullHistoryID(ctx, q, siteID, id)
	if err != nil {
		return nil, err
	}
	var base []byte
	upper := int64(1<<63 - 1)
	if fullID.Valid {
		if err := legacy.FullHistoryValue(ctx, q, fullID.Int64, &base); err != nil {
			return nil, err
		}
		if fullID.Int64 == id {
			return json.RawMessage(base), nil
		}
		upper = fullID.Int64
	} else if err := legacy.CurrentStateValue(ctx, q, siteID, &base); err != nil {
		return nil, err
	}
	doc, err := decodeNumbers(base)
	if err != nil {
		return nil, err
	}
	rows, err := legacy.HistoryDiffRows(ctx, q, siteID, id, upper)
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
	return legacy.ListHistory(ctx, database, siteID, kind, name, limit, before)
}

// GetHistoryEntry reads one change with its earlier value (the document's is
// rebuilt from history when the row keeps only a diff). sql.ErrNoRows when it
// is not this target's. q must give one consistent view: a transaction
// holding the site row, or ReadHistoryEntry's snapshot.
func GetHistoryEntry(ctx context.Context, q rowsQuerier, siteID, kind, name string, id int64) (HistoryEntry, error) {
	e, prev, err := legacy.HistoryRecord(ctx, q, siteID, kind, name, id)
	if err != nil {
		return e, err
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
	if err := legacy.CurrentStateValue(ctx, tx, siteID, &stored); err != nil {
		return nil, 0, err
	}
	return json.RawMessage(stored), ver, tx.Commit()
}

// HasStateHistory reports whether the document has any recorded change.
func HasStateHistory(ctx context.Context, database *sql.DB, siteID string) (bool, error) {
	return legacy.HasStateHistory(ctx, database, siteID)
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
	res, err := legacy.SoftDeleteItem(ctx, tx, siteID, collection, id)
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
	return legacy.SoftClearCollection(ctx, database, siteID, collection, a)
}

// UndeleteItems brings back one deleted item (id > 0) or every deleted item
// of the list (id == 0) and returns how many came back. onlyCleared brings
// back only items the owner's clear took (a Personal name: a record its
// person deleted stays deleted). within > 0 brings back only items deleted
// that recently. The name's declaration lock is held throughout. maxItems > 0
// (a Shared board) refuses with ErrNameFull when the name would hold more
// than maxItems live items; a Page info name refuses with ErrOneDocument when
// it would hold more than one. ErrSiteFull when they would take the site past
// maxBytes.
func UndeleteItems(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor, maxBytes int64, onlyCleared bool, within time.Duration, maxItems int) (int64, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	kind, err := lockNameFor(ctx, tx, siteID, collection)
	if err != nil {
		return 0, err
	}
	n, err := legacy.UndeleteItems(ctx, tx, siteID, collection, id, a, onlyCleared, within)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		if err := itemsWithin(ctx, tx, siteID, collection, maxItems); err != nil {
			return 0, err
		}
		if err := oneDocument(ctx, tx, siteID, collection, kind); err != nil {
			return 0, err
		}
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// ErrOneDocument: Page info is one document, and bringing this item back
// would make a second live one beside it.
var ErrOneDocument = model.ErrOneDocument

// lockNameFor takes the name's declaration-row lock when it has one, so a
// restore and an add (or a change of kind) cannot interleave, and returns
// its kind ("" when undeclared).
func lockNameFor(ctx context.Context, tx *sql.Tx, siteID, name string) (string, error) {
	return legacy.LockHistoryName(ctx, tx, siteID, name)
}

// oneDocument is ErrOneDocument when a Page info name (kind content) now
// holds more than one live item.
func oneDocument(ctx context.Context, tx *sql.Tx, siteID, name, kind string) error {
	if kind != KindContent {
		return nil
	}
	many, err := NameHoldsItems(ctx, tx, siteID, name, 1)
	if err != nil {
		return err
	}
	if many {
		return ErrOneDocument
	}
	return nil
}

// itemsWithin is ErrNameFull when a capped name (maxItems > 0) now holds more
// than maxItems live items.
func itemsWithin(ctx context.Context, tx *sql.Tx, siteID, name string, maxItems int) error {
	if maxItems <= 0 {
		return nil
	}
	over, err := NameHoldsItems(ctx, tx, siteID, name, maxItems)
	if err != nil {
		return err
	}
	if over {
		return ErrNameFull
	}
	return nil
}

// RestoreItemVersion undoes list change id: a delete or clear brings the item
// back; an edit or an earlier restore puts back the item's data from before
// it (and brings the item back if it was deleted since). ErrSiteFull when
// that grows the site past maxBytes; with maxItems > 0 (a Shared board),
// ErrNameFull when an item brought back would take the name past maxItems;
// ErrOneDocument when it would be a second live Page info document.
func RestoreItemVersion(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a Actor, maxBytes int64, maxItems int) (CollectionItem, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	kind, err := lockNameFor(ctx, tx, siteID, collection)
	if err != nil {
		return CollectionItem{}, err
	}
	e, err := GetHistoryEntry(ctx, tx, siteID, HistoryList, collection, id)
	if err != nil {
		return CollectionItem{}, err
	}
	if e.ItemID == nil {
		return CollectionItem{}, sql.ErrNoRows
	}
	itemID := *e.ItemID
	cur, deleted, err := legacy.LockHistoryItem(ctx, tx, itemID, siteID, collection)
	if err != nil {
		return CollectionItem{}, err
	}
	grew := false
	switch {
	case e.Op == OpDelete || e.Op == OpClear:
		if deleted.Valid {
			if err := legacy.UndeleteHistoryItem(ctx, tx, itemID); err != nil {
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
		if err := legacy.RestoreHistoryItem(ctx, tx, itemID, e.Value); err != nil {
			return CollectionItem{}, err
		}
		if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &itemID, OpRestore, cur, a); err != nil {
			return CollectionItem{}, err
		}
		grew = deleted.Valid || len(e.Value) > len(cur)
	}
	if deleted.Valid {
		if err := itemsWithin(ctx, tx, siteID, collection, maxItems); err != nil {
			return CollectionItem{}, err
		}
		if err := oneDocument(ctx, tx, siteID, collection, kind); err != nil {
			return CollectionItem{}, err
		}
	}
	if grew {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return CollectionItem{}, err
		}
	}
	it, err := legacy.HistoryItem(ctx, tx, itemID)
	if err != nil {
		return CollectionItem{}, err
	}
	return it, tx.Commit()
}

// ---- deleting for good (owner) ------------------------------------------------

// PurgeDeletedItems removes, for good, one item of a list's Recently deleted
// (id > 0) or all of them (id == 0), with their history. Live items are never
// touched. Returns how many went.
func PurgeDeletedItems(ctx context.Context, database *sql.DB, siteID, collection string, id int64) (int64, error) {
	return legacy.PurgeDeletedItems(ctx, database, siteID, collection, id)
}

// ClearSiteHistory removes, for good, every earlier version of a site's saved
// data and list items. The data itself and Recently deleted stay. Returns how
// many changes went.
func ClearSiteHistory(ctx context.Context, database *sql.DB, siteID string) (int64, error) {
	return legacy.ClearSiteHistory(ctx, database, siteID)
}

// ---- keeping history bounded -------------------------------------------------

// PurgeSavedData removes, for good, history older than undoDays and items
// deleted more than undoDays ago (their history goes with them). A change to
// the document is kept past undoDays while an earlier change to it is still
// kept: that one may be a diff, rebuilt only through every newer change, and
// created_at (its transaction's start) can be out of id order when two writes
// waited on the same row lock.
func PurgeSavedData(ctx context.Context, database *sql.DB, undoDays int) (history, items int64, err error) {
	res, err := legacy.PurgeOldDeletedItems(ctx, database, undoDays)
	if err != nil {
		return 0, 0, err
	}
	items, _ = res.RowsAffected()
	res, err = legacy.PurgeOldHistory(ctx, database, undoDays)
	if err != nil {
		return 0, items, err
	}
	history, _ = res.RowsAffected()
	return history, items, nil
}

// HistoryBytes is a site's history size (sites.history_bytes); 0 when the
// site is gone.
func HistoryBytes(ctx context.Context, q Querier, siteID string) (int64, error) {
	return legacy.HistoryBytes(ctx, q, siteID)
}

// SitesOverHistoryCap lists the sites whose history holds more than capBytes.
func SitesOverHistoryCap(ctx context.Context, database *sql.DB, capBytes int64) ([]string, error) {
	return legacy.SitesOverHistoryCap(ctx, database, capBytes)
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
	return legacy.ThinSiteHistory(ctx, database, siteID, capBytes)
}

// ---- what a person did on other people's sites (Download my data) -------------

// ChangeElsewhere is one change this person made to saved data on a site
// that is not theirs, while signed in there: where, what, when. Never the
// value (that is the site owner's data).
type ChangeElsewhere = model.ChangeElsewhere

// ListChangesElsewhere returns every recorded change this person made to
// other people's saved data, oldest first.
func ListChangesElsewhere(ctx context.Context, database *sql.DB, userID string) ([]ChangeElsewhere, error) {
	return legacy.ListChangesElsewhere(ctx, database, userID)
}
