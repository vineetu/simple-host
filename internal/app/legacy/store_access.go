package legacy

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
)

// AddBlocked adds a pattern to the site's block list.
func (svc *Adapter) AddBlocked(ctx context.Context, siteID string, pattern string) (int, error) {
	return repo.AddBlocked(ctx, svc.database, siteID, pattern)
}

// ClearSiteHistory removes, for good, every earlier version of a site's saved
// data and list items. The data itself and Recently deleted stay. Returns how
// many changes went.
func (svc *Adapter) ClearSiteHistory(ctx context.Context, siteID string) (int64, error) {
	return repo.ClearSiteHistory(ctx, svc.database, siteID)
}

// CountKindNames is how many names on the site are declared kind, other
// than except.
func (svc *Adapter) CountKindNames(ctx context.Context, siteID string, kind string, except string) (int, error) {
	return repo.CountKindNames(ctx, svc.database, siteID, kind, except)
}

// GetDataSettings reads a name declaration and the site legacy flag.
func (svc *Adapter) GetDataSettings(ctx context.Context, siteID string, name string) (model.DataSettings, error) {
	return repo.GetDataSettings(ctx, svc.database, siteID, name)
}

// GetItemAuthor reads the stamped author of an item.
func (svc *Adapter) GetItemAuthor(ctx context.Context, siteID string, name string, id int64) (model.ItemAuthor, error) {
	return repo.GetItemAuthor(ctx, svc.database, siteID, name, id)
}

// GetSavers reads the site's who-may-save setting.
func (svc *Adapter) GetSavers(ctx context.Context, siteID string) (model.Savers, error) {
	return repo.GetSavers(ctx, svc.database, siteID)
}

// ListCollectionSummariesByID returns the site's collection inventory.
func (svc *Adapter) ListCollectionSummariesByID(ctx context.Context, siteID string) ([]model.CollectionSummary, error) {
	return repo.ListCollectionSummariesByID(ctx, svc.database, siteID)
}

func (svc *Adapter) ListDataWatch(ctx context.Context, days int) ([]model.LegacyDataWatchSite, time.Time, error) {
	return repo.ListDataWatch(ctx, svc.database, days)
}

// ListDeletedItems returns a page of deleted collection items.
func (svc *Adapter) ListDeletedItems(ctx context.Context, siteID string, collection string, limit int, after model.LegacyDeletedCursor) ([]model.LegacyDeletedItem, error) {
	return repo.ListDeletedItems(ctx, svc.database, siteID, collection, limit, after)
}

// ListHistory returns one target's changes newest first: the document
// (kind state) or every item of one list (kind list). before > 0 pages back.
func (svc *Adapter) ListHistory(ctx context.Context, siteID string, kind string, name string, limit int, before int64) ([]model.HistoryEntry, error) {
	return repo.ListHistory(ctx, svc.database, siteID, kind, name, limit, before)
}

// NameHasRows reports whether name holds any item at all, live or in
// Recently deleted (a name that holds data cannot become Personal, and a
// Personal name that holds records cannot become anything else).
func (svc *Adapter) NameHasRows(ctx context.Context, siteID string, name string) (bool, error) {
	return repo.NameHasRows(ctx, svc.database, siteID, name)
}

// PurgeDeletedItems removes, for good, one item of a list's Recently deleted
// (id > 0) or all of them (id == 0), with their history. Live items are never
// touched. Returns how many went.
func (svc *Adapter) PurgeDeletedItems(ctx context.Context, siteID string, collection string, id int64) (int64, error) {
	return repo.PurgeDeletedItems(ctx, svc.database, siteID, collection, id)
}

// ReadHistoryEntry is GetHistoryEntry in one read-only snapshot.
func (svc *Adapter) ReadHistoryEntry(ctx context.Context, siteID string, kind string, name string, id int64) (model.HistoryEntry, error) {
	return readHistoryEntry(ctx, svc.database, siteID, kind, name, id)
}

// RestoreItemVersion undoes list change id: a delete or clear brings the item
// back; an edit or an earlier restore puts back the item's data from before
// it (and brings the item back if it was deleted since). ErrSiteFull when
// that grows the site past maxBytes; with maxItems > 0 (a Shared board),
// ErrNameFull when an item brought back would take the name past maxItems;
// ErrOneDocument when it would be a second live Page info document.
func (svc *Adapter) RestoreItemVersion(ctx context.Context, siteID string, collection string, id int64, a model.Actor, maxBytes int64, maxItems int) (model.CollectionItem, error) {
	return restoreItemVersion(ctx, svc.database, siteID, collection, id, a, maxBytes, maxItems)
}

// SetNotify changes a name's email choice.
func (svc *Adapter) SetNotify(ctx context.Context, siteID string, name string, notify string) (bool, error) {
	return repo.SetNotify(ctx, svc.database, siteID, name, notify)
}

// SoftClearCollection marks every live item of one list deleted and returns
// how many. The list's private/public setting stays.
func (svc *Adapter) SoftClearCollection(ctx context.Context, siteID string, collection string, a model.Actor) (int64, error) {
	return repo.SoftClearCollection(ctx, svc.database, siteID, collection, a)
}

// SoftDeleteItem marks one live item deleted. ok=false when it is not a live
// item of that list.
func (svc *Adapter) SoftDeleteItem(ctx context.Context, siteID string, collection string, id int64, a model.Actor) (bool, error) {
	return softDeleteItem(ctx, svc.database, siteID, collection, id, a)
}

// UpdateCollectionItemByID rewrites one live item's data inside a
// transaction: fn gets the stored JSON and returns the new JSON; the earlier
// data goes to the item's history. Returns sql.ErrNoRows when the item is not
// a live item of that site's collection, ErrSiteFull when the edit grows the
// site past maxBytes.
func (svc *Adapter) UpdateCollectionItemByID(ctx context.Context, siteID string, collection string, id int64, a model.Actor, maxBytes int64, fn func(json.RawMessage) (json.RawMessage, error)) (model.CollectionItem, error) {
	return updateCollectionItemByID(ctx, svc.database, siteID, collection, id, a, maxBytes, fn)
}
