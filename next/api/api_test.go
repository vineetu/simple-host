package api_test

import (
	"strings"
	"testing"

	"simplehost/api"
	"simplehost/core/testkit"
	"simplehost/sites/serve"
)

// SPEC test 3 (F2): change handle. Sites answer at the new address. The old
// handle answers "not found" and cannot be taken.
func TestChangeHandle(t *testing.T) {
	k := testkit.Start(t, api.Mount, serve.Mount)
	nina, olga := k.Person(t, "nina"), k.Person(t, "olga")
	k.Exec(t, `INSERT INTO owner_addresses (owner_id, app_shape) VALUES ($1, true)`, olga.ID) // an existing account
	k.Publish(t, nina, "blog", map[string]string{"index.html": "nina's blog"})
	k.Publish(t, olga, "shop", map[string]string{"index.html": "olga's shop"})
	b := k.Browser()
	patch := func(who testkit.Who, owner string, body any) testkit.Res {
		return testkit.Call(t, b, "PATCH", "https://simple-host.app/v1/owners/"+owner, body, "X-API-Key", who.Key)
	}
	get := func(url string) testkit.Res { return testkit.Call(t, b, "GET", url, nil) }

	if r := patch(nina, "me", map[string]string{"handle": "nora", "display_name": "Nora"}); r.Status != 200 || !strings.Contains(r.Body, `"handle":"nora"`) {
		t.Fatalf("change handle: %d %s", r.Status, r.Body)
	}
	if r := get("https://blog--nora.simple-host.site/"); r.Status != 200 || r.Body != "nina's blog" {
		t.Fatalf("new address: %d %s", r.Status, r.Body)
	}
	for _, url := range []string{"https://blog--nina.simple-host.site/", "https://nina.simple-host.site/"} {
		if r := get(url); r.Status != 404 {
			t.Fatalf("old address %s: %d, want 404", url, r.Status)
		}
	}
	// The old handle is retired for good.
	if r := patch(olga, "olga", map[string]string{"handle": "nina"}); r.Status != 409 || !strings.Contains(r.Body, "name_taken") {
		t.Fatalf("taking the old handle: %d %s", r.Status, r.Body)
	}
	// An existing account moves to the flat address under its new handle.
	if r := patch(olga, "olga", map[string]string{"handle": "olive"}); r.Status != 200 {
		t.Fatalf("existing account: %d %s", r.Status, r.Body)
	}
	if r := get("https://shop--olive.simple-host.site/"); r.Status != 200 || get("https://shop.olga.simple-host.app/").Status != 404 {
		t.Fatalf("existing account's new address: %d", r.Status)
	}
	// Only the person (or the team's members) may change it.
	if r := patch(olga, "nora", map[string]string{"display_name": "x"}); r.Status != 403 {
		t.Fatalf("someone else's handle: %d %s", r.Status, r.Body)
	}
}
