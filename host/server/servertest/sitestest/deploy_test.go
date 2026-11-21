package sitestest

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// ?keep=1 merges the upload onto the live version: unnamed files stay,
// remove_files go, and a removal path may not leave the site.
func TestKeepOtherFiles(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "keep-files")
	image := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	r := a.api(t, owner, "POST", "/v1/sites/big/files", map[string]any{"files": map[string]string{"index.html": "home", "about.html": "old", "remove.html": "drop"}, "files_base64": map[string]string{"photo.png": base64.StdEncoding.EncodeToString(image)}})
	if r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	r = a.api(t, owner, "PUT", "/v1/sites/big/files?keep=1", map[string]any{"files": map[string]string{"about.html": "new"}, "remove_files": []string{"remove.html"}})
	if r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	for _, file := range []struct{ path, want string }{{"index.html", "home"}, {"about.html", "new"}, {"photo.png", string(image)}} {
		r = a.api(t, owner, "GET", "/v1/sites/big/versions/2/files/"+file.path, nil)
		if r.Status != 200 || string(r.Body) != file.want {
			t.Fatal(file.path, r.Status, string(r.Body))
		}
	}
	if r = a.api(t, owner, "GET", "/v1/sites/big/versions/2/files/remove.html", nil); r.Status != 404 {
		t.Fatal("removed file kept")
	}
	for _, tc := range []struct {
		path   string
		remove []string
	}{{"/v1/sites/big/files", []string{"about.html"}}, {"/v1/sites/big/files?keep=1", []string{"../outside"}}, {"/v1/sites/big/files?keep=1", []string{`a\..\outside`}}} {
		if r = a.api(t, owner, "PUT", tc.path, map[string]any{"files": map[string]string{"index.html": "new"}, "remove_files": tc.remove}); r.Status != 400 {
			t.Fatal(tc.path, r.Status, string(r.Body))
		}
	}
	if r = a.api(t, owner, "PUT", "/v1/sites/missing/files?keep=1", map[string]any{"files": map[string]string{"about.html": "new"}}); r.Status != 404 {
		t.Fatal(r.Status, string(r.Body))
	}
	// A site without a live version accepts an ordinary complete first version.
	if _, err := a.Database.Exec(`UPDATE sites SET active_version = 0 WHERE name = 'big' AND user_id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if r = a.api(t, owner, "PUT", "/v1/sites/big/files?keep=1", map[string]any{"files": map[string]string{"index.html": "fresh"}}); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
}

// B3: the expiry sweep keeps a taken-down site, as the purge does.
func TestExpirySweepKeepsTakenDownSite(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "expiry-held")
	a.Deploy(t, owner, "held")
	if _, err := a.Database.Exec(`UPDATE sites SET expires_at = now() - interval '1 hour', suspended_at = now() WHERE name = 'held' AND user_id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	a.Sites.SweepExpiredSites()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE name = 'held' AND user_id = $1`, owner.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("taken-down site swept", n, err)
	}
}

// MAX_SITES_OVERRIDES replaces the per-account cap for the listed handles
// only, and follows an account's earlier handles.
func TestSiteOverridesEnforced(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.SetLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "2", "MAX_SITES_OVERRIDES": olive.Handle + ":4"})
	fill := func(uid string, n int) {
		t.Helper()
		if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
			SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, n); err != nil {
			t.Fatal(err)
		}
	}
	create := func(p rt.Person, name string) rt.Resp {
		return a.files(t, p, "POST", name, map[string]string{"index.html": "x"})
	}
	quota := func(r rt.Resp) bool { return r.Status == http.StatusForbidden && r.Code() == "site_quota_reached" }

	fill(oscar.ID, 2)
	if r := create(oscar, "fresh"); !quota(r) {
		t.Fatalf("oscar over the global cap: %d %s", r.Status, r.Body)
	}
	// A handle change keeps the override (the old handle stays an alias).
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, olive.Handle, olive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, olive.Handle+"-new", olive.ID); err != nil {
		t.Fatal(err)
	}
	fill(olive.ID, 2)
	if r := create(olive, "third"); r.Status != http.StatusCreated {
		t.Fatalf("olive under her override: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at) VALUES ($1, 'gone-x', now())`, olive.ID); err != nil {
		t.Fatal(err)
	}
	if r := create(olive, "fifth"); !quota(r) || !strings.Contains(string(r.Body), "4") {
		t.Fatalf("olive over her override: %d %s", r.Status, r.Body)
	}
}
