package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// MAX_SITES_OVERRIDES replaces the per-account cap for the listed handles
// only, follows an account's earlier handles, and is what /v1/me reports.
func TestSiteOverridesEnforced(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	oliveID, oliveHandle := a.UserID(t, olive)
	oscarID, _ := a.UserID(t, oscar)
	servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "2", "MAX_SITES_OVERRIDES": oliveHandle + ":4"}))

	fill := func(uid string, n int) {
		t.Helper()
		if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
			SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, n); err != nil {
			t.Fatal(err)
		}
	}
	create := func(p servertest.Person, name string) servertest.Resp {
		return a.At(t, "POST", "simple-host.test", "/v1/sites/"+name+"/files",
			map[string]any{"files": map[string]string{"index.html": "x"}}, map[string]string{"X-API-Key": p.Key})
	}
	maxSites := func(p servertest.Person) any {
		return a.At(t, "GET", "simple-host.test", "/v1/me", nil, map[string]string{"X-API-Key": p.Key}).JSON(t)["max_sites"]
	}
	quota := func(r servertest.Resp) bool {
		return r.Status == http.StatusForbidden && strings.Contains(string(r.Body), "site_quota_reached")
	}

	// Without an override: the global cap of 2.
	fill(oscarID, 2)
	if r := create(oscar, "fresh"); !quota(r) {
		t.Fatalf("oscar over the global cap: %d %s", r.Status, r.Body)
	}
	if got := maxSites(oscar); got != float64(2) {
		t.Fatalf("oscar max_sites = %v, want 2", got)
	}

	// With one: 4, past the global cap.
	fill(oliveID, 2)
	if r := create(olive, "third"); r.Status != http.StatusCreated && r.Status != http.StatusOK {
		t.Fatalf("olive under her override: %d %s", r.Status, r.Body)
	}
	if got := maxSites(olive); got != float64(4) {
		t.Fatalf("olive max_sites = %v, want 4", got)
	}
	if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at) VALUES ($1, 'gone-x', now())`, oliveID); err != nil {
		t.Fatal(err)
	}
	if r := create(olive, "fifth"); !quota(r) || !strings.Contains(string(r.Body), "at most 4 sites") {
		t.Fatalf("olive over her override: %d %s", r.Status, r.Body)
	}

	// A handle change keeps the override (the old handle stays an alias).
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, oliveHandle, oliveID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, oliveHandle+"-new", oliveID); err != nil {
		t.Fatal(err)
	}
	if got := maxSites(olive); got != float64(4) {
		t.Fatalf("olive max_sites after a handle change = %v, want 4", got)
	}

	// The admin list reads every alias at once; a held handle with no
	// account (user_id NULL) must not break it.
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, NULL)`, oliveHandle+"-held"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM handle_aliases WHERE handle = $1`, oliveHandle+"-held") })
	all, err := accountsrepo.HandleAliasesByUser(context.Background(), a.Database)
	if err != nil || len(all[oliveID]) != 1 || all[oliveID][0] != oliveHandle {
		t.Fatalf("aliases by user: %v %v", all[oliveID], err)
	}
}
