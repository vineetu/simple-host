package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/auth"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

// v0.7.2 correctness fixes (final/hosted-correctness.md). Needs DB_DSN.

// L5: a rollback whose commit fails leaves the served files as they were.
func TestRollbackServesOnlyWhatCommitted(t *testing.T) {
	a := newPrivateApp(t)
	ann := a.newPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.key}
	a.deploy(t, ann, "blog") // v1: <h1>blog</h1>
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, key); r.status != 200 && r.status != 201 {
		t.Fatalf("v2: %d %s", r.status, r.body)
	}
	uid, _ := a.userID(t, ann)
	siteID := a.siteID(t, ann, "blog")
	served := func() string {
		t.Helper()
		b, err := os.ReadFile(a.sites.disk.SiteDir(uid, "blog") + "/current/index.html")
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
		if _, err := a.database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	dropped := false
	drop := func() {
		if !dropped {
			dropped = true
			_, _ = a.database.Exec(`DROP TRIGGER IF EXISTS v072_fail_commit ON sites`)
			_, _ = a.database.Exec(`DROP FUNCTION IF EXISTS v072_fail_commit()`)
		}
	}
	t.Cleanup(drop)
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}, key); r.status != 500 {
		t.Fatalf("rollback with a failing commit: %d %s", r.status, r.body)
	}
	var active int
	_ = a.database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, siteID).Scan(&active)
	if active != 2 || served() != "v2" {
		t.Fatalf("after a failed commit: active_version %d, serving %q", active, served())
	}
	drop()
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}, key); r.status != 200 {
		t.Fatalf("rollback: %d %s", r.status, r.body)
	}
	if !strings.Contains(served(), "blog") {
		t.Fatalf("after rollback: %q", served())
	}
}

// Small-box trial B6: a response has the same shape whether or not the
// caller sends X-Skill-Version: arrays stay bare (the notice is a header),
// and an object keeps its fields byte for byte, big numbers included.
func TestNoticeKeepsResponseShape(t *testing.T) {
	mw := NoticeMiddleware("1.2.3")
	serve := func(body string, hdr string) *httptest.ResponseRecorder {
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		req := httptest.NewRequest("GET", "/v1/sites", nil)
		if hdr != "" {
			req.Header.Set("X-Skill-Version", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	arr := `[{"id":1},{"id":2}]`
	for _, v := range []string{"", "0.1.0", "1.2.3"} {
		rec := serve(arr, v)
		if rec.Body.String() != arr {
			t.Fatalf("skill %q: array became %s", v, rec.Body.String())
		}
		if stale := v == "0.1.0"; (rec.Header().Get("X-Skill-Notice") != "") != stale {
			t.Fatalf("skill %q: notice header %q", v, rec.Header().Get("X-Skill-Notice"))
		}
	}
	obj := `{"n":12345678901234567890,"f":1.10,"s":"x"}`
	rec := serve(obj, "0.1.0")
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("object: %v %s", err, rec.Body.String())
	}
	if string(got["n"]) != "12345678901234567890" || string(got["f"]) != "1.10" || got["_notice"] == nil {
		t.Fatalf("object changed: %s", rec.Body.String())
	}
	if rec := serve(`{}`, "0.1.0"); !strings.HasPrefix(rec.Body.String(), `{"_notice":"`) || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("empty object: %s", rec.Body.String())
	}
	for _, v := range []string{"1.2.3", ""} {
		if rec := serve(obj, v); rec.Body.String() != obj || rec.Header().Get("X-Skill-Notice") != "" {
			t.Fatalf("skill %q (current, or none: a plain API call): %s", v, rec.Body.String())
		}
	}
}

// Small-box trial B5: a server that gives sites no address of their own says
// plainly that it makes no preview links.
func TestNoPreviewOnSharedOrigin(t *testing.T) {
	a := newPrivateApp(t)
	if !a.sites.SharedOrigin() {
		t.Skip("the test app gives sites their own addresses")
	}
	ann := a.newPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.key}
	a.deploy(t, ann, "blog")
	r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/files?publish=false", map[string]any{"files": map[string]string{"index.html": "v2"}}, key)
	if r.status != 200 || r.json(t)["preview_url"] != nil || !strings.Contains(r.json(t)["note"].(string), "per-site address") {
		t.Fatalf("publish=false: %d %s", r.status, r.body)
	}
	p := a.at(t, "POST", "simple-host.test", "/v1/sites/blog/versions/2/preview-link", nil, key)
	wantCode(t, "preview link", p, 409, "preview_unavailable")
	if !strings.Contains(string(p.body), "per-site address") {
		t.Fatalf("says why: %s", p.body)
	}
}

// Small-box trial B7/B14: an install's llms.txt starts with what holds there,
// and its messages name its own contact, not the hosted support mailbox.
func TestInstanceNoteAndContact(t *testing.T) {
	prevHosts, prevNote, prevContact := instanceHosts, instanceNote, auth.SupportContact
	t.Cleanup(func() {
		instanceHosts, instanceNote = prevHosts, prevNote
		auth.SetSupportContact(prevContact, true)
	})
	SetInstanceHosts("ev.test", "sites.ev.test", "")
	auth.SetSupportContact("whoever runs this server", false)
	SetInstanceNote(InstanceFacts{SiteDomain: "ev.test", ContentHost: "sites.ev.test", SharedOrigin: true, Contact: auth.SupportContact})
	rec := httptest.NewRecorder()
	testAssetPipeline(edition.Default(config.DefaultLimits()), "").serveRewrittenAsset("llms.txt", instanceHosts, time.Now()).ServeHTTP(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	body := rec.Body.String()
	for _, want := range []string{"THIS SERVER (ev.test)", "shares that one browser origin", "Never call SH.requireSignIn()",
		"preview_unavailable", "sends no email", "ask whoever runs this server",
		"(one browser origin, shared by every site on this server)"} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt lacks %q", want)
		}
	}
	if !strings.HasPrefix(body, "THIS SERVER") {
		t.Errorf("the note is not first: %.80s", body)
	}
	if got := exportReadme(); strings.Contains(got, "support@simple-host.app") || !strings.Contains(got, "whoever runs this server") {
		t.Errorf("export README names the hosted mailbox")
	}
	if msg := auth.InvalidKeyMessage(); strings.Contains(msg, "/v1/auth") {
		t.Errorf("invalid key message sends to email sign-in: %s", msg)
	}
	// On simple-host.app itself: no note.
	SetInstanceHosts("simple-host.app", "", "")
	SetInstanceNote(InstanceFacts{SiteDomain: "simple-host.app"})
	if instanceNote != "" {
		t.Errorf("a note on the hosted service")
	}
}

func wantCode(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: %d %s, want %d %s", what, r.status, r.body, status, code)
	}
	if code != "" {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(r.body, &body)
		if body.Code != code {
			t.Fatalf("%s: code %q (%s), want %q", what, body.Code, r.body, code)
		}
	}
}
