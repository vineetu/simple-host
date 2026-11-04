package model

import (
	"strconv"
)

// LegacyHistoryPage parses the saved-data history page size and cursor.
func LegacyHistoryPage(limitValue, beforeValue string) (int, int64) {
	var before int64
	if v, err := strconv.ParseInt(beforeValue, 10, 64); err == nil && v > 0 {
		before = v
	}
	limit := 50
	if v, err := strconv.Atoi(limitValue); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}
	return limit, before
}

// LegacyHistoryNext returns the next cursor for a full history page.
func LegacyHistoryNext(entries []HistoryEntry, limit int) *int64 {
	if len(entries) < limit || len(entries) == 0 {
		return nil
	}
	n := entries[len(entries)-1].ID
	return &n
}
