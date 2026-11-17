package handler

import "strings"

// LegacyStoragePath reports whether path names an old saved-data route.
func LegacyStoragePath(path string) bool {
	return parseLegacyStoragePath(path).match
}

// parseLegacyStoragePath classifies the old saved-data routes. skip is the
// instance-wide notify/watch paths (no site to resolve). handle, site and
// adminID are filled when the path names them.
func parseLegacyStoragePath(path string) legacyStoragePath {
	switch path {
	case "/v1/data-notify/stop", "/v1/admin/data-watch":
		return legacyStoragePath{match: true, skip: true}
	}
	parts := strings.Split(path, "/")
	// /v1/sites/{site}/{legacy...}
	if len(parts) >= 5 && parts[1] == "v1" && parts[2] == "sites" && parts[3] != "" {
		if legacySiteSegment(parts[4]) {
			return legacyStoragePath{match: true, site: parts[3]}
		}
		return legacyStoragePath{}
	}
	// /v1/u/{handle}/sites/{site}/{legacy...}
	if len(parts) >= 7 && parts[1] == "v1" && parts[2] == "u" && parts[3] != "" && parts[4] == "sites" && parts[5] != "" {
		if legacySiteSegment(parts[6]) {
			return legacyStoragePath{match: true, handle: parts[3], site: parts[5]}
		}
		return legacyStoragePath{}
	}
	// Admin aliases for reading a site's old collections.
	if len(parts) >= 6 && parts[1] == "v1" && parts[2] == "admin" && parts[3] == "sites" && parts[4] != "" && parts[5] == "collections" {
		return legacyStoragePath{match: true, adminID: parts[4]}
	}
	return legacyStoragePath{}
}

type legacyStoragePath struct {
	match   bool
	skip    bool
	handle  string
	site    string
	adminID string
}

func legacySiteSegment(segment string) bool {
	switch segment {
	case "state", "collections", "data", "savers", "history", "allowed-origins", "allow-anonymous-writes":
		return true
	}
	return false
}
