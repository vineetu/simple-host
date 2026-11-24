package erase_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	access "simplehost/access/core"
	"simplehost/core/testkit"
	datasql "simplehost/data/sql"
	"simplehost/ops/erase"
)

// SPEC test 5 (F3): delete account removes sites, data, keys and app
// connections. A team they are the only member of goes with them.
func TestDeleteAccount(t *testing.T) {
	k := testkit.Start(t, erase.Mount, datasql.Mount)
	ctx := context.Background()
	nina, olga := k.Person(t, "nina"), k.Person(t, "olga")
	k.Publish(t, nina, "blog", map[string]string{"index.html": "hi"})
	b := k.Browser()
	auth := []string{"Authorization", "Bearer " + nina.Key}
	testkit.Call(t, b, "POST", "https://simple-host.app/v1/sites/blog/storage/sqlite/notes/execute", map[string]string{"sql": "CREATE TABLE notes (body TEXT)"}, auth...)
	k.Exec(t, `INSERT INTO oauth_clients (id, name) VALUES ('c1', 'Claude')`)
	k.Exec(t, `INSERT INTO oauth_grants (person_id, client_id) VALUES ($1, 'c1')`, nina.ID)
	// A team of nina alone, with a site, and a team she shares with olga.
	team := func(handle string, members ...testkit.Who) testkit.Who {
		id, err := access.NewTeam(ctx, k.App.DB, handle)
		if err == nil {
			err = access.ClaimName(ctx, k.App.DB, handle, "handle", id, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range members {
			k.Exec(t, `INSERT INTO team_members (team_id, person_id) VALUES ($1, $2)`, id, m.ID)
		}
		return testkit.Who{ID: id, Handle: handle}
	}
	solo, shared := team("ninas", nina), team("both", nina, olga)
	k.Publish(t, solo, "shop", map[string]string{"index.html": "shop"})
	k.Publish(t, shared, "club", map[string]string{"index.html": "club"})
	blog, _ := k.App.Sites.Get(ctx, nina.ID, "blog")

	if r := testkit.Call(t, b, "DELETE", "https://simple-host.app/v1/me", nil, auth...); r.Status != 200 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	count := func(q string, args ...any) (n int) {
		k.App.DB.QueryRow(q, args...).Scan(&n)
		return n
	}
	if n := count(`SELECT count(*) FROM sites WHERE owner_id IN ($1, $2)`, nina.ID, solo.ID); n != 0 {
		t.Fatalf("%d sites left", n)
	}
	if n := count(`SELECT count(*) FROM keys`) + count(`SELECT count(*) FROM oauth_grants`); n != 1 { // olga's key stays
		t.Fatalf("%d keys and app connections left, want olga's key", n)
	}
	if _, err := os.Stat(filepath.Join(k.App.Config.DataDir, "sites", blog.ID)); !os.IsNotExist(err) {
		t.Fatalf("the site's files and data are still on disk: %v", err)
	}
	if n := count(`SELECT count(*) FROM sites WHERE owner_id = $1`, shared.ID); n != 1 {
		t.Fatalf("the shared team's site went too")
	}
	if r := testkit.Call(t, b, "DELETE", "https://simple-host.app/v1/me", nil, auth...); r.Status != 401 {
		t.Fatalf("the key after deleting: %d", r.Status)
	}
	if err := access.ClaimName(ctx, k.App.DB, "nina", "handle", olga.ID, ""); err == nil {
		t.Fatal("the handle can be taken again")
	}
}
