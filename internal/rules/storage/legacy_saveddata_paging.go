package storage

import (
	"github.com/vsriram/simple-host/internal/model"
)

func LegacyHistoryPage(limitValue, beforeValue string) (int, int64) {
	return model.LegacyHistoryPage(limitValue, beforeValue)
}
func LegacyHistoryNext(entries []model.HistoryEntry, limit int) *int64 {
	return model.LegacyHistoryNext(entries, limit)
}
