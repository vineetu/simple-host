package sitestest

import (
	"encoding/json"
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// TestDiscardVersion covers ?author= on deploys, the author in the version
// list, ?pending=1, and DELETE /v1/sites/{sitename}/versions/{version}
// including its refusals, the admin mirror and the deploy-key scope gate.
// Needs DB_DSN (newConnectorApp).
func TestDiscardVersion(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "discard-owner")
	key := map[string]string{"X-API-Key": owner.Key}
	const site = "discard-site"
	deploy := func(t *testing.T, method, query string, want int) {
		t.Helper()
		r := a.Do(t, method, "/v1/sites/"+site+"/files"+query, servertest.JSONBody(map[string]any{
			"files": map[string]string{"index.html": "<h1>" + query + "</h1>"},
		}), key)
		if r.Status != want {
			t.Fatalf("%s %s: %d %s", method, query, r.Status, r.Body)
		}
	}
	type row struct {
		VersionNumber int    `json:"version_number"`
		Status        string `json:"status"`
		Author        string `json:"author"`
		IsActive      bool   `json:"is_active"`
	}
	list := func(t *testing.T, query string) []row {
		t.Helper()
		r := a.Do(t, "GET", "/v1/sites/"+site+"/versions"+query, nil, key)
		if r.Status != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, r.Status, r.Body)
		}
		var out []row
		if err := json.Unmarshal(r.Body, &out); err != nil {
			t.Fatalf("list %s: %s", query, r.Body)
		}
		return out
	}
	byNumber := func(rows []row, n int) row {
		for _, v := range rows {
			if v.VersionNumber == n {
				return v
			}
		}
		t.Fatalf("version %d not in %+v", n, rows)
		return row{}
	}
	errCode := func(t *testing.T, r servertest.Resp, status int, code string) {
		t.Helper()
		var e map[string]any
		if r.Status != status || json.Unmarshal(r.Body, &e) != nil || e["code"] != code {
			t.Fatalf("got %d %s, want %d %s", r.Status, r.Body, status, code)
		}
	}

	long := strings.Repeat("é", 100)
	deploy(t, "POST", "?author=%20Alice%20", http.StatusCreated)                                  // v1, live
	deploy(t, "PUT", "?publish=false&author="+url.QueryEscape(long), http.StatusOK)               // v2, ready
	deploy(t, "PUT", "?publish=false&author="+url.QueryEscape("Bo\x01b\x7f"), http.StatusOK)      // v3, ready
	deploy(t, "PUT", "", http.StatusOK)                                                           // v4, live
	deploy(t, "PUT", "?create=1&publish=false&author="+url.QueryEscape("Rel bot"), http.StatusOK) // v5, ready
	all := list(t, "")
	if len(all) != 5 {
		t.Fatalf("want 5 versions, got %+v", all)
	}
	for n, want := range map[int]row{
		1: {1, "active", "Alice", false},
		2: {2, "ready", strings.Repeat("é", 80), false},
		3: {3, "ready", "Bob", false},
		4: {4, "active", "", true},
		5: {5, "ready", "Rel bot", false},
	} {
		if got := byNumber(all, n); got != want {
			t.Errorf("v%d = %+v, want %+v", n, got, want)
		}
	}
	// A raw list item always carries "author", even when empty.
	if !strings.Contains(string(a.Do(t, "GET", "/v1/sites/"+site+"/versions", nil, key).Body), `"author":""`) {
		t.Error(`list: a version without an author must still show "author":""`)
	}

	// Pending: ready and never live, newest first.
	pending := list(t, "?pending=1")
	if len(pending) != 3 || pending[0].VersionNumber != 5 || pending[1].VersionNumber != 3 || pending[2].VersionNumber != 2 {
		t.Fatalf("pending = %+v, want v5, v3, v2", pending)
	}
	// Making v3 live turns it active: it leaves the pending list for good.
	if r := a.Do(t, "PUT", "/v1/sites/"+site+"/active-version", servertest.JSONBody(map[string]any{"version_number": 3}), key); r.Status != http.StatusOK {
		t.Fatalf("make v3 live: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, "PUT", "/v1/sites/"+site+"/active-version", servertest.JSONBody(map[string]any{"version_number": 4}), key); r.Status != http.StatusOK {
		t.Fatalf("make v4 live again: %d %s", r.Status, r.Body)
	}
	if pending = list(t, "?pending=true"); len(pending) != 2 || pending[0].VersionNumber != 5 || pending[1].VersionNumber != 2 {
		t.Fatalf("pending after v3 was live = %+v, want v5, v2", pending)
	}

	var userID, siteID string
	if err := a.Database.QueryRow(`SELECT s.id, s.user_id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.username = $1 AND s.name = $2`, owner.Email, site).Scan(&siteID, &userID); err != nil {
		t.Fatal(err)
	}
	discard := func(t *testing.T, path string) servertest.Resp {
		t.Helper()
		return a.Do(t, "DELETE", path, nil, key)
	}
	base := "/v1/sites/" + site + "/versions/"
	errCode(t, discard(t, base+"4"), http.StatusConflict, "version_live")
	errCode(t, discard(t, base+"3"), http.StatusConflict, "version_published") // live once, not now
	errCode(t, discard(t, base+"1"), http.StatusConflict, "version_published")
	errCode(t, discard(t, base+"99"), http.StatusNotFound, "version_not_found")
	errCode(t, discard(t, base+"0"), http.StatusNotFound, "version_not_found")
	errCode(t, discard(t, base+"two"), http.StatusNotFound, "version_not_found")
	errCode(t, a.Do(t, "DELETE", "/v1/sites/no-such-site/versions/2", nil, key), http.StatusNotFound, "site_not_found")
	if r := a.Do(t, "DELETE", base+"2", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}

	v2dir := a.Sites.Disk.VersionDir(userID, site, 2)
	if _, err := os.Stat(v2dir); err != nil {
		t.Fatalf("v2 folder before discard: %v", err)
	}
	r := discard(t, base+"2")
	if r.Status != http.StatusOK {
		t.Fatalf("discard v2: %d %s", r.Status, r.Body)
	}
	if got := r.JSON(t); got["site"] != site || got["discarded_version"] != float64(2) || got["active_version"] != float64(4) {
		t.Fatalf("discard v2 body: %s", r.Body)
	}
	if _, err := os.Stat(v2dir); !os.IsNotExist(err) {
		t.Fatalf("v2 folder after discard: %v", err)
	}
	if _, err := os.Stat(a.Sites.Disk.VersionDir(userID, site, 1)); err != nil {
		t.Fatalf("v1 folder must stay: %v", err)
	}
	// The row stays as a tombstone so the number is never handed out again.
	var status string
	if err := a.Database.QueryRow(`SELECT status FROM versions WHERE site_id = $1 AND version_number = 2`, siteID).Scan(&status); err != nil || status != "discarded" {
		t.Fatalf("v2 row after discard: %q %v", status, err)
	}
	errCode(t, discard(t, base+"2"), http.StatusNotFound, "version_not_found")
	if r := a.Do(t, "POST", base+"2/preview-link", nil, key); r.Status < 400 {
		t.Fatalf("preview link for a discarded version: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, "PUT", "/v1/sites/"+site+"/active-version", servertest.JSONBody(map[string]any{"version_number": 2}), key); r.Status < 400 {
		t.Fatalf("rollback to a discarded version: %d %s", r.Status, r.Body)
	}
	if all = list(t, ""); len(all) != 4 {
		t.Fatalf("after discard: %+v", all)
	}

	// A deploy-only key is refused by the scope gate, not by the handler.
	mk := a.Do(t, "POST", "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "ci", "scope": "deploy"}), key)
	if mk.Status != http.StatusCreated {
		t.Fatalf("mint deploy key: %d %s", mk.Status, mk.Body)
	}
	dk, _ := mk.JSON(t)["api_key"].(string)
	errCode(t, a.Do(t, "DELETE", base+"5", nil, map[string]string{"X-API-Key": dk}), http.StatusForbidden, "deploy_only_key")
	if got := byNumber(list(t, "?pending=yes"), 5); got.Status != "ready" {
		t.Fatalf("v5 after refused discard: %+v", got)
	}

	// The admin mirror runs the same handler as the owner.
	r = a.Do(t, "DELETE", "/v1/admin/sites/"+siteID+"/versions/5", nil, map[string]string{"X-API-Key": a.Admin})
	if r.Status != http.StatusOK || r.JSON(t)["discarded_version"] != float64(5) {
		t.Fatalf("admin discard v5: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, "GET", "/v1/sites/"+site+"/versions?pending=1", nil, key); r.Status != http.StatusOK || strings.TrimSpace(string(r.Body)) != "[]" {
		t.Fatalf("empty pending list must be []: %d %s", r.Status, r.Body)
	}

	// v5 was the newest and is discarded: the next draft is v6, not v5 again.
	deploy(t, "PUT", "?publish=false", http.StatusOK)
	if got := list(t, "?pending=1"); len(got) != 1 || got[0].VersionNumber != 6 {
		t.Fatalf("after discarding the newest, the next draft = %+v, want v6", got)
	}
	// Retention drops the tombstones below its floor but reports only real
	// versions: v1 and v3 had been live; v2 and v5 were discarded.
	r = a.Do(t, "PUT", "/v1/sites/"+site+"/keep-versions", servertest.JSONBody(map[string]any{"keep_versions": 1}), key)
	if r.Status != http.StatusOK {
		t.Fatalf("keep-versions: %d %s", r.Status, r.Body)
	}
	var kept struct {
		Removed []int `json:"removed_versions"`
	}
	if err := json.Unmarshal(r.Body, &kept); err != nil || len(kept.Removed) != 2 || kept.Removed[0]+kept.Removed[1] != 4 || kept.Removed[0]*kept.Removed[1] != 3 {
		t.Fatalf("removed_versions = %v (%v), want v1 and v3: %s", kept.Removed, err, r.Body)
	}
	var max int
	if err := a.Database.QueryRow(`SELECT max(version_number) FROM versions WHERE site_id = $1`, siteID).Scan(&max); err != nil || max != 6 {
		t.Fatalf("max version after prune = %d %v, want 6", max, err)
	}
}
