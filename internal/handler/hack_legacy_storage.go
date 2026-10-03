package handler

import (
	"net/http"
	"strings"
)

// HackLegacyStorageGate removes the old saved-data API from hosted Simple Hack.
// It wraps both public requests and the in-process REST calls made by MCP.
// Simple Host keeps the existing routes and stored data is never changed here.
func HackLegacyStorageGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hackMode && hackLegacyStoragePath(r.URL.Path) {
			writeJSON(w, http.StatusGone, errorResponse{
				Error: "Simple Hack website storage now uses KV, SQLite and files under /v1/sites/{site}/storage",
				Code:  "legacy_storage_removed",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hackLegacyStoragePath(path string) bool {
	switch path {
	case "/v1/data-notify/stop", "/v1/admin/data-watch":
		return true
	}
	parts := strings.Split(path, "/")
	// /v1/sites/{site}/{legacy...}
	if len(parts) >= 5 && parts[1] == "v1" && parts[2] == "sites" && parts[3] != "" {
		return hackLegacySiteSegment(parts[4])
	}
	// /v1/u/{handle}/sites/{site}/{legacy...}
	if len(parts) >= 7 && parts[1] == "v1" && parts[2] == "u" && parts[3] != "" && parts[4] == "sites" && parts[5] != "" {
		return hackLegacySiteSegment(parts[6])
	}
	// Admin aliases for reading a site's old collections.
	return len(parts) >= 6 && parts[1] == "v1" && parts[2] == "admin" && parts[3] == "sites" && parts[4] != "" && parts[5] == "collections"
}

func hackLegacySiteSegment(segment string) bool {
	switch segment {
	case "state", "collections", "data", "savers", "history", "allowed-origins", "allow-anonymous-writes":
		return true
	}
	return false
}
