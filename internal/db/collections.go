package db

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/vsriram/simple-host/internal/repository/legacy"
)

// UpdateCollectionItemByID rewrites one live item's data inside a
// transaction: fn gets the stored JSON and returns the new JSON; the earlier
// data goes to the item's history. Returns sql.ErrNoRows when the item is not
// a live item of that site's collection, ErrSiteFull when the edit grows the
// site past maxBytes.
func UpdateCollectionItemByID(ctx context.Context, db *sql.DB, siteID, collection string, id int64, a Actor, maxBytes int64, fn func(json.RawMessage) (json.RawMessage, error)) (CollectionItem, error) {
	return UpdateItemVersioned(ctx, db, siteID, collection, id, 0, a, maxBytes, fn)
}

// UpdateItemVersioned is UpdateCollectionItemByID with a version check: when
// ifVersion > 0 and the item is at another version, nothing changes and the
// current item comes back with ErrVersionConflict. Every edit adds one to the
// item's version.
func UpdateItemVersioned(ctx context.Context, db *sql.DB, siteID, collection string, id, ifVersion int64, a Actor, maxBytes int64, fn func(json.RawMessage) (json.RawMessage, error)) (CollectionItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, err
	}
	defer tx.Rollback()
	var cur CollectionItem
	cur, err = legacy.LockCollectionItem(ctx, tx, siteID, collection, id)
	if err != nil {
		return CollectionItem{}, err
	}
	old := cur.Data
	if ifVersion > 0 && cur.Version != ifVersion {
		return cur, ErrVersionConflict
	}
	next, err := fn(old)
	if err != nil {
		return CollectionItem{}, err
	}
	var it CollectionItem
	it, err = legacy.UpdateCollectionItem(ctx, tx, siteID, collection, id, next)
	if err != nil {
		return CollectionItem{}, err
	}
	if err := recordHistory(ctx, tx, siteID, HistoryList, collection, &id, OpEdit, old, a); err != nil {
		return CollectionItem{}, err
	}
	if len(it.Data) > len(old) {
		if err := roomAfter(ctx, tx, siteID, maxBytes); err != nil {
			return CollectionItem{}, err
		}
	}
	return it, tx.Commit()
}
