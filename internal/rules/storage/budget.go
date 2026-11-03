package storage

// ExtractBudget is the byte budget the unpacked files are held to: the
// account's own limit when it has an override, else 0, the instance's caps.
func ExtractBudget(limit int64, override bool) int64 {
	if override {
		return limit
	}
	return 0
}
