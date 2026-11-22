package servertest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/lifecycle"

	accountapp "github.com/vsriram/simple-host/host/access"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	"github.com/vsriram/simple-host/products/hack/hacktest"
)

func TestHackArchiveCleanup(t *testing.T) {
	a := newFeatApp(t)
	l := productconfig.DefaultLimits()
	l.EventCreatePerDay = 40
	l.EventMaxActivePerOrganiser = 40
	hacktest.SetLimits(a.App, l)

	org := a.NewPerson(t, "cln")
	now := time.Date(2026, 6, 15, 15, 4, 5, 0, time.UTC)
	keep := a.hack.Limits.EventSitesKeep
	warnFor := a.hack.Limits.EventRemovalWarn
	pastRemove := now.Add(-keep - 36*time.Hour)
	inWarn := now.Add(-keep + warnFor/2)

	removalSlug, removalTeams := a.makeCleanupEvent(t, org, "Removal Hack", "removal@example.com", 2)
	warnSlug, _ := a.makeCleanupEvent(t, org, "Harbour Hack", "harbour@example.com", 1)
	keepSlug, _ := a.makeCleanupEvent(t, org, "Keep Hack", "keep@example.com", 1)
	var live []string
	for _, stage := range []string{"open", "building", "closed", "judging", "results"} {
		slug, _ := a.makeCleanupEvent(t, org, "Stage "+stage, "stage-"+stage+"@example.com", 1)
		a.setEventClock(t, slug, stage, pastRemove, false)
		live = append(live, slug)
	}
	a.setEventClock(t, removalSlug, "archived", pastRemove, false)
	a.setEventClock(t, warnSlug, "archived", inWarn, false)
	a.setEventClock(t, keepSlug, "archived", pastRemove, true)

	// The organiser's own rubric and a published snapshot must survive removal.
	custom := hacktest.DecodeRubric(t, a.At(t, "PUT", "/v1/hack/events/"+removalSlug+"/rubric",
		hacktest.RubricBody(hacktest.Criterion("Impact", "Did it matter?", 60, 5), hacktest.Criterion("Craft", "Is it well made?", 40, 10)), a.Key(org)))
	if len(custom.Criteria) != 2 {
		t.Fatalf("custom rubric: %+v", custom.Criteria)
	}
	if _, err := a.DB.Exec(`
		INSERT INTO event_results (event_id, full_ranking, snapshot)
		SELECT id, false, '{"place":1,"note":"keep me"}'::jsonb FROM events WHERE slug = $1`, removalSlug); err != nil {
		t.Fatal(err)
	}
	// An archived event with no rubric rows must not gain any from the sweep.
	if _, err := a.DB.Exec(`DELETE FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, warnSlug); err != nil {
		t.Fatal(err)
	}

	rest := map[string]string{}
	for _, slug := range append([]string{removalSlug, warnSlug, keepSlug}, live...) {
		rest[slug] = a.eventRest(t, slug)
	}
	resultsBefore := a.resultsBlob(t, removalSlug)
	rubricBefore := a.rubricBlob(t, removalSlug)

	sites := &cleanupSites{fail: map[string]error{removalTeams[0]: errors.New("disk full")}}
	a.hack.SetSites(sites)
	ctx := context.Background()

	// No mailer: the whole pass, including removal, does nothing.
	a.runEventCleanup(ctx, now)
	if calls := sites.called(); len(calls) != 0 {
		t.Fatalf("trashed with no mailer: %+v", calls)
	}
	if warned, removed := a.eventStamps(t, removalSlug); warned.Valid || removed.Valid {
		t.Fatalf("stamped with no mailer: warned %v removed %v", warned, removed)
	}

	// A mailer that cannot send still removes (removal sends no email). A failed
	// warning is not recorded.
	a.hack.SetMailer(&hacktest.FailingReplyMailer{})
	a.runEventCleanup(ctx, now)
	account := a.EventAccount(t, removalSlug)
	calls := sites.called()
	if len(calls) != 2 || calls[0].account != account || calls[0].slug != removalTeams[0] || calls[1].account != account || calls[1].slug != removalTeams[1] {
		t.Fatalf("trash calls: %+v want %s / %v", calls, account, removalTeams)
	}
	warned, removed := a.eventStamps(t, removalSlug)
	if warned.Valid || !removed.Valid || !removed.Time.Equal(now) {
		t.Fatalf("removal stamps: warned %v removed %v want removed %v", warned, removed, now)
	}
	if a.eventRest(t, removalSlug) != rest[removalSlug] {
		t.Fatalf("removal changed the event row\n got %s\nwant %s", a.eventRest(t, removalSlug), rest[removalSlug])
	}
	if a.resultsBlob(t, removalSlug) != resultsBefore {
		t.Fatalf("event_results changed: %s want %s", a.resultsBlob(t, removalSlug), resultsBefore)
	}
	if a.rubricBlob(t, removalSlug) != rubricBefore {
		t.Fatalf("rubric changed:\n got %s\nwant %s", a.rubricBlob(t, removalSlug), rubricBefore)
	}
	if w, r := a.eventStamps(t, warnSlug); w.Valid || r.Valid {
		t.Fatalf("failed warning was recorded: warned %v removed %v", w, r)
	}
	if n := a.CountQuery(t, `SELECT COUNT(*) FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, warnSlug); n != 0 {
		t.Fatalf("sweep inserted rubric rows: %d", n)
	}
	a.wantUntouched(t, keepSlug, rest[keepSlug])
	for _, slug := range live {
		a.wantUntouched(t, slug, rest[slug])
	}

	mail := &hacktest.ReplyMailer{}
	warnCtx, cancelWarn := context.WithCancel(ctx)
	defer cancelWarn()
	a.hack.SetMailer(&cancelCleanupMailer{mail, cancelWarn})
	a.runEventCleanup(warnCtx, now)
	if warnCtx.Err() == nil {
		t.Fatal("context-aware cleanup sender was not called")
	}
	a.hack.SetMailer(mail)
	if more := sites.called(); len(more) != 2 {
		t.Fatalf("trashed again on the warning pass: %+v", more)
	}
	warned, removed = a.eventStamps(t, warnSlug)
	if !warned.Valid || !warned.Time.Equal(now) || removed.Valid {
		t.Fatalf("warn stamps: warned %v removed %v", warned, removed)
	}
	if a.eventRest(t, warnSlug) != rest[warnSlug] {
		t.Fatalf("warning changed the event row\n got %s\nwant %s", a.eventRest(t, warnSlug), rest[warnSlug])
	}
	a.wantUntouched(t, keepSlug, rest[keepSlug])
	for _, slug := range live {
		a.wantUntouched(t, slug, rest[slug])
	}
	if w, r := a.eventStamps(t, removalSlug); w.Valid || !r.Time.Equal(now) {
		t.Fatalf("removal event changed on the warning pass: warned %v removed %v", w, r)
	}

	var closed time.Time
	if err := a.DB.QueryRow(`SELECT closed_at FROM events WHERE slug = $1`, warnSlug).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	removeOn := closed.UTC().Add(keep).Format("2 January 2006")
	subject := "Harbour Hack: team sites will be removed on " + removeOn
	text := fmt.Sprintf(`Harbour Hack was archived. Its team sites will be removed on %s.

The event page and the results are not affected.

If you need more time, write to support@simple-host.app.

Simple Hack
`, removeOn)
	wantMail := "harbour@example.com|" + a.hack.Limits.IdleReplyTo + "|" + subject + "|" + text
	msgs := mail.Messages()
	if len(msgs) != 1 || msgs[0] != wantMail {
		t.Fatalf("warning email:\n got %q\nwant %q", msgs, wantMail)
	}

	// A second pass does not send the warning again, and does not remove the sites yet.
	a.runEventCleanup(ctx, now)
	if again := mail.Messages(); len(again) != 1 {
		t.Fatalf("warning sent again: %d", len(again))
	}
	if _, removed := a.eventStamps(t, warnSlug); removed.Valid {
		t.Fatal("warned event was removed")
	}
	if more := sites.called(); len(more) != 2 {
		t.Fatalf("trash calls after the second pass: %+v", more)
	}
}

func TestHackRoleIsolation(t *testing.T) {
	a := newFeatApp(t)
	orgA, orgB := a.NewPerson(t, "oa"), a.NewPerson(t, "ob")
	part := a.NewPerson(t, "pa")
	stranger := a.NewPerson(t, "st")
	slugA, slugB := hacktest.UniqueSlug(), hacktest.UniqueSlug()
	if r := a.CreateEvent(t, orgA, slugA, nil); r.Status != 201 {
		t.Fatalf("A: %d %s", r.Status, r.Body)
	}
	if r := a.CreateEvent(t, orgB, slugB, nil); r.Status != 201 {
		t.Fatalf("B: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, orgA, slugA)
	join := a.At(t, "GET", "/v1/hack/events/"+slugA, nil, a.Key(orgA)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	jr := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.Key(part))
	if jr.Status != 200 {
		t.Fatalf("join: %d %s", jr.Status, jr.Body)
	}

	eventRoutes := []struct{ method, path string }{
		{"GET", "/v1/hack/events/" + slugA},
		{"PATCH", "/v1/hack/events/" + slugA},
		{"POST", "/v1/hack/events/" + slugA + "/stage"},
		{"POST", "/v1/hack/events/" + slugA + "/codes/join"},
		{"DELETE", "/v1/hack/events/" + slugA},
		{"GET", "/v1/hack/events/" + slugA + "/people"},
		{"DELETE", "/v1/hack/events/" + slugA + "/people/" + a.UserID(t, part)},
		{"GET", "/v1/hack/events/" + slugA + "/teams"},
		{"POST", "/v1/hack/events/" + slugA + "/teams"},
		{"POST", "/v1/hack/events/" + slugA + "/teams/join"},
		{"POST", "/v1/hack/events/" + slugA + "/teams/leave"},
		{"POST", "/v1/hack/events/" + slugA + "/teams/nope/members"},
		{"DELETE", "/v1/hack/events/" + slugA + "/teams/nope/members/" + a.UserID(t, part)},
		{"DELETE", "/v1/hack/events/" + slugA + "/teams/nope"},
	}
	bodies := map[string]any{
		"PATCH /v1/hack/events/" + slugA:                        map[string]string{"tagline": "x"},
		"POST /v1/hack/events/" + slugA + "/stage":              map[string]string{"stage": "building"},
		"POST /v1/hack/events/" + slugA + "/teams":              map[string]string{"name": "T"},
		"POST /v1/hack/events/" + slugA + "/teams/join":         map[string]string{"code": "abcdef"},
		"POST /v1/hack/events/" + slugA + "/teams/nope/members": map[string]string{"user_id": a.UserID(t, part)},
	}
	for _, rt := range eventRoutes {
		body := bodies[rt.method+" "+rt.path]
		r := a.At(t, rt.method, rt.path, body, a.Key(stranger))
		if r.Status != 404 || r.JSON(t)["code"] != "event_not_found" {
			t.Errorf("stranger %s %s: %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	orgRoutes := []struct{ method, path string }{
		{"PATCH", "/v1/hack/events/" + slugA},
		{"POST", "/v1/hack/events/" + slugA + "/stage"},
		{"POST", "/v1/hack/events/" + slugA + "/codes/join"},
		{"DELETE", "/v1/hack/events/" + slugA},
		{"GET", "/v1/hack/events/" + slugA + "/people"},
		{"GET", "/v1/hack/events/" + slugA + "/teams"},
	}
	for _, rt := range orgRoutes {
		body := bodies[rt.method+" "+rt.path]
		r := a.At(t, rt.method, rt.path, body, a.Key(part))
		if r.Status != 404 || r.JSON(t)["code"] != "event_not_found" {
			t.Errorf("participant %s %s: %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
	r := a.At(t, "GET", "/v1/hack/events/"+slugB, nil, a.Key(orgA))
	if r.Status != 404 || r.JSON(t)["code"] != "event_not_found" {
		t.Fatalf("cross-event: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", "/v1/hack/events/"+slugA+"/people", nil, a.Key(orgB))
	if r.Status != 404 {
		t.Fatalf("orgB people A: %d %s", r.Status, r.Body)
	}
}

func TestHackAdmin(t *testing.T) {
	a := newFeatApp(t)
	l := productconfig.DefaultLimits()
	l.EventCreatePerDay = 20
	l.EventMaxActivePerOrganiser = 20
	hacktest.SetLimits(a.App, l)
	org := a.NewPerson(t, "ad")
	part := a.NewPerson(t, "ap")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "P"}, a.Key(part)); r.Status != 200 {
		t.Fatalf("join: %d %s", r.Status, r.Body)
	}

	if r := a.At(t, "GET", "/v1/admin/hack/events", nil, a.Key(org)); r.Status != 404 {
		t.Fatalf("non-admin list: %d %s", r.Status, r.Body)
	}
	list := a.At(t, "GET", "/v1/admin/hack/events", nil, a.AdminH())
	if list.Status != 200 {
		t.Fatalf("admin list: %d %s", list.Status, list.Body)
	}
	found := false
	var listed struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(list.Body, &listed); err != nil {
		t.Fatalf("admin list shape: %v %s", err, list.Body)
	}
	for _, ev := range listed.Events {
		if ev["slug"] == slug {
			if ev["participants"] != float64(1) {
				t.Fatalf("flat counts: %v", ev)
			}
			found = true
			if ev["organiser_name"] != "Ada Lovelace" || ev["contact_email"] != "ada@example.com" {
				t.Fatalf("organiser details: %v", ev)
			}
			if ev["created_by_email"] != org.Email {
				t.Fatalf("creator: %v", ev["created_by_email"])
			}
			counts := ev["counts"].(map[string]any)
			if counts["participants"] != float64(1) {
				t.Fatalf("counts: %v", counts)
			}
		}
	}
	if !found {
		t.Fatal("event missing from admin list")
	}

	r := a.At(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "spam"}, a.AdminH())
	if r.Status != 200 || r.JSON(t)["taken_down"] != true {
		t.Fatalf("takedown: %d %s", r.Status, r.Body)
	}
	var suspended bool
	if err := a.DB.QueryRow(`
		SELECT u.suspended_at IS NOT NULL FROM events e JOIN users u ON u.id = e.account_id WHERE e.slug = $1`, slug).Scan(&suspended); err != nil || !suspended {
		t.Fatalf("holding not suspended: %v %v", suspended, err)
	}
	r = a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"tagline": "nope"}, a.Key(org))
	if r.Status != 403 || r.JSON(t)["code"] != "event_taken_down" {
		t.Fatalf("write: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "X"}, a.Key(part))
	if r.Status != 403 || r.JSON(t)["code"] != "event_taken_down" {
		t.Fatalf("part write: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", "/v1/hack/join/"+join, nil, nil)
	if r.Status != 403 || r.JSON(t)["code"] != "event_taken_down" {
		t.Fatalf("join get: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "POST", "/v1/admin/hack/events/"+slug+"/restore", nil, a.AdminH())
	if r.Status != 200 || r.JSON(t)["taken_down"] != false {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if err := a.DB.QueryRow(`
		SELECT u.suspended_at IS NOT NULL FROM events e JOIN users u ON u.id = e.account_id WHERE e.slug = $1`, slug).Scan(&suspended); err != nil || suspended {
		t.Fatalf("holding still suspended: %v %v", suspended, err)
	}
	r = a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"tagline": "back"}, a.Key(org))
	if r.Status != 200 {
		t.Fatalf("write after restore: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "DELETE", "/v1/admin/hack/events/"+slug, nil, a.AdminH())
	if r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	r = a.CreateEvent(t, org, slug, nil)
	if r.Status != 409 || r.JSON(t)["code"] != "name_taken" {
		t.Fatalf("joined slug not reserved: %d %s", r.Status, r.Body)
	}

	draft := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, draft, nil); r.Status != 201 {
		t.Fatalf("draft: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "DELETE", "/v1/hack/events/"+draft, nil, a.Key(org))
	if r.Status != http.StatusNoContent {
		t.Fatalf("org delete draft: %d %s", r.Status, r.Body)
	}
	r = a.CreateEvent(t, org, draft, nil)
	if r.Status != 201 {
		t.Fatalf("draft slug not freed: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, draft)
	// Open but nobody joined: still deletable, and cannot be ended.
	if r := a.At(t, "POST", "/v1/hack/events/"+draft+"/stage", map[string]string{"stage": "archived"}, a.Key(org)); r.Status != 409 || r.JSON(t)["code"] != "archive_needs_participants" {
		t.Fatalf("end empty: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", "/v1/hack/events/"+draft, nil, a.Key(org)); r.Status != http.StatusNoContent {
		t.Fatalf("delete open empty: %d %s", r.Status, r.Body)
	}
	if r := a.CreateEvent(t, org, draft, nil); r.Status != 201 {
		t.Fatalf("recreate: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, draft)
	jc := a.At(t, "GET", "/v1/hack/events/"+draft, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P"}, a.Key(a.NewPerson(t, "dp"))); r.Status != 200 {
		t.Fatalf("join: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "DELETE", "/v1/hack/events/"+draft, nil, a.Key(org))
	if r.Status != 409 || r.JSON(t)["code"] != "delete_only_empty" {
		t.Fatalf("delete with people: %d %s", r.Status, r.Body)
	}

	a.At(t, "POST", "/v1/hack/events/"+draft+"/stage", map[string]string{"stage": "archived"}, a.Key(org))
	r = a.At(t, "POST", "/v1/hack/events/"+draft+"/stage", map[string]string{"stage": "open"}, a.Key(org))
	if r.Status != 409 || r.JSON(t)["code"] != "event_closed" {
		t.Fatalf("leave archived: %d %s", r.Status, r.Body)
	}
}

func TestHackReviewFixes(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "rforg")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	a.OpenEvent(t, org, slug)
	jc := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	join := func(label string) hacktest.Person {
		p := a.NewPerson(t, label)
		if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": label}, a.Key(p)); r.Status != 200 {
			t.Fatalf("join %s: %d %s", label, r.Status, r.Body)
		}
		return p
	}
	p1, p2, p3 := join("rfa"), join("rfb"), join("rfc")

	// A participant's team answer carries no emails or account ids.
	r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Owls"}, a.Key(p1))
	if r.Status != 201 || strings.Contains(string(r.Body), "@") || strings.Contains(string(r.Body), "user_id") {
		t.Fatalf("create team leaks: %d %s", r.Status, r.Body)
	}
	code := r.JSON(t)["code"].(string)
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.Key(p2))
	if r.Status != 200 || strings.Contains(string(r.Body), "@") || strings.Contains(string(r.Body), "user_id") {
		t.Fatalf("join team leaks: %d %s", r.Status, r.Body)
	}
	// Team names are unique, whatever the case.
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "OWLS"}, a.Key(p3)); r.Status != 409 || r.JSON(t)["code"] != "team_name_taken" {
		t.Fatalf("duplicate name: %d %s", r.Status, r.Body)
	}
	// Taking someone off a team gives the team a new code.
	if r := a.At(t, "DELETE", "/v1/hack/events/"+slug+"/teams/owls/members/"+a.UserID(t, p2), nil, a.Key(org)); r.Status != http.StatusNoContent {
		t.Fatalf("take off: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.Key(p2)); r.Status != 404 || r.JSON(t)["code"] != "team_not_found" {
		t.Fatalf("old team code still works: %d %s", r.Status, r.Body)
	}
	// Clearing the end date.
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"ends_at": ""}, a.Key(org)); r.Status != 200 || r.JSON(t)["event"].(map[string]any)["ends_at"] != nil {
		t.Fatalf("clear ends_at: %d %s", r.Status, r.Body)
	}
	// judging and results are offered since M3 (closed since M2); an unknown
	// stage name is still refused outright.
	for _, st := range []string{"judging", "results"} {
		if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": st}, a.Key(org)); r.Status != 200 {
			t.Fatalf("stage %s: %d %s", st, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "not-a-real-stage"}, a.Key(org)); r.Status != 400 || r.JSON(t)["code"] != "invalid_stage" {
		t.Fatalf("unknown stage: %d %s", r.Status, r.Body)
	}
	// The admin reads an event it is not in read-only, and joins like anyone
	// else (joining is closed at this stage).
	if r := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.AdminH()); r.Status != 200 || r.JSON(t)["admin_view"] != true {
		t.Fatalf("admin view: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "Admin"}, a.AdminH()); r.Status != 409 || r.JSON(t)["code"] != "joining_closed" {
		t.Fatalf("admin join: %d %s", r.Status, r.Body)
	}
	// The list carries the event's time zone.
	found := false
	for _, ev := range hacktest.JSONArr(t, a.At(t, "GET", "/v1/hack/events", nil, a.Key(org))) {
		if ev["slug"] == slug {
			found = ev["time_zone"] != nil
		}
	}
	if !found {
		t.Fatal("list: no time_zone")
	}
	// New reserved names.
	if r := a.At(t, "GET", "/v1/hack/names/mta-sts", nil, a.Key(org)); r.Status != 200 || r.JSON(t)["available"] != false {
		t.Fatalf("mta-sts: %d %s", r.Status, r.Body)
	}
}

func TestHackReviewRound2(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "r2org")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	a.OpenEvent(t, org, slug)
	jc := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)

	// A venue's shared address spent by anonymous lookups does not stop a
	// signed-in person from joining (they are metered by account).
	for i := 0; i < 200; i++ {
		a.At(t, "GET", "/v1/hack/join/zzzzzzzz", nil, nil)
	}
	if r := a.At(t, "GET", "/v1/hack/join/zzzzzzzz", nil, nil); r.Status != http.StatusTooManyRequests {
		t.Fatalf("anonymous lookups not limited: %d", r.Status)
	}
	p1 := a.NewPerson(t, "r2p1")
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P1"}, a.Key(p1)); r.Status != 200 {
		t.Fatalf("signed-in join after the address ran dry: %d %s", r.Status, r.Body)
	}

	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Night  Owls"}, a.Key(p1)); r.Status != 201 || r.JSON(t)["name"] != "Night Owls" {
		t.Fatalf("collapse spaces: %d %s", r.Status, r.Body)
	}
	p2 := a.NewPerson(t, "r2p2")
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P2"}, a.Key(p2)); r.Status != 200 {
		t.Fatalf("join p2: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "night owls"}, a.Key(p2)); r.Status != 409 || r.JSON(t)["code"] != "team_name_taken" {
		t.Fatalf("duplicate after collapse: %d %s", r.Status, r.Body)
	}

	// Two teams of one name at the same moment: one is created, the other is
	// told the name is taken (never a server error).
	p3, p4 := a.NewPerson(t, "r2p3"), a.NewPerson(t, "r2p4")
	for _, p := range []hacktest.Person{p3, p4} {
		if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P"}, a.Key(p)); r.Status != 200 {
			t.Fatalf("join: %d %s", r.Status, r.Body)
		}
	}
	codes := make(chan int, 2)
	for _, p := range []hacktest.Person{p3, p4} {
		go func(p hacktest.Person) {
			codes <- a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Race Team"}, a.Key(p)).Status
		}(p)
	}
	got := []int{<-codes, <-codes}
	if !((got[0] == 201 && got[1] == 409) || (got[0] == 409 && got[1] == 201)) {
		t.Fatalf("race: %v", got)
	}

	// An organiser of a running event cannot delete the account; after the
	// event ends they can, and their name and address leave the event.
	orgID := a.UserID(t, org)
	if code, _, err := hacktest.ErasureBlockForTest(t, context.Background(), a.DB, orgID); err != nil || code != "organises_events" {
		t.Fatalf("running event: %q %v", code, err)
	}
	var holding string
	if err := a.DB.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&holding); err != nil {
		t.Fatal(err)
	}
	if code, _, err := hacktest.ErasureBlockForTest(t, context.Background(), a.DB, holding); err != nil || code != "event_account" {
		t.Fatalf("holding account: %q %v", code, err)
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, a.Key(org)); r.Status != 200 {
		t.Fatalf("end: %d %s", r.Status, r.Body)
	}
	// Nothing changes after the end.
	if r := a.At(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.UserID(t, p2), nil, a.Key(org)); r.Status != 409 || r.JSON(t)["code"] != "event_closed" {
		t.Fatalf("remove after end: %d %s", r.Status, r.Body)
	}
	if code, _, err := hacktest.ErasureBlockForTest(t, context.Background(), a.DB, orgID); err != nil || code != "" {
		t.Fatalf("ended event: %q %v", code, err)
	}
	var name, contact string
	if err := a.DB.QueryRow(`SELECT organiser_name, contact_email FROM events WHERE slug = $1`, slug).Scan(&name, &contact); err != nil || name != "" || contact != "" {
		t.Fatalf("organiser details kept: %q %q %v", name, contact, err)
	}
}

func TestHackReviewRound3(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "r3org")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	a.OpenEvent(t, org, slug)
	jc := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)

	// A flooded venue address still lets a signed-in person load the join page.
	for i := 0; i < 200; i++ {
		a.At(t, "GET", "/v1/hack/join/zzzzzzzz", nil, nil)
	}
	p1 := a.NewPerson(t, "r3p1")
	if r := a.At(t, "GET", "/v1/hack/join/"+jc, nil, a.Key(p1)); r.Status != 200 {
		t.Fatalf("signed-in join page at a flooded address: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P1"}, a.Key(p1)); r.Status != 200 {
		t.Fatalf("join: %d %s", r.Status, r.Body)
	}
	// Team codes are 8 characters; invisible marks do not make a new name.
	r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Race Two"}, a.Key(p1))
	if r.Status != 201 || len(r.JSON(t)["code"].(string)) != 8 {
		t.Fatalf("team code: %d %s", r.Status, r.Body)
	}
	p2 := a.NewPerson(t, "r3p2")
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P2"}, a.Key(p2)); r.Status != 200 {
		t.Fatalf("join p2: %d %s", r.Status, r.Body)
	}

	ctx := context.Background()
	var holding string
	if err := a.DB.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&holding); err != nil {
		t.Fatal(err)
	}
	if held, err := accountapp.HoldingAccount(ctx, a.DB, holding); err != nil || !held {
		t.Fatalf("holding account not recognised: %v %v", held, err)
	}
	if held, err := accountapp.HoldingAccount(ctx, a.DB, a.UserID(t, org)); err != nil || held {
		t.Fatalf("organiser taken for a holding account: %v %v", held, err)
	}
	// A person alone on a team takes the team with them.
	if code, _, err := hacktest.ErasureBlockForTest(t, ctx, a.DB, a.UserID(t, p1)); err != nil || code != "" {
		t.Fatalf("participant delete: %q %v", code, err)
	}
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, slug).Scan(&n); err != nil || n != 0 {
		t.Fatalf("empty team left: %d %v", n, err)
	}
	// A taken-down event does not hold its organiser's account.
	if _, err := a.DB.Exec(`UPDATE events SET taken_down_at = now() WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if code, _, err := hacktest.ErasureBlockForTest(t, ctx, a.DB, a.UserID(t, org)); err != nil || code != "" {
		t.Fatalf("taken-down organiser: %q %v", code, err)
	}
}

func TestHackCorrectnessRound(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "c5org")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, map[string]any{"starts_at": "2026-10-10", "ends_at": "2026-10-12", "time_zone": "UTC"}); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	// A zone change alone keeps the calendar dates.
	r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"time_zone": "America/Los_Angeles"}, a.Key(org))
	ev := r.JSON(t)["event"].(map[string]any)
	if r.Status != 200 || ev["starts_at"] != "2026-10-10T07:00:00Z" || ev["ends_at"] != "2026-10-12T07:00:00Z" {
		t.Fatalf("re-anchor: %d %v %v", r.Status, ev["starts_at"], ev["ends_at"])
	}
	// CRLF from an API client is plain text.
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"about": "one\r\ntwo"}, a.Key(org)); r.Status != 200 || r.JSON(t)["event"].(map[string]any)["about"] != "one\ntwo" {
		t.Fatalf("crlf: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	jc := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	p := a.NewPerson(t, "c5p")
	if r := a.At(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P"}, a.Key(p)); r.Status != 200 {
		t.Fatalf("join: %d %s", r.Status, r.Body)
	}
	// A bad team name has its own code.
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "ㅤ"}, a.Key(p)); r.Status != 400 || r.JSON(t)["code"] != "invalid_team_name" {
		t.Fatalf("team name code: %d %s", r.Status, r.Body)
	}
	// The admin list carries the zone.
	var listed struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(a.At(t, "GET", "/v1/admin/hack/events", nil, a.AdminH()).Body, &listed); err != nil {
		t.Fatal(err)
	}
	for _, e := range listed.Events {
		if e["slug"] == slug && e["time_zone"] != "America/Los_Angeles" {
			t.Fatalf("admin time_zone: %v", e["time_zone"])
		}
	}
	// No new links after the end.
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, a.Key(org)); r.Status != 200 {
		t.Fatalf("end: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/codes/join", nil, a.Key(org)); r.Status != 409 || r.JSON(t)["code"] != "event_closed" {
		t.Fatalf("regen after end: %d %s", r.Status, r.Body)
	}
}

func (a *featApp) makeCleanupEvent(t *testing.T, org hacktest.Person, title, contact string, nTeams int) (string, []string) {
	t.Helper()
	slug := hacktest.UniqueSlug()
	extra := map[string]any{"title": title}
	if contact != "" {
		extra["contact_email"] = contact
	}
	if r := a.CreateEvent(t, org, slug, extra); r.Status != http.StatusCreated {
		t.Fatalf("create %s: %d %s", title, r.Status, r.Body)
	}
	if nTeams == 0 {
		return slug, nil
	}
	a.OpenEvent(t, org, slug)
	join := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	teams := make([]string, 0, nTeams)
	for i := 0; i < nTeams; i++ {
		name := fmt.Sprintf("Crew %d", i+1)
		p := a.NewPerson(t, "ct")
		if r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": name}, a.Key(p)); r.Status != http.StatusOK {
			t.Fatalf("join %s: %d %s", name, r.Status, r.Body)
		}
		if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.Key(p)); r.Status != http.StatusCreated {
			t.Fatalf("team %s: %d %s", name, r.Status, r.Body)
		}
		var teamSlug string
		if err := a.DB.QueryRow(`
			SELECT t.slug FROM event_teams t JOIN events e ON e.id = t.event_id
			 WHERE e.slug = $1 AND t.name = $2`, slug, name).Scan(&teamSlug); err != nil {
			t.Fatal(err)
		}
		teams = append(teams, teamSlug)
	}
	return slug, teams
}

func (a *featApp) setEventClock(t *testing.T, slug, stage string, closed time.Time, keep bool) {
	t.Helper()
	res, err := a.DB.Exec(`UPDATE events SET stage = $2, closed_at = $3, keep_sites = $4 WHERE slug = $1`, slug, stage, closed, keep)
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		t.Fatalf("clock %s: %d %v", slug, n, err)
	}
}

// eventRest is every column the sweep must not write, including updated_at.
func (a *featApp) eventRest(t *testing.T, slug string) string {
	t.Helper()
	var s string
	err := a.DB.QueryRow(`
		SELECT concat_ws(E'\n',
			stage, title, organiser_name, organisation, contact_email, purpose,
			coalesce(tagline, ''), coalesce(about, ''), coalesce(rules, ''), coalesce(prizes, ''),
			keep_sites::text, coalesce(closed_at::text, ''), coalesce(taken_down_reason, ''),
			results_visibility, gallery_open::text, judge_assignment_mode, judges_per_team::text,
			coalesce(judging_lock_reason, ''), team_size_max::text, join_code, judge_code,
			updated_at::text, coalesce(results_published_at::text, ''), coalesce(submission_deadline::text, ''))
		  FROM events WHERE slug = $1`, slug).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (a *featApp) resultsBlob(t *testing.T, slug string) string {
	t.Helper()
	var s string
	err := a.DB.QueryRow(`
		SELECT full_ranking::text || '|' || snapshot::text || '|' || coalesce(published_by::text, '') || '|' || published_at::text
		  FROM event_results r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (a *featApp) rubricBlob(t *testing.T, slug string) string {
	t.Helper()
	rows, err := a.DB.Query(`
		SELECT c.id::text || '|' || c.position::text || '|' || c.name || '|' || c.description || '|' || c.weight::text || '|' || c.max_points::text
		  FROM rubric_criteria c JOIN events e ON e.id = c.event_id
		 WHERE e.slug = $1 ORDER BY c.position, c.id`, slug)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// cleanupSites records TrashTeamSite. fail maps a team slug to an error.
type cleanupSites struct {
	hacktest.SiteStub
	mu    sync.Mutex
	calls []siteCall
	fail  map[string]error
}

type siteCall struct{ account, slug string }

// runEventCleanup runs the archive cleanup once, as the lifecycle worker does.
func (a *featApp) runEventCleanup(ctx context.Context, now time.Time) {
	h := a.hack.Hack
	(&lifecycle.Handler{Hack: h}).RunEventCleanup(ctx, now, h.Limits.EventSitesKeep, h.Limits.EventRemovalWarn, h.Links.Trash(), events.ArchiveWarningSender(h.Mailer), h.Limits.IdleReplyTo)
}

func (s *cleanupSites) called() []siteCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]siteCall(nil), s.calls...)
}

func (a *featApp) eventStamps(t *testing.T, slug string) (sql.NullTime, sql.NullTime) {
	t.Helper()
	var warned, removed sql.NullTime
	if err := a.DB.QueryRow(`SELECT removal_warned_at, sites_removed_at FROM events WHERE slug = $1`, slug).Scan(&warned, &removed); err != nil {
		t.Fatal(err)
	}
	return warned, removed
}

func (a *featApp) wantUntouched(t *testing.T, slug, rest string) {
	t.Helper()
	if a.eventRest(t, slug) != rest {
		t.Fatalf("%s event row changed\n got %s\nwant %s", slug, a.eventRest(t, slug), rest)
	}
	if warned, removed := a.eventStamps(t, slug); warned.Valid || removed.Valid {
		t.Fatalf("%s was stamped: warned %v removed %v", slug, warned, removed)
	}
}

// cancelCleanupMailer models provider acceptance racing worker shutdown.
type cancelCleanupMailer struct {
	*hacktest.ReplyMailer
	cancel context.CancelFunc
}

func (m *cancelCleanupMailer) SendNoticeReplyToCtx(ctx context.Context, to, replyTo, subject, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.cancel()
	return m.SendNoticeReplyTo(to, replyTo, subject, text)
}

func (s *cleanupSites) TrashTeamSite(_ context.Context, accountID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, siteCall{accountID, name})
	if err := s.fail[name]; err != nil {
		return err
	}
	return nil
}
