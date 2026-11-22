package entries

import (
	"net/http"
	"testing"
)

// Tests for the fixes from the 2026-11-02 full production test of Simple
// Hack (findings 1, 2, 5, 6, 8) and security review finding F1.

// publishTeamSite gives the event's holding account a live site named after
// the team (what loadTeamList reads), at version 1.
func PublishTeamSite(t *testing.T, a *hackApp, slug, teamSlug string) {
	t.Helper()
	var accountID string
	if err := a.DB.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO sites (user_id, name, site_url, active_version) VALUES ($1, $2, $3, 1)`,
		accountID, teamSlug, "https://example.test/"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO versions (site_id, version_number, disk_path, status)
		SELECT id, 1, 'x', 'active' FROM sites WHERE user_id = $1 AND name = $2`, accountID, teamSlug); err != nil {
		t.Fatal(err)
	}
}

func JudgeQueueByTeam(t *testing.T, w *rulesWorld) (map[string]map[string]any, map[string]any) {
	t.Helper()
	q := w.a.At(t, "GET", "/v1/hack/events/"+w.Slug+"/judge/queue", nil, w.a.Key(w.Judge))
	if q.Status != http.StatusOK {
		t.Fatalf("queue: %d %s", q.Status, q.Body)
	}
	out := map[string]map[string]any{}
	body := q.JSON(t)
	for _, raw := range body["queue"].([]any) {
		item := raw.(map[string]any)
		out[item["team_id"].(string)] = item
	}
	return out, body
}
