package storage

import (
	"github.com/vsriram/simple-host/internal/model"
)

// LegacyDataListReadable reports whether a declaration supports list reads.
func LegacyDataListReadable(kind string, shared bool) bool {
	return LegacyEntriesLike(kind, shared) || kind == "board"
}

// LegacyDataReadLimited reports whether the data route takes a read token.
func LegacyDataReadLimited(kind string, shared, count bool) bool {
	return kind == "content" || !LegacyDataListReadable(kind, shared) || count
}

// LegacyDataRoute is the action for a data read after declaration checks.
type LegacyDataRoute = model.LegacyDataRoute

const (
	LegacyDataPersonal   = model.LegacyDataPersonal
	LegacyDataContent    = model.LegacyDataContent
	LegacyDataUndeclared = model.LegacyDataUndeclared
	LegacyDataCount      = model.LegacyDataCount
	LegacyDataList       = model.LegacyDataList
)

// LegacyDataReadRoute selects the data read operation for a declaration.
func LegacyDataReadRoute(kind string, shared, count bool) LegacyDataRoute {
	switch {
	case kind == "mine":
		return LegacyDataPersonal
	case kind == "content":
		return LegacyDataContent
	case !LegacyDataListReadable(kind, shared):
		return LegacyDataUndeclared
	case count:
		return LegacyDataCount
	default:
		return LegacyDataList
	}
}
