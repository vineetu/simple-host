package db

import (
	"context"
	legacy "github.com/vsriram/simple-host/internal/repository/legacy"
)

// CountKindNames is how many names on the site are declared kind, other
// than except.
func CountKindNames(ctx context.Context, q Querier, siteID, kind, except string) (int, error) {
	return legacy.CountKindNames(ctx, q, siteID, kind, except)
}

// CountLiveItems is how many live items a name holds.
func CountLiveItems(ctx context.Context, q Querier, siteID, name string) (int64, error) {
	return legacy.CountLiveItems(ctx, q, siteID, name)
}

// NameHasRows reports whether name holds any item at all, live or in
// Recently deleted (a name that holds data cannot become Personal, and a
// Personal name that holds records cannot become anything else).
func NameHasRows(ctx context.Context, q Querier, siteID, name string) (bool, error) {
	return legacy.NameHasRows(ctx, q, siteID, name)
}
