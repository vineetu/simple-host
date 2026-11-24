package rules_test

import (
	"context"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
)

type fixture struct {
	k     *testkit.Kit
	bob   testkit.Who
	addr  string
	owner func(method, path string, body any) testkit.Res
}

func setup(t *testing.T) fixture {
	k := testkit.Start(t, rows.Mount, datasql.Mount, datarules.Mount)
	f := fixture{k: k, bob: k.Person(t, "bob")}
	k.Publish(t, f.bob, "wall", map[string]string{"index.html": "wall"})
	s, _ := k.App.Sites.Get(context.Background(), f.bob.ID, "wall")
	f.addr = k.App.Sites.URL(s)
	f.owner = func(method, path string, body any) testkit.Res {
		return testkit.Call(t, k.Browser(), method, "https://simple-host.app/v1/sites/wall/storage/"+path, body, "Authorization", "Bearer "+f.bob.Key)
	}
	if r := f.owner("POST", "sqlite/posts/execute", map[string]any{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, text TEXT, visitor_id TEXT)"}); r.Status != 200 {
		t.Fatal(r.Body)
	}
	return f
}

func want(t *testing.T, r testkit.Res, status int, has string) {
	t.Helper()
	if r.Status != status || !strings.Contains(r.Body, has) {
		t.Fatalf("got %d %s, want %d with %q", r.Status, r.Body, status, has)
	}
}

// SPEC test 22 (F16): with no rules, a visitor is refused. With "signed-in
// may add", a signed-in visitor adds.
func TestRulesDecideWhoAdds(t *testing.T) {
	f := setup(t)
	ann := f.k.Person(t, "ann")
	page := f.k.Browser()
	f.k.SignIn(t, page, ann, f.addr)
	rows := f.addr + "v1/sites/wall/storage/sqlite/posts/tables/posts/rows"
	post := map[string]any{"text": "hi"}
	want(t, testkit.Call(t, page, "POST", rows, post, "X-SH-CSRF", "1"), 403, "not_allowed")
	want(t, testkit.Call(t, f.k.Browser(), "POST", rows, post, "X-SH-CSRF", "1"), 401, "sign_in_required")

	// A mistake is reported exactly when saving, and nothing changes.
	want(t, f.owner("PUT", "posts/rules", `{"rules": {"posts": {"policies": [{"name": "add", "command": "add", "check": "colour == 1"}]}}}`), 400, "colour")
	want(t, f.owner("PUT", "posts/rules", `{"rules": {"posts": {"policies": [{"name": "add", "command": "add", "check": "auth.signed_in"}]}}}`), 200, "posts")
	want(t, testkit.Call(t, page, "POST", rows, post, "X-SH-CSRF", "1"), 200, "last_insert_id")
	want(t, testkit.Call(t, f.k.Browser(), "POST", rows, post, "X-SH-CSRF", "1"), 401, "sign_in_required")
}

// SPEC test 23 (F16): an anonymous post passes only with a good spam check.
// A suspended person is refused.
func TestSpamCheckAndSuspension(t *testing.T) {
	f := setup(t)
	want(t, f.owner("PUT", "posts/rules", `{"spam_check": true, "rules": {"posts": {"policies": [
		{"name": "post", "command": "add", "check": "auth.checked"}, {"name": "members", "command": "add", "check": "auth.signed_in"}]}}}`), 200, `"spam_check":true`)
	rows := f.addr + "v1/sites/wall/storage/sqlite/posts/tables/posts/rows"
	post := map[string]any{"text": "hi"}
	anon := f.k.Browser()
	want(t, testkit.Call(t, anon, "POST", rows, post), 403, "spam_check_required")
	want(t, testkit.Call(t, anon, "POST", rows, post, "X-SH-Turnstile", "wrong"), 403, "spam_check_failed")
	want(t, testkit.Call(t, anon, "POST", rows, post, "X-SH-Turnstile", f.k.Turnstile), 200, "last_insert_id")

	ann := f.k.Person(t, "ann")
	page := f.k.Browser()
	f.k.SignIn(t, page, ann, f.addr)
	want(t, testkit.Call(t, page, "POST", rows, post, "X-SH-CSRF", "1"), 200, "last_insert_id")
	f.k.Exec(t, `UPDATE people SET suspended_at = now() WHERE id = $1`, ann.ID)
	if r := testkit.Call(t, page, "POST", rows, post, "X-SH-CSRF", "1"); r.Status < 400 {
		t.Fatalf("suspended person added: %d %s", r.Status, r.Body)
	}
}
