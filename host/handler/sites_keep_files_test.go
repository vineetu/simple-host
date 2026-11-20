package handler

import (
	"encoding/base64"
	"testing"
)

func TestKeepOtherFiles(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "keep-files")
	headers := map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}
	image := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	r := a.do(t, "POST", "/v1/sites/big/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "home", "about.html": "old", "remove.html": "drop"}, "files_base64": map[string]string{"photo.png": base64.StdEncoding.EncodeToString(image)}}), headers)
	if r.status != 201 {
		t.Fatal(r.status, string(r.body))
	}
	r = a.do(t, "PUT", "/v1/sites/big/files?keep=1", jsonBody(map[string]any{"files": map[string]string{"about.html": "new"}, "remove_files": []string{"remove.html"}}), headers)
	if r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	for _, file := range []struct{ path, want string }{{"index.html", "home"}, {"about.html", "new"}, {"photo.png", string(image)}} {
		r = a.do(t, "GET", "/v1/sites/big/versions/2/files/"+file.path, nil, headers)
		if r.status != 200 || string(r.body) != file.want {
			t.Fatal(file.path, r.status, string(r.body))
		}
	}
	if r = a.do(t, "GET", "/v1/sites/big/versions/2/files/remove.html", nil, headers); r.status != 404 {
		t.Fatal("removed file kept")
	}
	for _, tc := range []struct {
		path   string
		remove []string
	}{{"/v1/sites/big/files", []string{"about.html"}}, {"/v1/sites/big/files?keep=1", []string{"../outside"}}, {"/v1/sites/big/files?keep=1", []string{`a\..\outside`}}} {
		r = a.do(t, "PUT", tc.path, jsonBody(map[string]any{"files": map[string]string{"index.html": "new"}, "remove_files": tc.remove}), headers)
		if r.status != 400 {
			t.Fatal(tc.path, r.status, string(r.body))
		}
	}
	// Keeping on a missing site preserves today's missing-site response.
	r = a.do(t, "PUT", "/v1/sites/missing/files?keep=1", jsonBody(map[string]any{"files": map[string]string{"about.html": "new"}}), headers)
	if r.status != 404 {
		t.Fatal(r.status, string(r.body))
	}
	// A site without a live version still accepts the ordinary complete first version.
	if _, err := a.database.Exec(`UPDATE sites SET active_version = 0 WHERE name = 'big' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.email); err != nil {
		t.Fatal(err)
	}
	r = a.do(t, "PUT", "/v1/sites/big/files?keep=1", jsonBody(map[string]any{"files": map[string]string{"index.html": "fresh"}}), headers)
	if r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
}

// B3: the expiry sweep keeps a taken-down site, as the purge does.
func TestExpirySweepKeepsTakenDownSite(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "expiry-held")
	headers := map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}
	if r := a.do(t, "POST", "/v1/sites/held/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "x"}}), headers); r.status != 201 {
		t.Fatal(r.status, string(r.body))
	}
	if _, err := a.database.Exec(`UPDATE sites SET expires_at = now() - interval '1 hour', suspended_at = now() WHERE name = 'held' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.email); err != nil {
		t.Fatal(err)
	}
	a.sites.sweepExpiredSites()
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE name = 'held' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.email).Scan(&n); err != nil || n != 1 {
		t.Fatal("taken-down site swept", n, err)
	}
}
