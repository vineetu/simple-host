package servertest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// v0.7.2 correctness fixes (final/hosted-correctness.md). Needs DB_DSN.

// L5: a rollback whose commit fails leaves the served files as they were.
func TestRollbackServesOnlyWhatCommitted(t *testing.T) {
	a := NewPrivateApp(t)
	ann := a.NewPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.Key}
	a.Deploy(t, ann, "blog") // v1: <h1>blog</h1>
	if r := a.At(t, "PUT", "simple-host.test", "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, key); r.Status != 200 && r.Status != 201 {
		t.Fatalf("v2: %d %s", r.Status, r.Body)
	}
	uid, _ := a.UserID(t, ann)
	siteID := a.SiteID(t, ann, "blog")
	served := func() string {
		t.Helper()
		b, err := os.ReadFile(a.Sites.Disk.SiteDir(uid, "blog") + "/current/index.html")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if served() != "v2" {
		t.Fatalf("before: %q", served())
	}
	// The commit fails: a deferred check on this site's row.
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION v072_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit refused'; END $$`,
		`DROP TRIGGER IF EXISTS v072_fail_commit ON sites`,
		`CREATE CONSTRAINT TRIGGER v072_fail_commit AFTER UPDATE ON sites DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id = '` + siteID + `') EXECUTE FUNCTION v072_fail_commit()`,
	} {
		if _, err := a.Database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	dropped := false
	drop := func() {
		if !dropped {
			dropped = true
			_, _ = a.Database.Exec(`DROP TRIGGER IF EXISTS v072_fail_commit ON sites`)
			_, _ = a.Database.Exec(`DROP FUNCTION IF EXISTS v072_fail_commit()`)
		}
	}
	t.Cleanup(drop)
	if r := a.At(t, "PUT", "simple-host.test", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}, key); r.Status != 500 {
		t.Fatalf("rollback with a failing commit: %d %s", r.Status, r.Body)
	}
	var active int
	_ = a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, siteID).Scan(&active)
	if active != 2 || served() != "v2" {
		t.Fatalf("after a failed commit: active_version %d, serving %q", active, served())
	}
	drop()
	if r := a.At(t, "PUT", "simple-host.test", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}, key); r.Status != 200 {
		t.Fatalf("rollback: %d %s", r.Status, r.Body)
	}
	if !strings.Contains(served(), "blog") {
		t.Fatalf("after rollback: %q", served())
	}
}

// Small-box trial B5: a server that gives sites no address of their own says
// plainly that it makes no preview links.
func TestNoPreviewOnSharedOrigin(t *testing.T) {
	a := NewPrivateApp(t)
	if !a.Sites.SharedOrigin() {
		t.Skip("the test app gives sites their own addresses")
	}
	ann := a.NewPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.Key}
	a.Deploy(t, ann, "blog")
	r := a.At(t, "PUT", "simple-host.test", "/v1/sites/blog/files?publish=false", map[string]any{"files": map[string]string{"index.html": "v2"}}, key)
	if r.Status != 200 || r.JSON(t)["preview_url"] != nil || !strings.Contains(r.JSON(t)["note"].(string), "per-site address") {
		t.Fatalf("publish=false: %d %s", r.Status, r.Body)
	}
	p := a.At(t, "POST", "simple-host.test", "/v1/sites/blog/versions/2/preview-link", nil, key)
	wantCode(t, "preview link", p, 409, "preview_unavailable")
	if !strings.Contains(string(p.Body), "per-site address") {
		t.Fatalf("says why: %s", p.Body)
	}
}

func wantCode(t *testing.T, what string, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: %d %s, want %d %s", what, r.Status, r.Body, status, code)
	}
	if code != "" {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(r.Body, &body)
		if body.Code != code {
			t.Fatalf("%s: code %q (%s), want %q", what, body.Code, r.Body, code)
		}
	}
}
