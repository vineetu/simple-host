package holds_test

import (
	"strings"
	"testing"

	"simplehost/api"
	"simplehost/core/testkit"
	"simplehost/ops/holds"
	"simplehost/sites/serve"
)

// SPEC test 35 (F29/F30): the operator takes a site down (every address says
// "taken down"), then restores it. Nobody else can.
func TestTakeDownAndRestore(t *testing.T) {
	k := testkit.Start(t, api.Mount, holds.Mount, serve.Mount)
	nina, op := k.Person(t, "nina"), k.Person(t, "op")
	k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, op.ID)
	k.Publish(t, nina, "blog", map[string]string{"index.html": "hello"})
	b := k.Browser()
	patch := func(who testkit.Who, body any) testkit.Res {
		return testkit.Call(t, b, "PATCH", "https://simple-host.app/v1/sites/blog?owner=nina", body, "X-API-Key", who.Key)
	}
	if r := patch(nina, map[string]any{"short_name": "ninablog"}); r.Status != 200 {
		t.Fatalf("short name: %d %s", r.Status, r.Body)
	}
	// Over HTTPS the main address sends pages to the short name; plain HTTP shows it serving too.
	addresses := []string{"http://blog--nina.simple-host.site/", "https://ninablog.simple-host.app/"}
	every := func(status int, has string) {
		t.Helper()
		for _, url := range addresses {
			if r := testkit.Call(t, b, "GET", url, nil); r.Status != status || !strings.Contains(r.Body, has) {
				t.Fatalf("%s: %d %q, want %d with %q", url, r.Status, r.Body, status, has)
			}
		}
	}

	if r := patch(nina, map[string]any{"taken_down": true, "reason": "mine"}); r.Status != 403 || !strings.Contains(r.Body, "operator_only") {
		t.Fatalf("the owner taking it down: %d %s", r.Status, r.Body)
	}
	every(200, "hello")
	if r := patch(op, map[string]any{"taken_down": true, "reason": "Phishing page."}); r.Status != 200 || !strings.Contains(r.Body, `"taken_down":true`) {
		t.Fatalf("take down: %d %s", r.Status, r.Body)
	}
	every(410, "taken down")
	if r := patch(op, map[string]any{"taken_down": false}); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	every(200, "hello")
}
