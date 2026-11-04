package storage

// LegacyPublicGateCounts reports whether a declaration change must inspect
// existing private items before making them public.
func LegacyPublicGateCounts(current string, currentPrivate bool, kind string, private bool) bool {
	public := kind == "content" || kind == "board" || (kind != "mine" && !private)
	return current != "mine" && currentPrivate && public
}

// LegacyPublicGateRefuses reports whether existing items block that change.
func LegacyPublicGateRefuses(kind string, confirmPublic bool, live, deleted int64) bool {
	return live+deleted != 0 && (kind == "content" || !confirmPublic)
}
