package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

// publicGate runs under the declaration lock before a private name (cur,
// curPrivate) becomes readable by anyone as kind with private. Items in
// Recently deleted count: they come back under the name on a restore.
func publicGate(ctx context.Context, tx *sql.Tx, siteID, name, cur string, curPrivate bool, kind string, private, confirmPublic bool) error {
	if !storagerules.LegacyPublicGateCounts(cur, curPrivate, kind, private) {
		return nil
	}
	live, deleted, err := repo.CountCollectionItems(ctx, tx, siteID, name)
	if err != nil {
		return err
	}
	if !storagerules.LegacyPublicGateRefuses(kind, confirmPublic, live, deleted) {
		return nil
	}
	return &model.PrivateItemsError{Live: live, Deleted: deleted, Content: kind == model.KindContent}
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
func (svc *Adapter) DeclareDataLocked(ctx context.Context, siteID, name, kind string, private, onePerPerson bool, notify string, confirmPublic bool) error {
	tx, err := svc.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, curPrivate, err := repo.LockDeclaration(ctx, tx, siteID, name)
	if err != nil {
		return err
	}
	if cur != kind && (cur == model.KindPersonal || kind == model.KindPersonal) {
		has, err := repo.NameHasRows(ctx, tx, siteID, name)
		if err != nil {
			return err
		}
		if has {
			return model.ErrNameHasRows
		}
	}
	if err := publicGate(ctx, tx, siteID, name, cur, curPrivate, kind, private, confirmPublic); err != nil {
		return err
	}
	if kind == model.KindContent && cur != model.KindContent {
		many, err := repo.NameHoldsItems(ctx, tx, siteID, name, 1)
		if err != nil {
			return err
		}
		if many {
			return model.ErrSeveralItems
		}
	}
	if err := repo.DeclareData(ctx, tx, siteID, name, kind, private, onePerPerson, notify); err != nil {
		return err
	}
	return tx.Commit()
}

// SetCollectionPrivateLocked is SetCollectionPrivate under the declaration
// lock, with publicGate: a private list made public while it holds items,
// live or in Recently deleted, needs confirmPublic (*PrivateItemsError).
func (svc *Adapter) SetCollectionPrivateLocked(ctx context.Context, siteID, name string, private, confirmPublic bool) error {
	tx, err := svc.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cur, curPrivate, err := repo.LockDeclaration(ctx, tx, siteID, name)
	if err != nil {
		return err
	}
	if err := publicGate(ctx, tx, siteID, name, cur, curPrivate, cur, private, confirmPublic); err != nil {
		return err
	}
	if err := repo.UpdatePrivate(ctx, tx, siteID, name, private); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreStateVersion puts the document back as it was before change id. The
// restore is itself a change, so it can be undone too. ErrSiteFull when that
// grows the site past maxBytes.
func (svc *Adapter) RestoreStateVersion(ctx context.Context, siteID string, id int64, a model.Actor, maxBytes int64) (json.RawMessage, int, error) {
	tx, err := svc.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	cur, _, size, err := repo.LockState(ctx, tx, siteID)
	if err != nil {
		return nil, 0, err
	}
	e, err := getHistoryEntry(ctx, tx, siteID, model.HistoryState, "", id)
	if err != nil {
		return nil, 0, err
	}
	if e.Value == nil {
		return nil, 0, model.ErrNoEarlierValue
	}
	ver, err := setStateWithin(ctx, tx, siteID, e.Value, size, maxBytes)
	if err != nil {
		return nil, 0, err
	}
	if err := repo.RecordStateChange(ctx, tx, siteID, model.OpRestore, cur, e.Value, a); err != nil {
		return nil, 0, err
	}
	var stored []byte
	if err := repo.CurrentStateValue(ctx, tx, siteID, &stored); err != nil {
		return nil, 0, err
	}
	return json.RawMessage(stored), ver, tx.Commit()
}

// setStateWithin writes the document (row locked by lockState) and returns
// ErrSiteFull when the document grew and the site is now past maxBytes. The
// caller rolls back on error.
func setStateWithin(ctx context.Context, q *sql.Tx, siteID string, state json.RawMessage, oldSize, maxBytes int64) (int, error) {
	version, size, total, err := repo.UpdateLockedState(ctx, q, siteID, state)
	if err != nil {
		return 0, err
	}
	if maxBytes > 0 && size > oldSize && total > maxBytes {
		return 0, model.ErrSiteFull
	}
	return version, nil
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
func (svc *Adapter) UndeleteItems(ctx context.Context, siteID, collection string, id int64, a model.Actor, maxBytes int64, onlyCleared bool, within time.Duration, maxItems int) (int64, error) {
	tx, err := svc.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	kind, err := lockNameFor(ctx, tx, siteID, collection)
	if err != nil {
		return 0, err
	}
	n, err := repo.UndeleteItems(ctx, tx, siteID, collection, id, a, onlyCleared, within)
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

// lockNameFor takes the name's declaration-row lock when it has one, so a
// restore and an add (or a change of kind) cannot interleave, and returns
// its kind ("" when undeclared).
func lockNameFor(ctx context.Context, tx *sql.Tx, siteID, name string) (string, error) {
	return repo.LockHistoryName(ctx, tx, siteID, name)
}
