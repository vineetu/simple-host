package sitestest

import (
	"encoding/base64"
	"github.com/vsriram/simple-host/host/server/servertest"
	"testing"
)

func TestKeepOtherFiles(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "keep-files")
	headers := map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/json"}
	image := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	r := a.Do(t, "POST", "/v1/sites/big/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "home", "about.html": "old", "remove.html": "drop"}, "files_base64": map[string]string{"photo.png": base64.StdEncoding.EncodeToString(image)}}), headers)
	if r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	r = a.Do(t, "PUT", "/v1/sites/big/files?keep=1", servertest.JSONBody(map[string]any{"files": map[string]string{"about.html": "new"}, "remove_files": []string{"remove.html"}}), headers)
	if r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	for _, file := range []struct{ path, want string }{{"index.html", "home"}, {"about.html", "new"}, {"photo.png", string(image)}} {
		r = a.Do(t, "GET", "/v1/sites/big/versions/2/files/"+file.path, nil, headers)
		if r.Status != 200 || string(r.Body) != file.want {
			t.Fatal(file.path, r.Status, string(r.Body))
		}
	}
	if r = a.Do(t, "GET", "/v1/sites/big/versions/2/files/remove.html", nil, headers); r.Status != 404 {
		t.Fatal("removed file kept")
	}
	for _, tc := range []struct {
		path   string
		remove []string
	}{{"/v1/sites/big/files", []string{"about.html"}}, {"/v1/sites/big/files?keep=1", []string{"../outside"}}, {"/v1/sites/big/files?keep=1", []string{`a\..\outside`}}} {
		r = a.Do(t, "PUT", tc.path, servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "new"}, "remove_files": tc.remove}), headers)
		if r.Status != 400 {
			t.Fatal(tc.path, r.Status, string(r.Body))
		}
	}
	// Keeping on a missing site preserves today's missing-site response.
	r = a.Do(t, "PUT", "/v1/sites/missing/files?keep=1", servertest.JSONBody(map[string]any{"files": map[string]string{"about.html": "new"}}), headers)
	if r.Status != 404 {
		t.Fatal(r.Status, string(r.Body))
	}
	// A site without a live version still accepts the ordinary complete first version.
	if _, err := a.Database.Exec(`UPDATE sites SET active_version = 0 WHERE name = 'big' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.Email); err != nil {
		t.Fatal(err)
	}
	r = a.Do(t, "PUT", "/v1/sites/big/files?keep=1", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "fresh"}}), headers)
	if r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
}

// B3: the expiry sweep keeps a taken-down site, as the purge does.
func TestExpirySweepKeepsTakenDownSite(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "expiry-held")
	headers := map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/json"}
	if r := a.Do(t, "POST", "/v1/sites/held/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "x"}}), headers); r.Status != 201 {
		t.Fatal(r.Status, string(r.Body))
	}
	if _, err := a.Database.Exec(`UPDATE sites SET expires_at = now() - interval '1 hour', suspended_at = now() WHERE name = 'held' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.Email); err != nil {
		t.Fatal(err)
	}
	a.Sites.SweepExpiredSites()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE name = 'held' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.Email).Scan(&n); err != nil || n != 1 {
		t.Fatal("taken-down site swept", n, err)
	}
}
