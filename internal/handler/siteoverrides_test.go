package handler

import (
	"net/http"
	"strings"
	"testing"
)

// MAX_SITES_OVERRIDES replaces the per-account cap for the listed handles
// only, follows an account's earlier handles, and is what /v1/me reports.
func TestSiteOverridesEnforced(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	oliveID, oliveHandle := a.userID(t, olive)
	oscarID, _ := a.userID(t, oscar)
	withLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "2", "MAX_SITES_OVERRIDES": oliveHandle + ":4"})

	fill := func(uid string, n int) {
		t.Helper()
		if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
			SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, n); err != nil {
			t.Fatal(err)
		}
	}
	create := func(p person, name string) resp {
		return a.at(t, "POST", "simple-host.test", "/v1/sites/"+name+"/files",
			map[string]any{"files": map[string]string{"index.html": "x"}}, map[string]string{"X-API-Key": p.key})
	}
	maxSites := func(p person) any {
		return a.at(t, "GET", "simple-host.test", "/v1/me", nil, map[string]string{"X-API-Key": p.key}).json(t)["max_sites"]
	}
	quota := func(r resp) bool {
		return r.status == http.StatusForbidden && strings.Contains(string(r.body), "site_quota_reached")
	}

	// Without an override: the global cap of 2.
	fill(oscarID, 2)
	if r := create(oscar, "fresh"); !quota(r) {
		t.Fatalf("oscar over the global cap: %d %s", r.status, r.body)
	}
	if got := maxSites(oscar); got != float64(2) {
		t.Fatalf("oscar max_sites = %v, want 2", got)
	}

	// With one: 4, past the global cap.
	fill(oliveID, 2)
	if r := create(olive, "third"); r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("olive under her override: %d %s", r.status, r.body)
	}
	if got := maxSites(olive); got != float64(4) {
		t.Fatalf("olive max_sites = %v, want 4", got)
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, deleted_at) VALUES ($1, 'gone-x', now())`, oliveID); err != nil {
		t.Fatal(err)
	}
	if r := create(olive, "fifth"); !quota(r) || !strings.Contains(string(r.body), "at most 4 sites") {
		t.Fatalf("olive over her override: %d %s", r.status, r.body)
	}

	// A handle change keeps the override (the old handle stays an alias).
	if _, err := a.database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, oliveHandle, oliveID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, oliveHandle+"-new", oliveID); err != nil {
		t.Fatal(err)
	}
	if got := maxSites(olive); got != float64(4) {
		t.Fatalf("olive max_sites after a handle change = %v, want 4", got)
	}
}
