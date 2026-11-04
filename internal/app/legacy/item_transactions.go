package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// AppendEntry adds one entry by a signed-in person (a.ID) to a declared
// Submissions name: at most maxItems live entries in the name, and with
// onePerPerson at most one live entry per person (ErrOnePerPerson, with the
// id of the one they have; 0 when it was sent by another account of the same
// address). A person is the account or its address with any +tag dropped
// (BaseEmail). The declaration row is locked, so two saves at once cannot
// both pass either check; under it the name must still be kind (Submissions,
// or a Shared board) with the privacy the caller read (private), else
// ErrKindChanged.
func appendEntry(ctx context.Context, database *sql.DB, siteID, name, kind string, private bool, data json.RawMessage, a model.Actor, onePerPerson bool, maxItems int) (model.CollectionItem, int64, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, 0, err
	}
	defer tx.Rollback()
	// Under the lock, the name is still what the caller read: a change of
	// kind (to Page info, say) or of privacy (a private list made public
	// while this entry was on its way) may have committed since.
	cur, curPrivate, err := repo.LockEntryDeclaration(ctx, tx, siteID, name)
	if err != nil {
		return model.CollectionItem{}, 0, err
	}
	if cur != kind || curPrivate != private {
		return model.CollectionItem{}, 0, repo.ErrKindChanged
	}
	if onePerPerson && a.ID != "" {
		have, mine, err := repo.LivePersonEntry(ctx, tx, siteID, name, a.ID, model.BaseEmail(a.Email), 0)
		if err != nil {
			return model.CollectionItem{}, 0, err
		}
		if have != 0 {
			if !mine {
				have = 0 // another account of the same address: nothing to point at
			}
			return model.CollectionItem{}, have, model.ErrOnePerPerson
		}
	}
	full, err := repo.NameHoldsItems(ctx, tx, siteID, name, maxItems-1)
	if err != nil {
		return model.CollectionItem{}, 0, err
	}
	if full {
		return model.CollectionItem{}, 0, model.ErrNameFull
	}
	it, err := repo.InsertEntry(ctx, tx, siteID, name, data, a)
	if err != nil {
		return model.CollectionItem{}, 0, err
	}
	return it, 0, tx.Commit()
}

// UndoOwnWithdrawal brings back item id of name when userID sent it and
// withdrew it themselves within the last window: the item's latest change is
// that person's own delete. ok=false otherwise (nothing changes). For a
// declared Submissions name (declared) it takes the same lock as AppendEntry
// and keeps its rules: at most maxItems live entries (ErrNameFull) and, with
// onePerPerson, none while the person has another live entry
// (ErrOnePerPerson).
//
// anyAuthor (a Shared board) lets the person who deleted an item bring it
// back whoever added it; otherwise it must be their own.
func undoOwnWithdrawal(ctx context.Context, database *sql.DB, siteID, name string, id int64, userID string, window time.Duration, declared, onePerPerson bool, maxItems int, a model.Actor, maxBytes int64, anyAuthor bool) (model.CollectionItem, bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, false, err
	}
	defer tx.Rollback()
	if declared {
		if err := repo.LockContentDeclaration(ctx, tx, siteID, name); err != nil {
			return model.CollectionItem{}, false, err
		}
	}
	op, actor, at, err := repo.LockWithdrawal(ctx, tx, siteID, name, id, userID, anyAuthor)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, false, nil
	}
	if err != nil {
		return model.CollectionItem{}, false, err
	}
	if op.String != "delete" || actor.String != userID || time.Since(at) > window {
		return model.CollectionItem{}, false, nil
	}
	if declared && onePerPerson {
		other, _, err := repo.LivePersonEntry(ctx, tx, siteID, name, userID, model.BaseEmail(a.Email), id)
		if err != nil {
			return model.CollectionItem{}, false, err
		}
		if other != 0 {
			return model.CollectionItem{}, false, model.ErrOnePerPerson
		}
	}
	if declared {
		full, err := repo.NameHoldsItems(ctx, tx, siteID, name, maxItems-1)
		if err != nil {
			return model.CollectionItem{}, false, err
		}
		if full {
			return model.CollectionItem{}, false, model.ErrNameFull
		}
	}
	it, err := repo.RestoreWithdrawal(ctx, tx, id)
	if err != nil {
		return model.CollectionItem{}, false, err
	}
	if err := repo.RecordHistory(ctx, tx, siteID, "list", name, &id, "undelete", nil, a); err != nil {
		return model.CollectionItem{}, false, err
	}
	if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
		return model.CollectionItem{}, false, err
	}
	return it, true, tx.Commit()
}

// SavePersonal writes a.ID's record in a Personal name: fn gets the live
// record (nil when there is none) and returns the new one. Each person has
// one row per name for good: a deleted record (by them, or cleared by the
// owner) is written over and comes back live, so a restore never makes two.
// The earlier value goes to history. The declaration row is locked, so two
// first saves at once cannot make two rows. ErrSiteFull when it grows the
// site past maxBytes; ErrPeopleFull when the person has no row yet and
// peopleMax people already have one (live or in Recently deleted).
func savePersonal(ctx context.Context, database *sql.DB, siteID, name string, a model.Actor, maxBytes int64, peopleMax int, fn func(cur json.RawMessage) (json.RawMessage, error)) (model.CollectionItem, error) {
	if a.ID == "" {
		return model.CollectionItem{}, errors.New("a personal record needs a signed-in person")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, err
	}
	defer tx.Rollback()
	if err := repo.LockPersonalDeclaration(ctx, tx, siteID, name); err != nil {
		return model.CollectionItem{}, err
	}
	id, old, deleted, err := repo.LockPersonalItem(ctx, tx, siteID, name, a.ID)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, err
	}
	var cur json.RawMessage
	if found && !deleted.Valid {
		cur = old
	}
	next, err := fn(cur)
	if err != nil {
		return model.CollectionItem{}, err
	}
	var it model.CollectionItem
	if found {
		it, err = repo.UpdatePersonalItem(ctx, tx, id, next, a.Email)
		if err != nil {
			return model.CollectionItem{}, err
		}
		if err := repo.RecordHistory(ctx, tx, siteID, "list", name, &id, "edit", old, a); err != nil {
			return model.CollectionItem{}, err
		}
	} else {
		if peopleMax > 0 {
			n, err := repo.CountPersonalItems(ctx, tx, siteID, name, peopleMax)
			if err != nil {
				return model.CollectionItem{}, err
			}
			if n >= peopleMax {
				return model.CollectionItem{}, model.ErrPeopleFull
			}
		}
		it, err = repo.InsertPersonalItem(ctx, tx, siteID, name, next, a)
		if err != nil {
			return model.CollectionItem{}, err
		}
	}
	if !found || deleted.Valid || len(it.Data) > len(old) {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return model.CollectionItem{}, err
		}
	}
	return it, tx.Commit()
}

// ForgetClearedPersonal is a person deleting their record after the owner's
// clear took it: nothing changes but a delete by them in its history, so the
// owner's "Restore" (which brings back only what a clear took) leaves it
// deleted. ok=false when they have no record that a clear took last.
func forgetClearedPersonal(ctx context.Context, database *sql.DB, siteID, name string, a model.Actor) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	id, op, err := repo.LockClearedPersonal(ctx, tx, siteID, name, a.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && op.String != "clear") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := repo.RecordHistory(ctx, tx, siteID, "list", name, &id, "delete", nil, a); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SoftDeleteItem marks one live item deleted. ok=false when it is not a live
// item of that list.
func softDeleteItem(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a model.Actor) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := repo.SoftDeleteItem(ctx, tx, siteID, collection, id)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if err := repo.RecordHistory(ctx, tx, siteID, "list", collection, &id, "delete", nil, a); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// RestoreItemVersion undoes list change id: a delete or clear brings the item
// back; an edit or an earlier restore puts back the item's data from before
// it (and brings the item back if it was deleted since). ErrSiteFull when
// that grows the site past maxBytes; with maxItems > 0 (a Shared board),
// ErrNameFull when an item brought back would take the name past maxItems;
// ErrOneDocument when it would be a second live Page info document.
func restoreItemVersion(ctx context.Context, database *sql.DB, siteID, collection string, id int64, a model.Actor, maxBytes int64, maxItems int) (model.CollectionItem, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, err
	}
	defer tx.Rollback()
	kind, err := repo.LockHistoryName(ctx, tx, siteID, collection)
	if err != nil {
		return model.CollectionItem{}, err
	}
	e, err := getHistoryEntry(ctx, tx, siteID, "list", collection, id)
	if err != nil {
		return model.CollectionItem{}, err
	}
	if e.ItemID == nil {
		return model.CollectionItem{}, sql.ErrNoRows
	}
	itemID := *e.ItemID
	cur, deleted, err := repo.LockHistoryItem(ctx, tx, itemID, siteID, collection)
	if err != nil {
		return model.CollectionItem{}, err
	}
	grew := false
	switch {
	case e.Op == "delete" || e.Op == "clear":
		if deleted.Valid {
			if err := repo.UndeleteHistoryItem(ctx, tx, itemID); err != nil {
				return model.CollectionItem{}, err
			}
			if err := repo.RecordHistory(ctx, tx, siteID, "list", collection, &itemID, "undelete", nil, a); err != nil {
				return model.CollectionItem{}, err
			}
			grew = true
		}
	case e.Value == nil:
		return model.CollectionItem{}, model.ErrNoEarlierValue
	default:
		if err := repo.RestoreHistoryItem(ctx, tx, itemID, e.Value); err != nil {
			return model.CollectionItem{}, err
		}
		if err := repo.RecordHistory(ctx, tx, siteID, "list", collection, &itemID, "restore", cur, a); err != nil {
			return model.CollectionItem{}, err
		}
		grew = deleted.Valid || len(e.Value) > len(cur)
	}
	if deleted.Valid {
		if err := itemsWithin(ctx, tx, siteID, collection, maxItems); err != nil {
			return model.CollectionItem{}, err
		}
		if err := oneDocument(ctx, tx, siteID, collection, kind); err != nil {
			return model.CollectionItem{}, err
		}
	}
	if grew {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return model.CollectionItem{}, err
		}
	}
	it, err := repo.HistoryItem(ctx, tx, itemID)
	if err != nil {
		return model.CollectionItem{}, err
	}
	return it, tx.Commit()
}

// UpdateItemVersioned is UpdateCollectionItemByID with a version check: when
// ifVersion > 0 and the item is at another version, nothing changes and the
// current item comes back with ErrVersionConflict. Every edit adds one to the
// item's version.
func updateItemVersioned(ctx context.Context, db *sql.DB, siteID, collection string, id, ifVersion int64, a model.Actor, maxBytes int64, fn func(json.RawMessage) (json.RawMessage, error)) (model.CollectionItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, err
	}
	defer tx.Rollback()
	var cur model.CollectionItem
	cur, err = repo.LockCollectionItem(ctx, tx, siteID, collection, id)
	if err != nil {
		return model.CollectionItem{}, err
	}
	old := cur.Data
	if ifVersion > 0 && cur.Version != ifVersion {
		return cur, model.ErrVersionConflict
	}
	next, err := fn(old)
	if err != nil {
		return model.CollectionItem{}, err
	}
	var it model.CollectionItem
	it, err = repo.UpdateCollectionItem(ctx, tx, siteID, collection, id, next)
	if err != nil {
		return model.CollectionItem{}, err
	}
	if err := repo.RecordHistory(ctx, tx, siteID, "list", collection, &id, "edit", old, a); err != nil {
		return model.CollectionItem{}, err
	}
	if len(it.Data) > len(old) {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return model.CollectionItem{}, err
		}
	}
	return it, tx.Commit()
}

// UpdateCollectionItemByID rewrites one live item's data inside a
// transaction: fn gets the stored JSON and returns the new JSON; the earlier
// data goes to the item's history. Returns sql.ErrNoRows when the item is not
// a live item of that site's collection, ErrSiteFull when the edit grows the
// site past maxBytes.
func updateCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, id int64, a model.Actor, maxBytes int64, fn func(json.RawMessage) (json.RawMessage, error)) (model.CollectionItem, error) {
	return updateItemVersioned(ctx, db, siteID, collection, id, 0, a, maxBytes, fn)
}

// roomAfter checks, inside a write's transaction, that a write which grew the
// site left it within maxBytes (ErrSiteFull otherwise; the caller rolls back).
func roomAfter(ctx context.Context, q sqlkit.Querier, siteID string, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	used, err := repo.SiteDataUsage(ctx, q, siteID)
	if err != nil {
		return err
	}
	if used > maxBytes {
		return model.ErrSiteFull
	}
	return nil
}

// oneDocument is ErrOneDocument when a Page info name (kind content) now
// holds more than one live item.
func oneDocument(ctx context.Context, tx *sql.Tx, siteID, name, kind string) error {
	if kind != "content" {
		return nil
	}
	many, err := repo.NameHoldsItems(ctx, tx, siteID, name, 1)
	if err != nil {
		return err
	}
	if many {
		return model.ErrOneDocument
	}
	return nil
}

// itemsWithin is ErrNameFull when a capped name (maxItems > 0) now holds more
// than maxItems live items.
func itemsWithin(ctx context.Context, tx *sql.Tx, siteID, name string, maxItems int) error {
	if maxItems <= 0 {
		return nil
	}
	over, err := repo.NameHoldsItems(ctx, tx, siteID, name, maxItems)
	if err != nil {
		return err
	}
	if over {
		return model.ErrNameFull
	}
	return nil
}

// getItemHistoryEntry reads list history; state-diff reconstruction stays in the state workflow.
func getHistoryEntry(ctx context.Context, q sqlkit.Querier, siteID, kind, name string, id int64) (model.HistoryEntry, error) {
	e, prev, err := repo.HistoryRecord(ctx, q, siteID, kind, name, id)
	if err != nil {
		return e, err
	}
	if prev != nil {
		e.Value = json.RawMessage(prev)
	}
	return e, nil
}

// ReadHistoryEntry is GetHistoryEntry in one read-only snapshot.
func readHistoryEntry(ctx context.Context, database *sql.DB, siteID, kind, name string, id int64) (model.HistoryEntry, error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return model.HistoryEntry{}, err
	}
	defer tx.Rollback()
	return getHistoryEntry(ctx, tx, siteID, kind, name, id)
}
