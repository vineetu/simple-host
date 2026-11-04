package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/legacy"
	"time"
)

type IdempotentResponse = model.LegacyIdempotentResponse
type DataWatchSite = model.LegacyDataWatchSite

// NameHoldsItems reports whether a name has more than max live items.
func NameHoldsItems(ctx context.Context, q Querier, siteID, name string, max int) (bool, error) {
	return legacy.NameHoldsItems(ctx, q, siteID, name, max)
}

func ClaimIdempotencyKey(ctx context.Context, d *sql.DB, scope []byte, siteID string, hash []byte, stale time.Duration) (bool, IdempotentResponse, error) {
	return legacy.ClaimIdempotencyKey(ctx, d, scope, siteID, hash, stale)
}
func SaveIdempotentResponse(ctx context.Context, d *sql.DB, scope []byte, status int, etag string, ref int64) error {
	return legacy.SaveIdempotentResponse(ctx, d, scope, status, etag, ref)
}
func ReleaseIdempotencyKey(ctx context.Context, d *sql.DB, scope []byte) error {
	return legacy.ReleaseIdempotencyKey(ctx, d, scope)
}
func PurgeIdempotencyKeys(ctx context.Context, d *sql.DB, keep time.Duration, max int) (int64, error) {
	return legacy.PurgeIdempotencyKeys(ctx, d, keep, max)
}
func PurgeDataWatch(ctx context.Context, d *sql.DB, days int) (int64, error) {
	return legacy.PurgeDataWatch(ctx, d, days)
}
func BumpDataWatch(ctx context.Context, d *sql.DB, siteID, metric string, n int64) error {
	return legacy.BumpDataWatch(ctx, d, siteID, metric, n)
}
func ListDataWatch(ctx context.Context, d *sql.DB, days int) ([]DataWatchSite, time.Time, error) {
	return legacy.ListDataWatch(ctx, d, days)
}

// CollectionItem is one row in an append-only collection.
type CollectionItem = model.CollectionItem

// ListCollectionItemsByID returns a page of live collection items.
func ListCollectionItemsByID(ctx context.Context, d *sql.DB, siteID, collection string, limit int, before int64, withAuthor bool) ([]CollectionItem, error) {
	return legacy.ListCollectionItemsByID(ctx, d, siteID, collection, limit, before, withAuthor)
}

// IsCollectionPrivate reports the collection's privacy setting.
func IsCollectionPrivate(ctx context.Context, d *sql.DB, siteID, collection string) (bool, error) {
	return legacy.IsCollectionPrivate(ctx, d, siteID, collection)
}

// SetCollectionPrivate records the collection's privacy setting.
func SetCollectionPrivate(ctx context.Context, d *sql.DB, siteID, collection string, private bool) error {
	return legacy.SetCollectionPrivate(ctx, d, siteID, collection, private)
}

// CollectionExistsByID reports whether the collection has live items.
func CollectionExistsByID(ctx context.Context, d *sql.DB, siteID, collection string) (bool, error) {
	return legacy.CollectionExistsByID(ctx, d, siteID, collection)
}

// CollectionNameUsed reports whether the collection name was ever used.
func CollectionNameUsed(ctx context.Context, d *sql.DB, siteID, collection string) (bool, error) {
	return legacy.CollectionNameUsed(ctx, d, siteID, collection)
}

// ListCollectionKeysByID returns the collection's object keys.
func ListCollectionKeysByID(ctx context.Context, d *sql.DB, siteID, collection string) ([]string, error) {
	return legacy.ListCollectionKeysByID(ctx, d, siteID, collection)
}

// ForEachCollectionItemByID walks the collection's live items.
func ForEachCollectionItemByID(ctx context.Context, d *sql.DB, siteID, collection string, fn func(CollectionItem) error) error {
	return legacy.ForEachCollectionItemByID(ctx, d, siteID, collection, fn)
}

// GetCollectionItemByID returns an item, live or deleted.
func GetCollectionItemByID(ctx context.Context, d *sql.DB, siteID, collection string, id int64) (CollectionItem, error) {
	return legacy.GetCollectionItemByID(ctx, d, siteID, collection, id)
}

// CollectionSummary describes a named collection.
type CollectionSummary = model.CollectionSummary

// PersonalFewMax is the minimum count shown for Personal records.
const PersonalFewMax = legacy.PersonalFewMax

// ListCollectionSummariesByID returns the site's collection inventory.
func ListCollectionSummariesByID(ctx context.Context, d *sql.DB, siteID string) ([]CollectionSummary, error) {
	return legacy.ListCollectionSummariesByID(ctx, d, siteID)
}

// Actor identifies the person who made a saved-data change.
type Actor = model.Actor

// AppendCollectionItemByID appends an item to an undeclared collection.
func AppendCollectionItemByID(ctx context.Context, d *sql.DB, siteID, collection string, private bool, data json.RawMessage, by Actor) (CollectionItem, error) {
	return legacy.AppendCollectionItemByID(ctx, d, siteID, collection, private, data, by)
}

// EnsureAdminUser ensures the admin row exists and returns its ID.
func EnsureAdminUser(ctx context.Context, d *sql.DB) (string, error) {
	return legacy.EnsureAdminUser(ctx, d)
}

// SiteDataUsage returns a site's live saved-data usage in bytes.
func SiteDataUsage(ctx context.Context, q Querier, siteID string) (int64, error) {
	return legacy.SiteDataUsage(ctx, q, siteID)
}

// DataSettings is one data name's declaration.
type DataSettings = model.DataSettings

// GetDataSettings reads a name declaration and the site legacy flag.
func GetDataSettings(ctx context.Context, q Querier, siteID, name string) (DataSettings, error) {
	return legacy.GetDataSettings(ctx, q, siteID, name)
}

// DeclareData records a name declaration.
func DeclareData(ctx context.Context, q Querier, siteID, name, kind string, private, onePerPerson bool, notify string) error {
	return legacy.DeclareData(ctx, q, siteID, name, kind, private, onePerPerson, notify)
}

// DeletedItem is an item in a list's Recently deleted.
type DeletedItem = model.LegacyDeletedItem

// DeletedCursor identifies a page position in Recently deleted.
type DeletedCursor = model.LegacyDeletedCursor

// ListDeletedItems returns a page of deleted collection items.
func ListDeletedItems(ctx context.Context, d *sql.DB, siteID, collection string, limit int, after DeletedCursor) ([]DeletedItem, error) {
	return legacy.ListDeletedItems(ctx, d, siteID, collection, limit, after)
}
