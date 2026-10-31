package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

// Apply to a hackathon without a join link (zp-hack-applications.sql, owner
// 2026-10-31): build-plan.md §8's Go handler and DB tests.

// applyBase helpers build a valid POST .../apply body, letting the caller
// override individual fields.
func applyBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"accept_coc":   true,
		"display_name": "Dewi Lestari",
		"city":         "Jakarta",
		"country":      "ID",
		"age_range":    "under_18",
		"gender":       "woman",
		"answers":      map[string]any{},
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

func (a *hackApp) apply(t *testing.T, slug string, p person, extra map[string]any) resp {
	t.Helper()
	return a.at(t, "POST", "/v1/hack/events/"+slug+"/apply", applyBody(extra), a.key(p))
}

func (a *hackApp) applyGet(t *testing.T, slug string, p *person) resp {
	t.Helper()
	headers := map[string]string{}
	if p != nil {
		headers = a.key(*p)
	}
	return a.at(t, "GET", "/v1/hack/events/"+slug+"/apply", nil, headers)
}

// TestHackApplyMigration is §8.1: applications_open defaults true for an
// existing event, and an existing (join-link) member has joined_via='link'
// and blank profile fields.
func TestHackApplyMigration(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "amorg")
	p := a.newPerson(t, "ammem")
	slug := uniqueSlug()
	created := a.createEvent(t, org, slug, nil).json(t)
	if created["event"].(map[string]any)["applications_open"] != true {
		t.Fatalf("applications_open default: %v", created["event"])
	}
	a.openEvent(t, org, slug)
	code := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(p)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	var joinedVia, city, country, ageRange, gender string
	if err := a.database.QueryRow(`SELECT joined_via, city, country, age_range, gender FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`,
		slug, a.userID(t, p)).Scan(&joinedVia, &city, &country, &ageRange, &gender); err != nil {
		t.Fatal(err)
	}
	if joinedVia != "link" || city != "" || country != "" || ageRange != "" || gender != "" {
		t.Fatalf("existing member row: joined_via=%q city=%q country=%q age_range=%q gender=%q", joinedVia, city, country, ageRange, gender)
	}
}

// TestHackApplyGetPreview is §8.2-4: signed out vs signed in, draft/taken
// down/unknown all 404 identically, and closed_reason for every stage.
func TestHackApplyGetPreview(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "agorg")
	p := a.newPerson(t, "agmem")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)

	// Draft: 404, same body as unknown.
	draftResp := a.applyGet(t, slug, nil)
	unknownResp := a.applyGet(t, uniqueSlug(), nil)
	wantTS(t, "draft 404", draftResp, 404, "event_not_found")
	wantTS(t, "unknown 404", unknownResp, 404, "event_not_found")
	if string(draftResp.body) != string(unknownResp.body) {
		t.Fatalf("draft/unknown bodies differ: %s vs %s", draftResp.body, unknownResp.body)
	}

	a.openEvent(t, org, slug)

	// Signed out: applications_open + fields, no signup_questions/coc_text/me.
	out := a.applyGet(t, slug, nil).json(t)
	if out["applications_open"] != true || out["closed_reason"] != nil {
		t.Fatalf("signed out open: %v", out)
	}
	if out["fields"] == nil {
		t.Fatal("fields missing")
	}
	for _, k := range []string{"signup_questions", "coc_text", "coc_default", "me"} {
		if _, present := out[k]; present {
			t.Fatalf("signed out leaked %s: %v", k, out)
		}
	}

	// Signed in, not a member: questions/coc present, me null.
	out = a.applyGet(t, slug, &p).json(t)
	if out["signup_questions"] == nil || out["coc_default"] == nil {
		t.Fatalf("signed in missing questions/coc: %v", out)
	}
	if me, present := out["me"]; !present || me != nil {
		t.Fatalf("signed in non-member me: %v", out)
	}

	// Apply, then re-read as a pending applicant.
	if r := a.apply(t, slug, p, nil); r.status != 200 {
		t.Fatalf("apply: %d %s", r.status, r.body)
	}
	out = a.applyGet(t, slug, &p).json(t)
	me, _ := out["me"].(map[string]any)
	if me["role"] != "participant" || me["approval_status"] != "pending" || me["joined_via"] != "application" {
		t.Fatalf("pending me: %v", out["me"])
	}

	// Approve the pending applicant: the later archived-stage transition
	// needs at least one approved participant (archive_needs_participants).
	wantTS(t, "approve", a.at(t, "POST", "/v1/hack/events/"+slug+"/applications/"+a.userID(t, p)+"/decision", map[string]string{"decision": "approved"}, a.key(org)), 200, "")

	// Switch off: applications_off.
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"applications_open": false}, a.key(org)); r.status != 200 {
		t.Fatalf("switch off: %d %s", r.status, r.body)
	}
	out = a.applyGet(t, slug, nil).json(t)
	if out["applications_open"] != false || out["closed_reason"] != "applications_off" {
		t.Fatalf("switch off preview: %v", out)
	}
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"applications_open": true}, a.key(org)); r.status != 200 {
		t.Fatalf("switch on: %d %s", r.status, r.body)
	}

	// Stage closed/judging/results/archived: signup_over. building: open.
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, a.key(org)); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	out = a.applyGet(t, slug, nil).json(t)
	if out["applications_open"] != true || out["closed_reason"] != nil {
		t.Fatalf("building preview: %v", out)
	}
	for _, stage := range []string{"closed", "judging", "results", "archived"} {
		if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": stage}, a.key(org)); r.status != 200 {
			t.Fatalf("%s: %d %s", stage, r.status, r.body)
		}
		out = a.applyGet(t, slug, nil).json(t)
		if out["applications_open"] != false || out["closed_reason"] != "signup_over" {
			t.Fatalf("%s preview: %v", stage, out)
		}
	}

	// Taken down: 404, same as unknown.
	takenSlug := uniqueSlug()
	a.createEvent(t, org, takenSlug, nil)
	a.openEvent(t, org, takenSlug)
	if r := a.at(t, "POST", "/v1/admin/hack/events/"+takenSlug+"/takedown", map[string]string{"reason": "spam"}, a.adminH()); r.status != 200 {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	takenResp := a.applyGet(t, takenSlug, nil)
	wantTS(t, "taken down 404", takenResp, 404, "event_not_found")
	if string(takenResp.body) != string(unknownResp.body) {
		t.Fatalf("taken-down/unknown bodies differ: %s vs %s", takenResp.body, unknownResp.body)
	}
}

// TestHackApplyGetMemberApplicationsOffFieldsHidden is pre-ship review item
// 3: signup_questions/coc_text/coc_default are gated on applications being
// open right now even for a signed-in caller (security review #5) -- but
// `me` must still come back for an existing member once applications are
// switched off, so they can see their own pending status.
func TestHackApplyGetMemberApplicationsOffFieldsHidden(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "agmoorg")
	p := a.newPerson(t, "agmomem")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	wantTS(t, "apply", a.apply(t, slug, p, nil), 200, "")

	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"applications_open": false}, a.key(org)); r.status != 200 {
		t.Fatalf("switch off: %d %s", r.status, r.body)
	}

	out := a.applyGet(t, slug, &p).json(t)
	if out["applications_open"] != false || out["closed_reason"] != "applications_off" {
		t.Fatalf("closed preview: %v", out)
	}
	for _, k := range []string{"signup_questions", "coc_text", "coc_default"} {
		if _, present := out[k]; present {
			t.Fatalf("applications off leaked %s to a signed-in member: %v", k, out)
		}
	}
	me, ok := out["me"].(map[string]any)
	if !ok {
		t.Fatalf("member's me missing while applications are off: %v", out)
	}
	if me["role"] != "participant" || me["approval_status"] != "pending" || me["joined_via"] != "application" {
		t.Fatalf("member me: %v", me)
	}
}

// TestHackApplyPostValidation is §8.5-10: the happy path, D1 (pending
// whatever approval_required says), blank optional fields, every field
// validator, the stage/switch gates and idempotent re-apply.
func TestHackApplyPostValidation(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "aporg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug

	// Happy path, all fields, normalised (country upper-cased, city spaces collapsed).
	happy := a.newPerson(t, "aphappy")
	r := a.apply(t, slug, happy, map[string]any{"city": "  Jakarta   Selatan ", "country": "id"})
	wantTS(t, "happy path", r, 200, "")
	me := r.json(t)["me"].(map[string]any)
	if me["approval_status"] != "pending" {
		t.Fatalf("happy path pending: %v", me)
	}
	var joinedVia, city, country, ageRange, gender, status string
	if err := a.database.QueryRow(`SELECT joined_via,city,country,age_range,gender,approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`,
		slug, a.userID(t, happy)).Scan(&joinedVia, &city, &country, &ageRange, &gender, &status); err != nil {
		t.Fatal(err)
	}
	if joinedVia != "application" || city != "Jakarta Selatan" || country != "ID" || ageRange != "under_18" || gender != "woman" || status != "pending" {
		t.Fatalf("stored row: via=%q city=%q country=%q age=%q gender=%q status=%q", joinedVia, city, country, ageRange, gender, status)
	}

	// D1: approval_required=false still leaves the application pending.
	if r := a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false}, a.key(org)); r.status != 200 {
		t.Fatalf("approval off: %d %s", r.status, r.body)
	}
	notReq := a.newPerson(t, "apnotreq")
	r = a.apply(t, slug, notReq, nil)
	wantTS(t, "apply with approval off", r, 200, "")
	if r.json(t)["me"].(map[string]any)["approval_status"] != "pending" {
		t.Fatalf("D1 violated: %v", r.json(t)["me"])
	}

	// Every optional field empty: 200, blanks stored.
	blank := a.newPerson(t, "apblank")
	r = a.apply(t, slug, blank, map[string]any{"city": "", "country": "", "age_range": "", "gender": ""})
	wantTS(t, "all optional blank", r, 200, "")
	if err := a.database.QueryRow(`SELECT city,country,age_range,gender FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`,
		slug, a.userID(t, blank)).Scan(&city, &country, &ageRange, &gender); err != nil {
		t.Fatal(err)
	}
	if city != "" || country != "" || ageRange != "" || gender != "" {
		t.Fatalf("blank fields not blank: %q %q %q %q", city, country, ageRange, gender)
	}

	// Validation, in order. Each case uses its own account: the apply route's
	// rate limiter runs before any field validation, so one account would
	// otherwise trip RATE_LIMIT_EVENT_APPLY_USER's default burst of 5 well
	// before the end of this list.
	cases := []struct {
		name  string
		extra map[string]any
		code  string
	}{
		{"coc_required", map[string]any{"accept_coc": false}, "coc_required"},
		{"display_name_empty", map[string]any{"display_name": ""}, "invalid_display_name"},
		{"city_too_long", map[string]any{"city": strings.Repeat("a", 81)}, "invalid_city"},
		{"city_newline", map[string]any{"city": "a\nb"}, "invalid_city"},
		{"city_bidi", map[string]any{"city": "a‮b"}, "invalid_city"},
		{"country_xx", map[string]any{"country": "XX"}, "invalid_country"},
		{"country_zz", map[string]any{"country": "ZZ"}, "invalid_country"},
		{"country_idn", map[string]any{"country": "IDN"}, "invalid_country"},
		{"country_1d", map[string]any{"country": "1D"}, "invalid_country"},
		{"country_uk", map[string]any{"country": "UK"}, "invalid_country"},
		{"age_range_bad", map[string]any{"age_range": "teen"}, "invalid_age_range"},
		{"gender_bad", map[string]any{"gender": "x"}, "invalid_gender"},
		{"answer_unknown", map[string]any{"answers": map[string]any{"nope": "x"}}, "invalid_answers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			who := a.newPerson(t, "apval"+tc.name)
			wantTS(t, tc.name, a.apply(t, slug, who, tc.extra), 400, tc.code)
		})
	}
	wantTS(t, "country gb ok", a.apply(t, slug, a.newPerson(t, "apvalgb"), map[string]any{"country": "GB"}), 200, "")

	// Required question.
	q := a.newPerson(t, "apquestion")
	if r := a.at(t, "PUT", base+"/registration", map[string]any{"questions": []map[string]any{{"id": "why", "prompt": "Why?", "required": true}}, "approval_required": false}, a.key(org)); r.status != 200 {
		t.Fatalf("set questions: %d %s", r.status, r.body)
	}
	wantTS(t, "answer required", a.apply(t, slug, q, map[string]any{"answers": map[string]any{}}), 400, "answer_required")
	wantTS(t, "answer given", a.apply(t, slug, q, map[string]any{"answers": map[string]any{"why": "Because"}}), 200, "")
	if r := a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false}, a.key(org)); r.status != 200 {
		t.Fatalf("clear questions: %d %s", r.status, r.body)
	}

	// Switch off -> 409 applications_closed.
	if r := a.at(t, "PATCH", base, map[string]any{"applications_open": false}, a.key(org)); r.status != 200 {
		t.Fatalf("switch off: %d %s", r.status, r.body)
	}
	off := a.newPerson(t, "apoff")
	wantTS(t, "switch off", a.apply(t, slug, off, nil), 409, "applications_closed")
	if r := a.at(t, "PATCH", base, map[string]any{"applications_open": true}, a.key(org)); r.status != 200 {
		t.Fatalf("switch on: %d %s", r.status, r.body)
	}

	// Building stage: 200. Closed stage: 409 joining_closed.
	if r := a.at(t, "POST", base+"/stage", map[string]string{"stage": "building"}, a.key(org)); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	building := a.newPerson(t, "apbuilding")
	wantTS(t, "building ok", a.apply(t, slug, building, nil), 200, "")
	if r := a.at(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, a.key(org)); r.status != 200 {
		t.Fatalf("closed: %d %s", r.status, r.body)
	}
	closedP := a.newPerson(t, "apclosed")
	wantTS(t, "closed stage", a.apply(t, slug, closedP, nil), 409, "joining_closed")
	if r := a.at(t, "POST", base+"/stage", map[string]string{"stage": "open"}, a.key(org)); r.status != 200 {
		t.Fatalf("reopen: %d %s", r.status, r.body)
	}

	// Idempotent: a second POST by the same applicant changes nothing.
	r1 := a.apply(t, slug, happy, map[string]any{"city": "Somewhere else"})
	wantTS(t, "idempotent repeat", r1, 200, "")
	if err := a.database.QueryRow(`SELECT city FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, happy)).Scan(&city); err != nil {
		t.Fatal(err)
	}
	if city != "Jakarta Selatan" {
		t.Fatalf("idempotent re-apply overwrote fields: %q", city)
	}

	// Approve happy, reject notReq; both re-apply as 200, unchanged status.
	wantTS(t, "approve", a.at(t, "POST", base+"/applications/"+a.userID(t, happy)+"/decision", map[string]string{"decision": "approved"}, a.key(org)), 200, "")
	if _, err := a.database.Exec(`UPDATE event_members SET city='Bandung',country='ID',age_range='under_18',gender='woman' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, notReq)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "reject", a.at(t, "POST", base+"/applications/"+a.userID(t, notReq)+"/decision", map[string]string{"decision": "rejected"}, a.key(org)), 200, "")
	// Rejecting an application clears its optional profile fields; approving keeps them.
	var rc, rco, rar, rg string
	if err := a.database.QueryRow(`SELECT city,country,age_range,gender FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, notReq)).Scan(&rc, &rco, &rar, &rg); err != nil {
		t.Fatal(err)
	}
	if rc != "" || rco != "" || rar != "" || rg != "" {
		t.Fatalf("rejected application kept profile fields: %q %q %q %q", rc, rco, rar, rg)
	}
	if err := a.database.QueryRow(`SELECT city FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, happy)).Scan(&city); err != nil {
		t.Fatal(err)
	}
	if city != "Jakarta Selatan" {
		t.Fatalf("approved application lost its city: %q", city)
	}
	wantTS(t, "approved re-apply", a.apply(t, slug, happy, nil), 200, "")
	if err := a.database.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, happy)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Fatalf("approved member changed on re-apply: %q", status)
	}
	wantTS(t, "rejected re-apply", a.apply(t, slug, notReq, nil), 200, "")
	if err := a.database.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, notReq)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" {
		t.Fatalf("rejected applicant was re-admitted: %q", status)
	}

	// A different role (the organiser themselves) applying -> already_member.
	wantTS(t, "organiser applying", a.apply(t, slug, org, nil), 409, "already_member")
}

// TestHackApplyRace is §8.11-12: two concurrent applies by one user land one
// row both 200, and applying concurrently with the organiser's switch-off
// either commits first or is refused -- never both committed and refused.
func TestHackApplyRace(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "arorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)

	racer := a.newPerson(t, "arracer")
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := []resp{}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			r := a.apply(t, slug, racer, nil)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.status != 200 {
			t.Fatalf("concurrent apply: %d %s", r.status, r.body)
		}
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, racer)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("race produced %d rows, want 1", n)
	}

	// Codex 4: the loose "either outcome" assertion above would also pass a
	// broken implementation that reads applications_open from the
	// pre-transaction snapshot instead of re-checking it under the events
	// row's FOR SHARE lock. Force the ordering instead of racing goroutine
	// scheduling: hold the event row FOR UPDATE from a second connection (as
	// review_fixes_test.go's waitBlocked pattern does for a site lock), let
	// the apply block on it, flip the switch and commit while apply is
	// still waiting, then require that the apply which proceeds *after*
	// that commit sees the closed switch -- 409 and no inserted row, never
	// a 200.
	switchSlug := uniqueSlug()
	a.createEvent(t, org, switchSlug, nil)
	a.openEvent(t, org, switchSlug)
	switcher := a.newPerson(t, "arswitcher")

	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var switchEventID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE slug=$1 FOR UPDATE`, switchSlug).Scan(&switchEventID); err != nil {
		t.Fatal(err)
	}

	applyDone := make(chan resp, 1)
	go func() { applyDone <- a.apply(t, switchSlug, switcher, nil) }()
	waitBlocked() // the apply's own FOR SHARE now waits on lockTx's FOR UPDATE.

	if _, err := lockTx.Exec(`UPDATE events SET applications_open=false WHERE id=$1`, switchEventID); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	applyResp := <-applyDone
	wantTS(t, "apply proceeding after switch-off commit", applyResp, 409, "applications_closed")
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=$1 AND user_id=$2`, switchEventID, a.userID(t, switcher)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("apply inserted a row despite the switch-off that committed first: %d rows", n)
	}
}

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
	org := a.newPerson(t, "atdorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	applicant := a.newPerson(t, "atdapplicant")

	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE slug=$1 FOR UPDATE`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	applyDone := make(chan resp, 1)
	go func() { applyDone <- a.apply(t, slug, applicant, nil) }()
	waitBlocked() // the apply's own row lock now waits on lockTx's FOR UPDATE.

	if _, err := lockTx.Exec(`UPDATE events SET taken_down_at = COALESCE(taken_down_at, now()), taken_down_reason = $2, updated_at = now() WHERE id = $1`, eventID, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	applyResp := <-applyDone
	wantTS(t, "apply proceeding after takedown commit", applyResp, 404, "event_not_found")
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, a.userID(t, applicant)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("apply inserted a row despite the takedown that committed first: %d rows", n)
	}
}

// TestHackApplyPerEventCapConcurrentDistinctApplicants is pre-ship review
// item 1 (B2): the per-event ceiling used to be counted under the events
// row's FOR SHARE lock, which lets any number of concurrent applicants hold
// that lock at once instead of serializing on it -- 20 concurrent
// applicants against a cap of 3 produced 10 rows. The events row is now
// locked FOR UPDATE before the count, so N concurrent *distinct* applicants
// (no account-level cap or lock in the way) must land exactly cap rows,
// with every other one refused 409 event_applications_full -- never more
// than the cap committed.
func TestHackApplyPerEventCapConcurrentDistinctApplicants(t *testing.T) {
	old := *config.Active()
	l := old
	l.EventApplyPendingPerEvent = 3
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })

	a := newHackApp(t)
	org := a.newPerson(t, "apeccorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)

	const n, wantCap = 10, 3
	people := make([]person, n)
	for i := range people {
		people[i] = a.newPerson(t, fmt.Sprintf("apeccuser%d", i))
	}

	var wg sync.WaitGroup
	results := make([]resp, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = a.apply(t, slug, people[i], nil)
		}()
	}
	wg.Wait()

	wins, losses := 0, 0
	for _, r := range results {
		switch {
		case r.status == 200:
			wins++
		case r.status == 409 && r.json(t)["code"] == "event_applications_full":
			losses++
		default:
			t.Fatalf("unexpected concurrent apply result: %d %s", r.status, r.body)
		}
	}
	if wins != wantCap || losses != n-wantCap {
		t.Fatalf("concurrent applies at the per-event cap: %d won, %d lost (want %d and %d)", wins, losses, wantCap, n-wantCap)
	}

	var count int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND approval_status='pending'`, slug).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != wantCap {
		t.Fatalf("event has %d pending rows, want exactly the cap of %d", count, wantCap)
	}
}

// TestHackApplyLimits is §8.13: the per-account rate limit, and the
// per-account pending cap (freed once one application is decided).
func TestHackApplyLimits(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "allimorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)

	// Default RATE_LIMIT_EVENT_APPLY_USER is burst 5: a 6th apply (even an
	// idempotent repeat, since the limiter runs before anything else) is 429.
	limited := a.newPerson(t, "allimuser")
	for i := 0; i < 5; i++ {
		if r := a.apply(t, slug, limited, nil); r.status != 200 {
			t.Fatalf("apply %d: %d %s", i, r.status, r.body)
		}
	}
	wantTS(t, "6th apply", a.apply(t, slug, limited, nil), 429, "rate_limited")

	// The pending cap (20) is a business rule independent of the rate
	// limiter: raise both knobs generously on a fresh app so the cap, not
	// the limiter or the per-organiser event cap, is what trips.
	old := *config.Active()
	l := old
	l.RateEventApplyUser.Burst = 100
	l.RateEventApplyIP.Burst = 100
	l.EventMaxActivePerOrganiser = 25
	l.EventCreatePerDay = 25
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })

	b := newHackApp(t)
	capOrg := b.newPerson(t, "allimcaporg")
	capPerson := b.newPerson(t, "allimcapperson")
	var slugs []string
	for i := 0; i < 21; i++ {
		s := uniqueSlug()
		if r := b.createEvent(t, capOrg, s, nil); r.status != 201 {
			t.Fatalf("create %d: %d %s", i, r.status, r.body)
		}
		b.openEvent(t, capOrg, s)
		slugs = append(slugs, s)
	}
	for i, s := range slugs[:20] {
		if r := b.apply(t, s, capPerson, nil); r.status != 200 {
			t.Fatalf("apply %d to %s: %d %s", i, s, r.status, r.body)
		}
	}
	wantTS(t, "21st pending", b.apply(t, slugs[20], capPerson, nil), 409, "too_many_pending")

	// Decide the first one; applying to the 21st then works.
	wantTS(t, "decide one", b.at(t, "POST", "/v1/hack/events/"+slugs[0]+"/applications/"+b.userID(t, capPerson)+"/decision", map[string]string{"decision": "approved"}, b.key(capOrg)), 200, "")
	wantTS(t, "21st after decision", b.apply(t, slugs[20], capPerson, nil), 200, "")
}

// TestHackApplyPendingCapConcurrentCrossEvent is the security review's
// Codex 2/Grok 2 fix: the 20-pending cap is read and inserted against under
// a lock on the applicant's own users row (db.LockUserRow), so two
// concurrent applies to two *different* events -- which lock different
// events rows and so cannot serialise on those alone -- still cannot both
// read 19 and both insert, landing at 21. Exactly one of the two must win.
func TestHackApplyPendingCapConcurrentCrossEvent(t *testing.T) {
	old := *config.Active()
	l := old
	l.RateEventApplyUser.Burst = 100
	l.RateEventApplyIP.Burst = 100
	l.EventMaxActivePerOrganiser = 25
	l.EventCreatePerDay = 25
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })

	b := newHackApp(t)
	org := b.newPerson(t, "apccorg")
	racer := b.newPerson(t, "apccracer")
	var slugs []string
	for i := 0; i < 21; i++ {
		s := uniqueSlug()
		if r := b.createEvent(t, org, s, nil); r.status != 201 {
			t.Fatalf("create %d: %d %s", i, r.status, r.body)
		}
		b.openEvent(t, org, s)
		slugs = append(slugs, s)
	}
	// 19 pending, one below the cap of 20.
	for i, s := range slugs[:19] {
		if r := b.apply(t, s, racer, nil); r.status != 200 {
			t.Fatalf("apply %d to %s: %d %s", i, s, r.status, r.body)
		}
	}

	var wg sync.WaitGroup
	results := make([]resp, 2)
	wg.Add(2)
	for i, s := range slugs[19:21] {
		i, s := i, s
		go func() {
			defer wg.Done()
			results[i] = b.apply(t, s, racer, nil)
		}()
	}
	wg.Wait()

	wins, losses := 0, 0
	for _, r := range results {
		switch {
		case r.status == 200:
			wins++
		case r.status == 409 && r.json(t)["code"] == "too_many_pending":
			losses++
		default:
			t.Fatalf("unexpected concurrent apply result: %d %s", r.status, r.body)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("concurrent cross-event applies at the cap: %d won, %d lost (want 1 and 1)", wins, losses)
	}

	pending, err := db.CountPendingApplications(context.Background(), b.database, b.userID(t, racer))
	if err != nil {
		t.Fatal(err)
	}
	if pending != 20 {
		t.Fatalf("pending applications after the race = %d, want exactly 20 (the cap)", pending)
	}
}

// TestHackApplyIPLimitIndependentOfAccounts is Codex 5: the existing limits
// test only ever exhausted one account's own bucket. RATE_LIMIT_EVENT_APPLY_IP
// must trip on its own: distinct accounts, each applying for the very first
// time (nowhere near their own per-account burst), still share one network
// address's bucket and can be capped by it.
func TestHackApplyIPLimitIndependentOfAccounts(t *testing.T) {
	old := *config.Active()
	l := old
	l.RateEventApplyIP.Burst = 3
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })

	a := newHackApp(t)
	org := a.newPerson(t, "aiplorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)

	for i := 0; i < 3; i++ {
		p := a.newPerson(t, fmt.Sprintf("aipluser%d", i))
		if r := a.apply(t, slug, p, nil); r.status != 200 {
			t.Fatalf("apply %d (own first attempt, well under its account limit): %d %s", i, r.status, r.body)
		}
	}
	fourth := a.newPerson(t, "aipluser4")
	wantTS(t, "4th distinct account, shared IP bucket exhausted", a.apply(t, slug, fourth, nil), 429, "rate_limited")
}

// TestHackApplyApplicationsAndPeopleFields is §8.14-15: GET .../applications
// and GET .../people carry the new fields, role gates stay the same, and a
// decision's effect on team access is unchanged.
func TestHackApplyApplicationsAndPeopleFields(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "aaforg")
	judge := a.newPerson(t, "aafjudge")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug

	applicant := a.newPerson(t, "aafapplicant")
	wantTS(t, "apply", a.apply(t, slug, applicant, map[string]any{"city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman"}), 200, "")

	apps := jsonArr(t, a.at(t, "GET", base+"/applications", nil, a.key(org)))
	var found map[string]any
	for _, row := range apps {
		if row["user_id"] == a.userID(t, applicant) {
			found = row
		}
	}
	if found == nil {
		t.Fatal("application missing from the list")
	}
	if found["joined_via"] != "application" || found["city"] != "Jakarta" || found["country"] != "ID" || found["age_range"] != "under_18" || found["gender"] != "woman" {
		t.Fatalf("application row: %v", found)
	}

	people := jsonArr(t, a.at(t, "GET", base+"/people", nil, a.key(org)))
	var personRow map[string]any
	for _, row := range people {
		if row["user_id"] == a.userID(t, applicant) {
			personRow = row
		}
	}
	if personRow == nil {
		t.Fatal("applicant missing from people")
	}
	if personRow["joined_via"] != "application" {
		t.Fatalf("people row joined_via: %v", personRow)
	}
	for _, k := range []string{"city", "country", "age_range", "gender"} {
		if _, present := personRow[k]; present {
			t.Fatalf("profile field %q leaked into GET /people: %v", k, personRow)
		}
	}

	// Judges/participants cannot read applications or the people list.
	if r := a.at(t, "POST", "/v1/hack/judge/"+a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["judge_code"].(string), map[string]any{"accept_coc": true, "display_name": "J"}, a.key(judge)); r.status != 200 {
		t.Fatalf("judge join: %d %s", r.status, r.body)
	}
	wantTS(t, "judge reads applications", a.at(t, "GET", base+"/applications", nil, a.key(judge)), 404, "event_not_found")
	wantTS(t, "applicant reads people", a.at(t, "GET", base+"/people", nil, a.key(applicant)), 403, "approval_pending")

	// Reject the applicant: blocked from team routes, exactly as today.
	wantTS(t, "reject", a.at(t, "POST", base+"/applications/"+a.userID(t, applicant)+"/decision", map[string]string{"decision": "rejected"}, a.key(org)), 200, "")
	wantTS(t, "rejected team denied", a.at(t, "POST", base+"/teams", map[string]string{"name": "No"}, a.key(applicant)), 403, "approval_rejected")
}

// TestHackApplyAdmitPendingLinkOnly is §8.16 (D5): turning approval off and
// admitting pending only admits join-link sign-ups; an application stays
// pending.
func TestHackApplyAdmitPendingLinkOnly(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "aaplorg")
	linkPerson := a.newPerson(t, "aaplink")
	appPerson := a.newPerson(t, "aaplapp")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug

	if r := a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": true}, a.key(org)); r.status != 200 {
		t.Fatalf("approval on: %d %s", r.status, r.body)
	}
	code := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	wantTS(t, "link join", a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "L"}, a.key(linkPerson)), 200, "")
	wantTS(t, "apply", a.apply(t, slug, appPerson, nil), 200, "")

	r := a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false, "admit_pending": true}, a.key(org))
	wantTS(t, "admit pending", r, 200, "")

	var linkStatus, appStatus string
	if err := a.database.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, linkPerson)).Scan(&linkStatus); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, appPerson)).Scan(&appStatus); err != nil {
		t.Fatal(err)
	}
	if linkStatus != "approved" {
		t.Fatalf("join-link pending not admitted: %q", linkStatus)
	}
	if appStatus != "pending" {
		t.Fatalf("D5 violated: application admitted by admit_pending: %q", appStatus)
	}
}

// TestHackApplyRemoveAndReapply is §8.17: removing an applicant deletes the
// row and its fields; they can apply again, pending again.
func TestHackApplyRemoveAndReapply(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "arrorg")
	p := a.newPerson(t, "arrmem")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	wantTS(t, "apply", a.apply(t, slug, p, map[string]any{"city": "Jakarta"}), 200, "")
	wantTS(t, "remove", a.at(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.userID(t, p), nil, a.key(org)), 204, "")
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, p)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("removed row still present")
	}
	r := a.apply(t, slug, p, map[string]any{"city": "Bandung"})
	wantTS(t, "reapply", r, 200, "")
	if r.json(t)["me"].(map[string]any)["approval_status"] != "pending" {
		t.Fatalf("reapply status: %v", r.json(t)["me"])
	}
}

// TestHackApplyParticipantsCSV is §8.18 (D11): eleven columns in order, the
// right labels, formula-safe cells, pending applicants excluded.
func TestHackApplyParticipantsCSV(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "acsvorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug

	code := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	linked := a.newPerson(t, "acsvlinked")
	wantTS(t, "join", a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "Link"}, a.key(linked)), 200, "")

	applicant := a.newPerson(t, "acsvapplicant")
	formula := a.newPerson(t, "acsvformula")
	// Security review (Grok 7): a leading Unicode format character (U+200E)
	// must not hide a formula from csvSafe either.
	lrmFormula := a.newPerson(t, "acsvlrmformula")
	wantTS(t, "apply", a.apply(t, slug, applicant, map[string]any{"city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman"}), 200, "")
	wantTS(t, "apply formula city", a.apply(t, slug, formula, map[string]any{"city": "=HYPERLINK(\"https://evil\")", "country": "GB", "age_range": "35_plus", "gender": "man"}), 200, "")
	wantTS(t, "apply lrm formula city", a.apply(t, slug, lrmFormula, map[string]any{"city": "‎=HYPERLINK(\"https://evil\")", "country": "GB", "age_range": "35_plus", "gender": "man"}), 200, "")
	pending := a.newPerson(t, "acsvpending")
	wantTS(t, "pending applicant", a.apply(t, slug, pending, nil), 200, "")

	wantTS(t, "approve applicant", a.at(t, "POST", base+"/applications/"+a.userID(t, applicant)+"/decision", map[string]string{"decision": "approved"}, a.key(org)), 200, "")
	wantTS(t, "approve formula", a.at(t, "POST", base+"/applications/"+a.userID(t, formula)+"/decision", map[string]string{"decision": "approved"}, a.key(org)), 200, "")
	wantTS(t, "approve lrm formula", a.at(t, "POST", base+"/applications/"+a.userID(t, lrmFormula)+"/decision", map[string]string{"decision": "approved"}, a.key(org)), 200, "")

	r := a.at(t, "GET", base+"/export/participants.csv", nil, a.key(org))
	if r.status != 200 {
		t.Fatalf("csv: %d %s", r.status, r.body)
	}
	rows, err := csv.NewReader(strings.NewReader(string(r.body))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{"name", "email", "team", "team_slug", "joined_at", "coc_accepted_at", "city", "country", "age_range", "gender", "joined_via"}
	if len(rows) == 0 || len(rows[0]) != 11 {
		t.Fatalf("header columns: %v", rows[0])
	}
	for i, h := range wantHeader {
		if rows[0][i] != h {
			t.Fatalf("header[%d] = %q, want %q", i, rows[0][i], h)
		}
	}
	byEmail := map[string][]string{}
	for _, row := range rows[1:] {
		byEmail[row[1]] = row
	}
	if _, present := byEmail[pending.email]; present {
		t.Fatalf("pending applicant in CSV: %v", byEmail)
	}
	linkRow, ok := byEmail[linked.email]
	if !ok {
		t.Fatal("link-joined participant missing from CSV")
	}
	if linkRow[6] != "" || linkRow[7] != "" || linkRow[8] != "" || linkRow[9] != "" || linkRow[10] != "join link" {
		t.Fatalf("link row: %v", linkRow)
	}
	appRow, ok := byEmail[applicant.email]
	if !ok {
		t.Fatal("applicant missing from CSV")
	}
	if appRow[6] != "Jakarta" || appRow[7] != "Indonesia" || appRow[8] != "Under 18" || appRow[9] != "Woman or girl" || appRow[10] != "application" {
		t.Fatalf("applicant row: %v", appRow)
	}
	formulaRow, ok := byEmail[formula.email]
	if !ok {
		t.Fatal("formula applicant missing from CSV")
	}
	if !strings.HasPrefix(formulaRow[6], "'=") {
		t.Fatalf("formula city not neutralised: %q", formulaRow[6])
	}
	if formulaRow[7] != "United Kingdom" || formulaRow[8] != "35+" || formulaRow[9] != "Man or boy" {
		t.Fatalf("formula row other fields: %v", formulaRow)
	}
	lrmRow, ok := byEmail[lrmFormula.email]
	if !ok {
		t.Fatal("lrm formula applicant missing from CSV")
	}
	if !strings.HasPrefix(lrmRow[6], "'‎=") {
		t.Fatalf("lrm formula city not neutralised: %q", lrmRow[6])
	}
}

// TestHackApplyDirectoryAndPublicJSON is §8.19-20: directory and public
// rows carry applications_open/apply_url by the shared rule, and no
// approval_required key leaks into either.
func TestHackApplyDirectoryAndPublicJSON(t *testing.T) {
	// Three events from one organiser: raise the per-organiser active-event
	// cap (default 2) so the third creation does not silently fail.
	old := *config.Active()
	l := old
	l.EventMaxActivePerOrganiser = 10
	l.EventCreatePerDay = 10
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })

	a := newHackApp(t)
	org := a.newPerson(t, "adporg")
	openSlug, closedSwitchSlug, unlistedSlug := uniqueSlug(), uniqueSlug(), uniqueSlug()
	for _, slug := range []string{openSlug, closedSwitchSlug, unlistedSlug} {
		if r := a.createEvent(t, org, slug, nil); r.status != 201 {
			t.Fatalf("create %s: %d %s", slug, r.status, r.body)
		}
		a.openEvent(t, org, slug)
	}
	for _, slug := range []string{openSlug, closedSwitchSlug} {
		if r := a.at(t, "PATCH", "/v1/hack/events/"+slug+"/directory", map[string]any{"listed": true}, a.key(org)); r.status != 200 {
			t.Fatalf("list %s: %d %s", slug, r.status, r.body)
		}
	}
	if r := a.at(t, "PATCH", "/v1/hack/events/"+closedSwitchSlug, map[string]any{"applications_open": false}, a.key(org)); r.status != 200 {
		t.Fatalf("switch off: %d %s", r.status, r.body)
	}

	groups := a.at(t, "GET", "/v1/hack/directory", nil, nil).json(t)
	rows := append(append([]any{}, groups["now"].([]any)...), groups["upcoming"].([]any)...)
	rows = append(rows, groups["past"].([]any)...)
	found := map[string]map[string]any{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		found[row["slug"].(string)] = row
	}
	open, ok := found[openSlug]
	if !ok {
		t.Fatal("listed open event missing from directory")
	}
	if open["applications_open"] != true || open["apply_url"] == nil || open["apply_url"] == "" {
		t.Fatalf("open directory row: %v", open)
	}
	off, ok := found[closedSwitchSlug]
	if !ok {
		t.Fatal("switched-off event missing from directory")
	}
	if off["applications_open"] != false {
		t.Fatalf("switched-off directory row: %v", off)
	}
	if _, present := off["apply_url"]; present {
		t.Fatalf("apply_url present when closed: %v", off)
	}
	if _, present := found[unlistedSlug]; present {
		t.Fatal("unlisted event appeared in the directory")
	}

	pub := a.at(t, "GET", "/v1/hack/events/"+openSlug+"/public", nil, nil).json(t)
	if pub["applications_open"] != true || pub["apply_url"] == nil {
		t.Fatalf("public json: %v", pub)
	}
	if _, present := pub["approval_required"]; present {
		t.Fatalf("approval_required leaked into public json: %v", pub)
	}
	pubOff := a.at(t, "GET", "/v1/hack/events/"+closedSwitchSlug+"/public", nil, nil).json(t)
	if pubOff["applications_open"] != false {
		t.Fatalf("public json switched off: %v", pubOff)
	}
}

// TestHackApplyPatchEventToggle is §8.21: PATCH applications_open, role and
// stage gates.
func TestHackApplyPatchEventToggle(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "apetorg")
	participant := a.newPerson(t, "apetpart")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug

	wantTS(t, "organiser off", a.at(t, "PATCH", base, map[string]any{"applications_open": false}, a.key(org)), 200, "")
	wantTS(t, "organiser on", a.at(t, "PATCH", base, map[string]any{"applications_open": true}, a.key(org)), 200, "")

	code := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	wantTS(t, "participant joins", a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(participant)), 200, "")
	wantTS(t, "participant denied", a.at(t, "PATCH", base, map[string]any{"applications_open": false}, a.key(participant)), 404, "event_not_found")

	wantTS(t, "stage archived", a.at(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, a.key(org)), 200, "")
	wantTS(t, "ended event", a.at(t, "PATCH", base, map[string]any{"applications_open": false}, a.key(org)), 409, "event_closed")

	// The platform admin reading an event it is not in is read-only: a
	// write (even on a live event) is refused the same as any non-member.
	liveSlug := uniqueSlug()
	a.createEvent(t, org, liveSlug, nil)
	a.openEvent(t, org, liveSlug)
	wantTS(t, "admin view read-only", a.at(t, "PATCH", "/v1/hack/events/"+liveSlug, map[string]any{"applications_open": false}, a.adminH()), 404, "event_not_found")
}

// TestHackApplyMyEventsApprovalStatus is §8.24: GET /v1/hack/events carries
// approval_status on participant rows.
func TestHackApplyMyEventsApprovalStatus(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "aesorg")
	p := a.newPerson(t, "aesmem")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	wantTS(t, "apply", a.apply(t, slug, p, nil), 200, "")

	rows := jsonArr(t, a.at(t, "GET", "/v1/hack/events", nil, a.key(p)))
	var row map[string]any
	for _, r := range rows {
		if r["slug"] == slug {
			row = r
		}
	}
	if row == nil || row["approval_status"] != "pending" {
		t.Fatalf("my events row: %v", row)
	}

	orgRows := jsonArr(t, a.at(t, "GET", "/v1/hack/events", nil, a.key(org)))
	var orgRow map[string]any
	for _, r := range orgRows {
		if r["slug"] == slug {
			orgRow = r
		}
	}
	if _, present := orgRow["approval_status"]; present {
		t.Fatalf("organiser row should not carry approval_status: %v", orgRow)
	}
}

// TestHackApplyPostCodeJoinRegression is §8.22: the join route behaves
// exactly as before (joined_via='link', blank profile fields) after
// extracting validateSignupAnswers.
func TestHackApplyPostCodeJoinRegression(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "arjorg")
	p := a.newPerson(t, "arjmem")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	code := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	wantTS(t, "join", a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(p)), 200, "")
	var joinedVia, city string
	if err := a.database.QueryRow(`SELECT joined_via, city FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.userID(t, p)).Scan(&joinedVia, &city); err != nil {
		t.Fatal(err)
	}
	if joinedVia != "link" || city != "" {
		t.Fatalf("join stored joined_via=%q city=%q", joinedVia, city)
	}
}

// TestHackApplyCountryValidatorCoverage is §8.25: the server's accepted
// ISO 3166-1 alpha-2 codes are exactly the fixed list
// screens/public-apply.js offers -- no drift between the two. If that file
// is not present yet (another agent's in-flight work), the test is skipped
// rather than failing spuriously.
func TestHackApplyCountryValidatorCoverage(t *testing.T) {
	js, err := embeddedStatic.ReadFile("static/hack/screens/public-apply.js")
	if err != nil {
		t.Skip("screens/public-apply.js not present yet")
	}
	codes := extractCountryCodes(string(js))
	if len(codes) == 0 {
		t.Skip("public-apply.js has no COUNTRY_CODES list yet")
	}
	rec := func(code string) (int, string) {
		w := newTestResponseRecorder()
		_, ok := checkHackCountry(w, code)
		if ok {
			return 200, ""
		}
		return w.status, w.code
	}
	for _, code := range codes {
		if status, _ := rec(code); status != 200 {
			t.Errorf("server rejects %s, which public-apply.js offers", code)
		}
	}
	// Every two-letter region the validator's underlying rule accepts must
	// be in the UI's list too (no server-only option nobody can pick, and
	// no UI option the server silently rejects).
	offered := map[string]bool{}
	for _, c := range codes {
		offered[c] = true
	}
	letters := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for _, x := range letters {
		for _, y := range letters {
			code := string(x) + string(y)
			status, _ := rec(code)
			if status == 200 && !offered[code] {
				t.Errorf("server accepts %s, which public-apply.js does not offer", code)
			}
		}
	}
}

func extractCountryCodes(js string) []string {
	start := strings.Index(js, "COUNTRY_CODES")
	if start < 0 {
		return nil
	}
	open := strings.Index(js[start:], "[")
	close := strings.Index(js[start:], "]")
	if open < 0 || close < 0 {
		return nil
	}
	block := js[start+open+1 : start+close]
	var codes []string
	for _, part := range strings.Split(block, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "'\"")
		if len(part) == 2 {
			codes = append(codes, part)
		}
	}
	return codes
}

// testResponseRecorder is a tiny http.ResponseWriter that captures just
// what checkHackCountry's error path writes, without pulling in net/http/httptest
// semantics this check does not need.
type testResponseRecorder struct {
	header http.Header
	status int
	code   string
}

func newTestResponseRecorder() *testResponseRecorder {
	return &testResponseRecorder{header: http.Header{}, status: 200}
}
func (w *testResponseRecorder) Header() http.Header { return w.header }
func (w *testResponseRecorder) Write(b []byte) (int, error) {
	var out map[string]any
	if json.Unmarshal(b, &out) == nil {
		if c, ok := out["code"].(string); ok {
			w.code = c
		}
	}
	return len(b), nil
}
func (w *testResponseRecorder) WriteHeader(status int) { w.status = status }
