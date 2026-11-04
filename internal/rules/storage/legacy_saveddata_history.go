package storage

import (
	"github.com/vsriram/simple-host/internal/model"
)

func LegacyHistoryThinMargin(capBytes int64) int64 { return model.LegacyHistoryThinMargin(capBytes) }
func LegacyHistoryThinPointless(size, stuck, capBytes int64, hasStuck bool) bool {
	return model.LegacyHistoryThinPointless(size, stuck, capBytes, hasStuck)
}
