package registration

import (
	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	productconfig "github.com/vsriram/simple-host/products/hack/config"

	"testing"
)

// Apply to a hackathon without a join link (zp-hack-applications.sql, owner
// 2026-10-31): build-plan.md §8's Go handler and DB tests.

// TestHackApplyTakedownRaceForcedOrdering is pre-ship review item 2: a
// forced-ordering test for the takedown race, the same shape as the
// switch-off one above. Hold the event row FOR UPDATE from a second
// connection, start the apply so its own row lock waits on it, commit a
// takedown (the same taken_down_at update db.hack.go's takedown path
// issues) while the apply is still waiting, then release: the apply that
// proceeds after that commit must see the event as taken down -- 404
// event_not_found, nothing inserted, never a 200. Removing the
// `|| takenDownNow` half of postApply's `if stage == "draft" ||
// takenDownNow` mutation check (hack_apply.go) makes this test fail; it was
// verified to fail that way and then restored.
func TestHackApplyTakedownRaceForcedOrdering(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "atdorg")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	applicant := a.NewPerson(t, "atdapplicant")

	lockTx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE slug=$1 FOR UPDATE`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	applyDone := make(chan hacktest.Resp, 1)
	go func() { applyDone <- a.Apply(t, slug, applicant, nil) }()
	hacktest.WaitBlocked() // the apply's own row lock now waits on lockTx's FOR UPDATE.

	if _, err := lockTx.Exec(`UPDATE events SET taken_down_at = COALESCE(taken_down_at, now()), taken_down_reason = $2, updated_at = now() WHERE id = $1`, eventID, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	applyResp := <-applyDone
	hacktest.WantTS(t, "apply proceeding after takedown commit", applyResp, 404, "event_not_found")
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, a.UserID(t, applicant)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("apply inserted a row despite the takedown that committed first: %d rows", n)
	}
}

// TestHackApplyLimits is §8.13: the per-account rate limit, and the
// per-account pending cap (freed once one application is decided).
func TestHackApplyLimits(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "allimorg")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)

	// Default RATE_LIMIT_EVENT_APPLY_USER is burst 5: a 6th apply (even an
	// idempotent repeat, since the limiter runs before anything else) is 429.
	limited := a.NewPerson(t, "allimuser")
	for i := 0; i < 5; i++ {
		if r := a.Apply(t, slug, limited, nil); r.Status != 200 {
			t.Fatalf("apply %d: %d %s", i, r.Status, r.Body)
		}
	}
	hacktest.WantTS(t, "6th apply", a.Apply(t, slug, limited, nil), 429, "rate_limited")

	// The pending cap (20) is a business rule independent of the rate
	// limiter: raise both knobs generously on a fresh app so the cap, not
	// the limiter or the per-organiser event cap, is what trips.
	l := productconfig.DefaultLimits()
	l.RateEventApplyUser.Burst = 100
	l.RateEventApplyIP.Burst = 100
	l.EventMaxActivePerOrganiser = 25
	l.EventCreatePerDay = 25
	b := newHackAppWithLimits(t, l)
	capOrg := b.NewPerson(t, "allimcaporg")
	capPerson := b.NewPerson(t, "allimcapperson")
	var slugs []string
	for i := 0; i < 21; i++ {
		s := hacktest.UniqueSlug()
		if r := b.CreateEvent(t, capOrg, s, nil); r.Status != 201 {
			t.Fatalf("create %d: %d %s", i, r.Status, r.Body)
		}
		b.OpenEvent(t, capOrg, s)
		b.ListEvent(t, s)
		slugs = append(slugs, s)
	}
	for i, s := range slugs[:20] {
		if r := b.Apply(t, s, capPerson, nil); r.Status != 200 {
			t.Fatalf("apply %d to %s: %d %s", i, s, r.Status, r.Body)
		}
	}
	hacktest.WantTS(t, "21st pending", b.Apply(t, slugs[20], capPerson, nil), 409, "too_many_pending")

	// Decide the first one; applying to the 21st then works.
	hacktest.WantTS(t, "decide one", b.At(t, "POST", "/v1/hack/events/"+slugs[0]+"/applications/"+b.UserID(t, capPerson)+"/decision", map[string]string{"decision": "approved"}, b.Key(capOrg)), 200, "")
	hacktest.WantTS(t, "21st after decision", b.Apply(t, slugs[20], capPerson, nil), 200, "")
}
