package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestVersionAuthor(t *testing.T) {
	long := strings.Repeat("é", 100)
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  Alice  ", "Alice"},
		{"Bo\x01b\x7f", "Bob"},
		{"A\u0085B\u009f", "AB"}, // C1 controls go too
		{"\tTab\nNew\rLine ", "TabNewLine"},
		{long, strings.Repeat("é", 80)},
		{strings.Repeat("a", 79) + " b", strings.Repeat("a", 79)}, // trimmed again after the cut
		{"Claude (release bot) <bot@example.com>", "Claude (release bot) <bot@example.com>"},
	} {
		r := httptest.NewRequest("PUT", "/v1/sites/s/files?"+url.Values{"author": {c.in}}.Encode(), nil)
		if got := versionAuthor(r); got != c.want {
			t.Errorf("versionAuthor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := versionAuthor(httptest.NewRequest("PUT", "/v1/sites/s/files", nil)); got != "" {
		t.Errorf("no ?author=: %q, want empty", got)
	}
}

func TestPendingParam(t *testing.T) {
	for in, want := range map[string]bool{"": false, "1": true, "true": true, "YES": true, "0": false, "no": false, "maybe": false} {
		r := httptest.NewRequest("GET", "/v1/sites/s/versions?pending="+in, nil)
		if got := pendingParam(r); got != want {
			t.Errorf("pending=%q: %v, want %v", in, got, want)
		}
	}
}

// TestDiscardVersion covers ?author= on deploys, the author in the version
// list, ?pending=1, and DELETE /v1/sites/{sitename}/versions/{version}
// including its refusals, the admin mirror and the deploy-key scope gate.
// Needs DB_DSN (newConnectorApp).
func TestDiscardVersion(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "discard-owner")
	key := map[string]string{"X-API-Key": owner.key}
	const site = "discard-site"
	deploy := func(t *testing.T, method, query string, want int) {
		t.Helper()
		r := a.do(t, method, "/v1/sites/"+site+"/files"+query, jsonBody(map[string]any{
			"files": map[string]string{"index.html": "<h1>" + query + "</h1>"},
		}), key)
		if r.status != want {
			t.Fatalf("%s %s: %d %s", method, query, r.status, r.body)
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
		r := a.do(t, "GET", "/v1/sites/"+site+"/versions"+query, nil, key)
		if r.status != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, r.status, r.body)
		}
		var out []row
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatalf("list %s: %s", query, r.body)
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
	errCode := func(t *testing.T, r resp, status int, code string) {
		t.Helper()
		var e map[string]any
		if r.status != status || json.Unmarshal(r.body, &e) != nil || e["code"] != code {
			t.Fatalf("got %d %s, want %d %s", r.status, r.body, status, code)
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
	if !strings.Contains(string(a.do(t, "GET", "/v1/sites/"+site+"/versions", nil, key).body), `"author":""`) {
		t.Error(`list: a version without an author must still show "author":""`)
	}

	// Pending: ready and never live, newest first.
	pending := list(t, "?pending=1")
	if len(pending) != 3 || pending[0].VersionNumber != 5 || pending[1].VersionNumber != 3 || pending[2].VersionNumber != 2 {
		t.Fatalf("pending = %+v, want v5, v3, v2", pending)
	}
	// Making v3 live turns it active: it leaves the pending list for good.
	if r := a.do(t, "PUT", "/v1/sites/"+site+"/active-version", jsonBody(map[string]any{"version_number": 3}), key); r.status != http.StatusOK {
		t.Fatalf("make v3 live: %d %s", r.status, r.body)
	}
	if r := a.do(t, "PUT", "/v1/sites/"+site+"/active-version", jsonBody(map[string]any{"version_number": 4}), key); r.status != http.StatusOK {
		t.Fatalf("make v4 live again: %d %s", r.status, r.body)
	}
	if pending = list(t, "?pending=true"); len(pending) != 2 || pending[0].VersionNumber != 5 || pending[1].VersionNumber != 2 {
		t.Fatalf("pending after v3 was live = %+v, want v5, v2", pending)
	}

	var userID, siteID string
	if err := a.database.QueryRow(`SELECT s.id, s.user_id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.username = $1 AND s.name = $2`, owner.email, site).Scan(&siteID, &userID); err != nil {
		t.Fatal(err)
	}
	discard := func(t *testing.T, path string) resp {
		t.Helper()
		return a.do(t, "DELETE", path, nil, key)
	}
	base := "/v1/sites/" + site + "/versions/"
	errCode(t, discard(t, base+"4"), http.StatusConflict, "version_live")
	errCode(t, discard(t, base+"3"), http.StatusConflict, "version_published") // live once, not now
	errCode(t, discard(t, base+"1"), http.StatusConflict, "version_published")
	errCode(t, discard(t, base+"99"), http.StatusNotFound, "version_not_found")
	errCode(t, discard(t, base+"0"), http.StatusNotFound, "version_not_found")
	errCode(t, discard(t, base+"two"), http.StatusNotFound, "version_not_found")
	errCode(t, a.do(t, "DELETE", "/v1/sites/no-such-site/versions/2", nil, key), http.StatusNotFound, "site_not_found")
	if r := a.do(t, "DELETE", base+"2", nil, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", r.status, r.body)
	}

	v2dir := a.sites.disk.VersionDir(userID, site, 2)
	if _, err := os.Stat(v2dir); err != nil {
		t.Fatalf("v2 folder before discard: %v", err)
	}
	r := discard(t, base+"2")
	if r.status != http.StatusOK {
		t.Fatalf("discard v2: %d %s", r.status, r.body)
	}
	if got := r.json(t); got["site"] != site || got["discarded_version"] != float64(2) || got["active_version"] != float64(4) {
		t.Fatalf("discard v2 body: %s", r.body)
	}
	if _, err := os.Stat(v2dir); !os.IsNotExist(err) {
		t.Fatalf("v2 folder after discard: %v", err)
	}
	if _, err := os.Stat(a.sites.disk.VersionDir(userID, site, 1)); err != nil {
		t.Fatalf("v1 folder must stay: %v", err)
	}
	// The row stays as a tombstone so the number is never handed out again.
	var status string
	if err := a.database.QueryRow(`SELECT status FROM versions WHERE site_id = $1 AND version_number = 2`, siteID).Scan(&status); err != nil || status != "discarded" {
		t.Fatalf("v2 row after discard: %q %v", status, err)
	}
	errCode(t, discard(t, base+"2"), http.StatusNotFound, "version_not_found")
	if r := a.do(t, "POST", base+"2/preview-link", nil, key); r.status < 400 {
		t.Fatalf("preview link for a discarded version: %d %s", r.status, r.body)
	}
	if r := a.do(t, "PUT", "/v1/sites/"+site+"/active-version", jsonBody(map[string]any{"version_number": 2}), key); r.status < 400 {
		t.Fatalf("rollback to a discarded version: %d %s", r.status, r.body)
	}
	if all = list(t, ""); len(all) != 4 {
		t.Fatalf("after discard: %+v", all)
	}

	// A deploy-only key is refused by the scope gate, not by the handler.
	mk := a.do(t, "POST", "/v1/me/keys", jsonBody(map[string]any{"name": "ci", "scope": "deploy"}), key)
	if mk.status != http.StatusCreated {
		t.Fatalf("mint deploy key: %d %s", mk.status, mk.body)
	}
	dk, _ := mk.json(t)["api_key"].(string)
	errCode(t, a.do(t, "DELETE", base+"5", nil, map[string]string{"X-API-Key": dk}), http.StatusForbidden, "deploy_only_key")
	if got := byNumber(list(t, "?pending=yes"), 5); got.Status != "ready" {
		t.Fatalf("v5 after refused discard: %+v", got)
	}

	// The admin mirror runs the same handler as the owner.
	r = a.do(t, "DELETE", "/v1/admin/sites/"+siteID+"/versions/5", nil, map[string]string{"X-API-Key": a.admin})
	if r.status != http.StatusOK || r.json(t)["discarded_version"] != float64(5) {
		t.Fatalf("admin discard v5: %d %s", r.status, r.body)
	}
	if r := a.do(t, "GET", "/v1/sites/"+site+"/versions?pending=1", nil, key); r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("empty pending list must be []: %d %s", r.status, r.body)
	}

	// v5 was the newest and is discarded: the next draft is v6, not v5 again.
	deploy(t, "PUT", "?publish=false", http.StatusOK)
	if got := list(t, "?pending=1"); len(got) != 1 || got[0].VersionNumber != 6 {
		t.Fatalf("after discarding the newest, the next draft = %+v, want v6", got)
	}
	// Retention drops the tombstones below its floor but reports only real
	// versions: v1 and v3 had been live; v2 and v5 were discarded.
	r = a.do(t, "PUT", "/v1/sites/"+site+"/keep-versions", jsonBody(map[string]any{"keep_versions": 1}), key)
	if r.status != http.StatusOK {
		t.Fatalf("keep-versions: %d %s", r.status, r.body)
	}
	var kept struct {
		Removed []int `json:"removed_versions"`
	}
	if err := json.Unmarshal(r.body, &kept); err != nil || len(kept.Removed) != 2 || kept.Removed[0]+kept.Removed[1] != 4 || kept.Removed[0]*kept.Removed[1] != 3 {
		t.Fatalf("removed_versions = %v (%v), want v1 and v3: %s", kept.Removed, err, r.body)
	}
	var max int
	if err := a.database.QueryRow(`SELECT max(version_number) FROM versions WHERE site_id = $1`, siteID).Scan(&max); err != nil || max != 6 {
		t.Fatalf("max version after prune = %d %v, want 6", max, err)
	}
}
