package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/legacy"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

// Saved data, step 2: kinds (saved-data redesign, approved 2026-09-27).
//
// A data name on a site is declared once as one kind:
//   - KindContent ("Page info"): one document the owner writes and anyone
//     reads. Stored as the one live row of collection_items for that name, so
//     it has the same history, undo and size accounting as a list item.
//   - KindEntries ("Submissions"): items visitors send. The owner reads all;
//     each visitor reads, changes and withdraws their own; everyone reads
//     them only when the owner made the name public (collection_settings.private
//     = false).
//
// A name with no kind (collection_settings.kind NULL or no row) is Shared:
// anyone reads it and signed-in visitors save to it, exactly as before the
// kinds. An install with SAVED_DATA_DEFAULT_KIND=declare_first makes it take
// no saves instead, on every site made after the kinds (legacy_data false).

// The kinds a name can be declared as.
const (
	KindContent = "content"
	KindEntries = "entries"
	// KindPersonal ("Personal"): one private record per signed-in person,
	// the live row of that name whose submitted_by is the person. Only that
	// person reads or writes it; the owner sees counts and sizes only.
	KindPersonal = "mine"
	// KindBoard ("Shared board"): a list anyone who can open the site reads;
	// signed-in visitors allowed to save add, change (with the item's
	// version) and delete items one at a time; only the owner clears it.
	KindBoard = "board"
)

// ErrVersionConflict: the item changed since the version the caller named
// (If-Match); the current item comes back with it.
var ErrVersionConflict = errors.New("the item changed since that version")

// Notify choices for Submissions.
const (
	NotifyOff   = "off"
	NotifyEach  = "each"
	NotifyDaily = "daily"
)

// Who may save (sites.savers_mode).
const (
	SaversAnyone = "anyone"
	SaversListed = "listed"
)

// ErrOnePerPerson: this person already has a live entry in a one-per-person name.
var ErrOnePerPerson = errors.New("one entry per person")

// ErrNameFull: the name holds as many entries as it may.
var ErrNameFull = errors.New("this list is full")

// ErrNameHasRows: the name holds items (live or in Recently deleted), so it
// cannot become Personal, or stop being Personal.
var ErrNameHasRows = errors.New("the name holds items")

// ErrSeveralItems: a list with more than one live item cannot become Page
// info, which is one document.
var ErrSeveralItems = errors.New("the name holds several items")

// ErrKindChanged: the name's declaration changed after the caller read it
// (the owner changed its kind at the same moment); nothing was saved.
var ErrKindChanged = legacy.ErrKindChanged

// PrivateItemsError: a change would make a private name readable by anyone
// while it holds items, live or in Recently deleted (a restore would bring
// those back under the public name). Content: the change is to Page info,
// which a private name becomes only when it holds nothing at all; any other
// change needs the owner's confirm_public.
type PrivateItemsError struct {
	Live, Deleted int64
	Content       bool
}

func (e *PrivateItemsError) Error() string { return "the private name holds items" }

// publicGate runs under the declaration lock before a private name (cur,
// curPrivate) becomes readable by anyone as kind with private. Items in
// Recently deleted count: they come back under the name on a restore.
func publicGate(ctx context.Context, tx *sql.Tx, siteID, name, cur string, curPrivate bool, kind string, private, confirmPublic bool) error {
	if !storagerules.LegacyPublicGateCounts(cur, curPrivate, kind, private) {
		return nil
	}
	live, deleted, err := legacy.CountCollectionItems(ctx, tx, siteID, name)
	if err != nil {
		return err
	}
	if !storagerules.LegacyPublicGateRefuses(kind, confirmPublic, live, deleted) {
		return nil
	}
	return &PrivateItemsError{Live: live, Deleted: deleted, Content: kind == KindContent}
}

// DeclareDataLocked is DeclareData under the name's declaration-row lock, the
// one SavePersonal, AppendEntry and PutContent take, so no save lands between
// the checks and the change. Refused, all checked under that lock:
//   - a change to or from Personal while the name holds any item, live or in
//     Recently deleted (ErrNameHasRows): a Personal record never becomes an
//     item the owner reads, and an item never becomes someone's record;
//   - a private name made readable by anyone (Page info, a Shared board,
//     public Submissions) while it holds items, live or in Recently deleted
//     (*PrivateItemsError): never for Page info, and otherwise only with
//     confirmPublic;
//   - a list with several live items made Page info (ErrSeveralItems).
func DeclareDataLocked(ctx context.Context, database *sql.DB, siteID, name, kind string, private, onePerPerson bool, notify string, confirmPublic bool) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, curPrivate, err := legacy.LockDeclaration(ctx, tx, siteID, name)
	if err != nil {
		return err
	}
	if cur != kind && (cur == KindPersonal || kind == KindPersonal) {
		has, err := NameHasRows(ctx, tx, siteID, name)
		if err != nil {
			return err
		}
		if has {
			return ErrNameHasRows
		}
	}
	if err := publicGate(ctx, tx, siteID, name, cur, curPrivate, kind, private, confirmPublic); err != nil {
		return err
	}
	if kind == KindContent && cur != KindContent {
		many, err := NameHoldsItems(ctx, tx, siteID, name, 1)
		if err != nil {
			return err
		}
		if many {
			return ErrSeveralItems
		}
	}
	if err := DeclareData(ctx, tx, siteID, name, kind, private, onePerPerson, notify); err != nil {
		return err
	}
	return tx.Commit()
}

// SetCollectionPrivateLocked is SetCollectionPrivate under the declaration
// lock, with publicGate: a private list made public while it holds items,
// live or in Recently deleted, needs confirmPublic (*PrivateItemsError).
func SetCollectionPrivateLocked(ctx context.Context, database *sql.DB, siteID, name string, private, confirmPublic bool) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, curPrivate, err := legacy.LockDeclaration(ctx, tx, siteID, name)
	if err != nil {
		return err
	}
	if err := publicGate(ctx, tx, siteID, name, cur, curPrivate, cur, private, confirmPublic); err != nil {
		return err
	}
	if err := legacy.UpdatePrivate(ctx, tx, siteID, name, private); err != nil {
		return err
	}
	return tx.Commit()
}

// BaseEmail normalizes an address for block lists.
func BaseEmail(email string) string { return storagerules.BaseEmail(email) }

// ---- Page info (content) --------------------------------------------------------

// GetContent returns the name's document (the live row), ok=false when
// nothing was saved yet.
func GetContent(ctx context.Context, q Querier, siteID, name string) (CollectionItem, bool, error) {
	return legacy.GetContent(ctx, q, siteID, name)
}

// ---- Submissions (entries) ------------------------------------------------------

// AppendEntry adds one entry by a signed-in person (a.ID) to a declared
// Submissions name: at most maxItems live entries in the name, and with
// onePerPerson at most one live entry per person (ErrOnePerPerson, with the
// id of the one they have; 0 when it was sent by another account of the same
// address). A person is the account or its address with any +tag dropped
// (BaseEmail). The declaration row is locked, so two saves at once cannot
// both pass either check; under it the name must still be kind (Submissions,
// or a Shared board) with the privacy the caller read (private), else
// ErrKindChanged.
func AppendEntry(ctx context.Context, database *sql.DB, siteID, name, kind string, private bool, data json.RawMessage, a Actor, onePerPerson bool, maxItems int) (CollectionItem, int64, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	defer tx.Rollback()
	// Under the lock, the name is still what the caller read: a change of
	// kind (to Page info, say) or of privacy (a private list made public
	// while this entry was on its way) may have committed since.
	cur, curPrivate, err := legacy.LockEntryDeclaration(ctx, tx, siteID, name)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	if cur != kind || curPrivate != private {
		return CollectionItem{}, 0, ErrKindChanged
	}
	if onePerPerson && a.ID != "" {
		have, mine, err := legacy.LivePersonEntry(ctx, tx, siteID, name, a.ID, BaseEmail(a.Email), 0)
		if err != nil {
			return CollectionItem{}, 0, err
		}
		if have != 0 {
			if !mine {
				have = 0 // another account of the same address: nothing to point at
			}
			return CollectionItem{}, have, ErrOnePerPerson
		}
	}
	full, err := NameHoldsItems(ctx, tx, siteID, name, maxItems-1)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	if full {
		return CollectionItem{}, 0, ErrNameFull
	}
	it, err := legacy.InsertEntry(ctx, tx, siteID, name, data, a)
	if err != nil {
		return CollectionItem{}, 0, err
	}
	return it, 0, tx.Commit()
}

// ListOwnEntries is one person's live entries in a name, newest first.
func ListOwnEntries(ctx context.Context, database *sql.DB, siteID, name, userID string, limit int, before int64) ([]CollectionItem, error) {
	return legacy.ListOwnEntries(ctx, database, siteID, name, userID, limit, before)
}

// ItemAuthor is who sent one item of a name and whether it is deleted.
// sql.ErrNoRows when the name has no such item.
type ItemAuthor = model.ItemAuthor

// GetItemAuthor reads the stamped author of an item.
func GetItemAuthor(ctx context.Context, q Querier, siteID, name string, id int64) (ItemAuthor, error) {
	return legacy.GetItemAuthor(ctx, q, siteID, name, id)
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
func UndoOwnWithdrawal(ctx context.Context, database *sql.DB, siteID, name string, id int64, userID string, window time.Duration, declared, onePerPerson bool, maxItems int, a Actor, maxBytes int64, anyAuthor bool) (CollectionItem, bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, false, err
	}
	defer tx.Rollback()
	if declared {
		if err := legacy.LockContentDeclaration(ctx, tx, siteID, name); err != nil {
			return CollectionItem{}, false, err
		}
	}
	op, actor, at, err := legacy.LockWithdrawal(ctx, tx, siteID, name, id, userID, anyAuthor)
	if errors.Is(err, sql.ErrNoRows) {
		return CollectionItem{}, false, nil
	}
	if err != nil {
		return CollectionItem{}, false, err
	}
	if op.String != OpDelete || actor.String != userID || time.Since(at) > window {
		return CollectionItem{}, false, nil
	}
	if declared && onePerPerson {
		other, _, err := legacy.LivePersonEntry(ctx, tx, siteID, name, userID, BaseEmail(a.Email), id)
		if err != nil {
			return CollectionItem{}, false, err
		}
		if other != 0 {
			return CollectionItem{}, false, ErrOnePerPerson
		}
	}
	if declared {
		full, err := NameHoldsItems(ctx, tx, siteID, name, maxItems-1)
		if err != nil {
			return CollectionItem{}, false, err
		}
		if full {
			return CollectionItem{}, false, ErrNameFull
		}
	}
	it, err := legacy.RestoreWithdrawal(ctx, tx, id)
	if err != nil {
		return CollectionItem{}, false, err
	}
	if err := recordHistory(ctx, tx, siteID, HistoryList, name, &id, OpUndelete, nil, a); err != nil {
		return CollectionItem{}, false, err
	}
	if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
		return CollectionItem{}, false, err
	}
	return it, true, tx.Commit()
}

// ---- Personal (mine) --------------------------------------------------------------

// GetPersonal is userID's live record in a Personal name, ok=false when
// they have none (never saved, or deleted).
func GetPersonal(ctx context.Context, q Querier, siteID, name, userID string) (CollectionItem, bool, error) {
	return legacy.GetPersonal(ctx, q, siteID, name, userID)
}

// PersonalItemID is the id of userID's record in a Personal name, live or in
// Recently deleted; 0 when they never saved one.
func PersonalItemID(ctx context.Context, q Querier, siteID, name, userID string) (int64, error) {
	return legacy.PersonalItemID(ctx, q, siteID, name, userID)
}

// SavePersonal writes a.ID's record in a Personal name: fn gets the live
// record (nil when there is none) and returns the new one. Each person has
// one row per name for good: a deleted record (by them, or cleared by the
// owner) is written over and comes back live, so a restore never makes two.
// The earlier value goes to history. The declaration row is locked, so two
// first saves at once cannot make two rows. ErrSiteFull when it grows the
// site past maxBytes; ErrPeopleFull when the person has no row yet and
// peopleMax people already have one (live or in Recently deleted).
func SavePersonal(ctx context.Context, database *sql.DB, siteID, name string, a Actor, maxBytes int64, peopleMax int, fn func(cur json.RawMessage) (json.RawMessage, error)) (CollectionItem, error) {
	if a.ID == "" {
		return CollectionItem{}, errors.New("a personal record needs a signed-in person")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	if err := legacy.LockPersonalDeclaration(ctx, tx, siteID, name); err != nil {
		return CollectionItem{}, err
	}
	id, old, deleted, err := legacy.LockPersonalItem(ctx, tx, siteID, name, a.ID)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CollectionItem{}, err
	}
	var cur json.RawMessage
	if found && !deleted.Valid {
		cur = old
	}
	next, err := fn(cur)
	if err != nil {
		return CollectionItem{}, err
	}
	var it CollectionItem
	if found {
		it, err = legacy.UpdatePersonalItem(ctx, tx, id, next, a.Email)
		if err != nil {
			return CollectionItem{}, err
		}
		if err := recordHistory(ctx, tx, siteID, HistoryList, name, &id, OpEdit, old, a); err != nil {
			return CollectionItem{}, err
		}
	} else {
		if peopleMax > 0 {
			n, err := legacy.CountPersonalItems(ctx, tx, siteID, name, peopleMax)
			if err != nil {
				return CollectionItem{}, err
			}
			if n >= peopleMax {
				return CollectionItem{}, ErrPeopleFull
			}
		}
		it, err = legacy.InsertPersonalItem(ctx, tx, siteID, name, next, a)
		if err != nil {
			return CollectionItem{}, err
		}
	}
	if !found || deleted.Valid || len(it.Data) > len(old) {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return CollectionItem{}, err
		}
	}
	return it, tx.Commit()
}

// ErrPeopleFull: a Personal name already holds a record for as many people
// as it may (SAVED_DATA_PERSONAL_PEOPLE_MAX).
var ErrPeopleFull = errors.New("this Personal name has as many people as it may")

// ForgetClearedPersonal is a person deleting their record after the owner's
// clear took it: nothing changes but a delete by them in its history, so the
// owner's "Restore" (which brings back only what a clear took) leaves it
// deleted. ok=false when they have no record that a clear took last.
func ForgetClearedPersonal(ctx context.Context, database *sql.DB, siteID, name string, a Actor) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	id, op, err := legacy.LockClearedPersonal(ctx, tx, siteID, name, a.ID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && op.String != OpClear) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := recordHistory(ctx, tx, siteID, HistoryList, name, &id, OpDelete, nil, a); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ListItemHistory is one item's changes, newest first (a person's own
// Personal record). before > 0 pages back.
func ListItemHistory(ctx context.Context, database *sql.DB, siteID, name string, itemID int64, limit int, before int64) ([]HistoryEntry, error) {
	return legacy.ListItemHistory(ctx, database, siteID, name, itemID, limit, before)
}

// ---- who may save ---------------------------------------------------------------

// Savers is a site's who-may-save setting.
type Savers = model.Savers

// GetSavers reads the site's who-may-save setting.
func GetSavers(ctx context.Context, database *sql.DB, siteID string) (Savers, error) {
	return legacy.GetSavers(ctx, database, siteID)
}

// AddBlocked adds a pattern to the site's block list.
func AddBlocked(ctx context.Context, database *sql.DB, siteID, pattern string) (int, error) {
	return legacy.AddBlocked(ctx, database, siteID, pattern)
}

// ---- email on new submissions ---------------------------------------------------

// NotifyDue is one name whose owner should be emailed about new entries.
type NotifyDue = model.NotifyDue

// DueNotifications lists names with pending submission emails.
func DueNotifications(ctx context.Context, database *sql.DB, each, daily time.Duration) ([]NotifyDue, error) {
	return legacy.DueNotifications(ctx, database, each, daily)
}

// ClaimNotification records a submission email claim.
func ClaimNotification(ctx context.Context, database *sql.DB, d NotifyDue) (bool, error) {
	return legacy.ClaimNotification(ctx, database, d)
}

// SetNotify changes a name's email choice.
func SetNotify(ctx context.Context, database *sql.DB, siteID, name, notify string) (bool, error) {
	return legacy.SetNotify(ctx, database, siteID, name, notify)
}
