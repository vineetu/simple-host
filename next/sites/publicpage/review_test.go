package publicpage_test

import (
	"context"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/locks"
	"simplehost/sites/publicpage"
	"simplehost/sites/serve"
)

// Review A8: a passcode-locked home site unlocks at the public page's address.
func TestHomeSiteUnlocksOnThePublicPage(t *testing.T) {
	k := testkit.Start(t, serve.Mount, locks.Mount, publicpage.Mount)
	v := k.Person(t, "victim")
	k.Publish(t, v, "blog", map[string]string{"index.html": "original"})
	s, _ := k.App.Sites.Get(context.Background(), v.ID, "blog")
	k.Exec(t, `INSERT INTO owner_addresses (owner_id, home_site_id) VALUES ($1, $2)`, v.ID, s.ID)
	if err := locks.SetPasscode(context.Background(), k.App, s, "secret123"); err != nil {
		t.Fatal(err)
	}
	b := k.Browser()
	if r := testkit.Call(t, b, "GET", "https://victim.simple-host.site/", nil); r.Status != 401 {
		t.Fatalf("home before: %d", r.Status)
	}
	if r := testkit.Call(t, b, "POST", "https://victim.simple-host.site/_sh/passcode", "passcode=secret123&next=/",
		"Content-Type", "application/x-www-form-urlencoded"); r.Status != 303 {
		t.Fatalf("unlock: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", "https://victim.simple-host.site/", nil); r.Status != 200 || r.Body != "original" {
		t.Fatalf("home after: %d %q", r.Status, r.Body)
	}
}
