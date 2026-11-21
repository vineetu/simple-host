package hackhttp

import (
	"encoding/csv"

	productconfig "github.com/vsriram/simple-host/products/hack/config"

	"strings"
	"testing"
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

// listEvent lists the event in Find a hackathon. Applications are taken only
// from listed events (owner, 2026-10-31).
func (a *hackApp) listEvent(t *testing.T, slug string) {
	t.Helper()
	if _, err := a.database.Exec(`UPDATE events SET directory_listed=true WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
}

func (a *hackApp) applyGet(t *testing.T, slug string, p *person) resp {
	t.Helper()
	headers := map[string]string{}
	if p != nil {
		headers = a.key(*p)
	}
	return a.at(t, "GET", "/v1/hack/events/"+slug+"/apply", nil, headers)
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
	a.listEvent(t, slug)

	a.listEvent(t, slug)

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
	a.listEvent(t, takenSlug)
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
	a.listEvent(t, slug)
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
	a.listEvent(t, slug)
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
		{"city_bidi", map[string]any{"city": "a‮b"}, "invalid_city"},
		{"country_uk", map[string]any{"country": "UK"}, "invalid_country"},
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
	a.listEvent(t, slug)
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

// TestHackApplyLimits is §8.13: the per-account rate limit, and the
// per-account pending cap (freed once one application is decided).
func TestHackApplyLimits(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "allimorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	a.listEvent(t, slug)

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
	l := productconfig.DefaultLimits()
	l.RateEventApplyUser.Burst = 100
	l.RateEventApplyIP.Burst = 100
	l.EventMaxActivePerOrganiser = 25
	l.EventCreatePerDay = 25
	b := newHackAppWithLimits(t, l)
	capOrg := b.newPerson(t, "allimcaporg")
	capPerson := b.newPerson(t, "allimcapperson")
	var slugs []string
	for i := 0; i < 21; i++ {
		s := uniqueSlug()
		if r := b.createEvent(t, capOrg, s, nil); r.status != 201 {
			t.Fatalf("create %d: %d %s", i, r.status, r.body)
		}
		b.openEvent(t, capOrg, s)
		b.listEvent(t, s)
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
	a.listEvent(t, slug)
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

// TestHackApplyRemoveAndReapply is §8.17: removing an applicant deletes the
// row and its fields; they can apply again, pending again.
func TestHackApplyRemoveAndReapply(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "arrorg")
	p := a.newPerson(t, "arrmem")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	a.listEvent(t, slug)
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
	a.listEvent(t, slug)
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
	a := newHackApp(t)
	l := productconfig.DefaultLimits()
	l.EventMaxActivePerOrganiser = 10
	l.EventCreatePerDay = 10
	setHackAppLimits(a, l)
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
	a.listEvent(t, slug)
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
	a.listEvent(t, liveSlug)
	wantTS(t, "admin view read-only", a.at(t, "PATCH", "/v1/hack/events/"+liveSlug, map[string]any{"applications_open": false}, a.adminH()), 404, "event_not_found")
}
