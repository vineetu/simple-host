package idle_test

import (
	"context"
	"testing"

	"simplehost/core/testkit"
	"simplehost/ops/idle"
	"simplehost/sites/serve"
)

// SPEC test 37 (F33): a site quiet for 3 months gets one email. A visit
// resets it. Still quiet, it is removed.
func TestAbandonedSites(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	ctx := context.Background()
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "old", map[string]string{"index.html": "old"})
	k.Publish(t, nina, "busy", map[string]string{"index.html": "busy"})
	quiet := func() {
		k.Exec(t, `UPDATE sites SET created_at = now() - interval '1 year', changed_at = now() - interval '4 months', last_visit_at = now() - interval '4 months' WHERE name = 'old'`)
	}
	sweep := func(mails int) {
		t.Helper()
		if err := idle.Sweep(ctx, k.App); err != nil {
			t.Fatal(err)
		}
		if got := len(k.Mail.To(nina.Email)); got != mails {
			t.Fatalf("%d emails, want %d", got, mails)
		}
	}
	visit := func(site string) int {
		return testkit.Call(t, k.Browser(), "GET", "https://"+site+"--nina.simple-host.site/", nil, "User-Agent", "Mozilla/5.0").Status
	}

	quiet()
	sweep(1) // one email, for the quiet site only
	sweep(1) // and only one
	// A visit keeps it: the clock starts again.
	visit("old")
	serve.Flush(ctx, k.App)
	sweep(1)
	quiet()
	sweep(2)
	// Still quiet 30 days after the email: it moves to the bin.
	k.Exec(t, `UPDATE sites SET idle_warned_at = now() - interval '31 days' WHERE name = 'old'`)
	sweep(2)
	if visit("old") != 404 || visit("busy") != 200 {
		t.Fatalf("after the sweep: old %d, busy %d", visit("old"), visit("busy"))
	}
}
