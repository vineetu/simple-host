package model

// LegacyHistoryThinMargin is the growth needed to retry an ineffective thin.
func LegacyHistoryThinMargin(capBytes int64) int64 {
	return max(capBytes/16, 64<<10)
}

// LegacyHistoryThinPointless reports whether growth since the last thin is small.
func LegacyHistoryThinPointless(size, stuck, capBytes int64, hasStuck bool) bool {
	return hasStuck && size < stuck+LegacyHistoryThinMargin(capBytes)
}
