package hackevents

import (
	"database/sql"
	"testing"
	"time"
)

func TestDirectoryGroupFollowsStage(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	later := sql.NullTime{Time: now.Add(48 * time.Hour), Valid: true}
	for stage, want := range map[string]string{
		"open": "upcoming", "building": "upcoming", "closed": "now", "judging": "now", "results": "past", "archived": "past",
	} {
		if got := DirectoryGroup(stage, later, "UTC", later, now); got != want {
			t.Errorf("%s with a future start: got %q, want %q", stage, got, want)
		}
	}
}
