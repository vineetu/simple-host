package handler

import (
	"os"
	"testing"
)

// TestServeStorageRulesBrowser shares the isolated owner, hosts and captured
// mail fixture with the preset checks, with rules saves and SQLite KV enabled.
// scripts/e2e/e2e-storage-rules.mjs drives the dashboard and stops this fixture.
func TestServeStorageRulesBrowser(t *testing.T) {
	file := os.Getenv("STORAGE_RULES_FIXTURE")
	if file == "" {
		t.Skip("STORAGE_RULES_FIXTURE unset")
	}
	serveVisitorRecordsBrowser(t, file)
}
