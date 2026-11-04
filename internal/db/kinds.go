package db

import (
	"context"
	"database/sql"
	"encoding/json"
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
var ErrVersionConflict = model.ErrVersionConflict

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
var ErrOnePerPerson = model.ErrOnePerPerson

// ErrNameFull: the name holds as many entries as it may.
var ErrNameFull = model.ErrNameFull

// ErrNameHasRows: the name holds items (live or in Recently deleted), so it
// cannot become Personal, or stop being Personal.
var ErrNameHasRows = model.ErrNameHasRows

// ErrSeveralItems: a list with more than one live item cannot become Page
// info, which is one document.
var ErrSeveralItems = model.ErrSeveralItems

// ErrKindChanged: the name's declaration changed after the caller read it
// (the owner changed its kind at the same moment); nothing was saved.
var ErrKindChanged = legacy.ErrKindChanged

// PrivateItemsError: a change would make a private name readable by anyone
// while it holds items, live or in Recently deleted (a restore would bring
// those back under the public name). Content: the change is to Page info,
// which a private name becomes only when it holds nothing at all; any other
// change needs the owner's confirm_public.
type PrivateItemsError = model.PrivateItemsError

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
func BaseEmail(email string) string { return model.BaseEmail(email) }

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

// ---- Personal (mine) --------------------------------------------------------------

// ErrPeopleFull: a Personal name already holds a record for as many people
// as it may (SAVED_DATA_PERSONAL_PEOPLE_MAX).
var ErrPeopleFull = model.ErrPeopleFull

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
