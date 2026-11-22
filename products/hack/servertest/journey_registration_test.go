package servertest

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	"github.com/vsriram/simple-host/products/hack/hacktest"
)

// TestHackApplyGetPreview is §8.2-4: signed out vs signed in, draft/taken
// down/unknown all 404 identically, and closed_reason for every stage.
func TestHackApplyGetPreview(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "agorg")
	p := a.NewPerson(t, "agmem")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)

	// Draft: 404, same body as unknown.
	draftResp := a.ApplyGet(t, slug, nil)
	unknownResp := a.ApplyGet(t, hacktest.UniqueSlug(), nil)
	hacktest.WantTS(t, "draft 404", draftResp, 404, "event_not_found")
	hacktest.WantTS(t, "unknown 404", unknownResp, 404, "event_not_found")
	if string(draftResp.Body) != string(unknownResp.Body) {
		t.Fatalf("draft/unknown bodies differ: %s vs %s", draftResp.Body, unknownResp.Body)
	}

	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)

	a.ListEvent(t, slug)

	// Signed out: applications_open + fields, no signup_questions/coc_text/me.
	out := a.ApplyGet(t, slug, nil).JSON(t)
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
	out = a.ApplyGet(t, slug, &p).JSON(t)
	if out["signup_questions"] == nil || out["coc_default"] == nil {
		t.Fatalf("signed in missing questions/coc: %v", out)
	}
	if me, present := out["me"]; !present || me != nil {
		t.Fatalf("signed in non-member me: %v", out)
	}

	// Apply, then re-read as a pending applicant.
	if r := a.Apply(t, slug, p, nil); r.Status != 200 {
		t.Fatalf("apply: %d %s", r.Status, r.Body)
	}
	out = a.ApplyGet(t, slug, &p).JSON(t)
	me, _ := out["me"].(map[string]any)
	if me["role"] != "participant" || me["approval_status"] != "pending" || me["joined_via"] != "application" {
		t.Fatalf("pending me: %v", out["me"])
	}

	// Approve the pending applicant: the later archived-stage transition
	// needs at least one approved participant (archive_needs_participants).
	hacktest.WantTS(t, "approve", a.At(t, "POST", "/v1/hack/events/"+slug+"/applications/"+a.UserID(t, p)+"/decision", map[string]string{"decision": "approved"}, a.Key(org)), 200, "")

	// Switch off: applications_off.
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"applications_open": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("switch off: %d %s", r.Status, r.Body)
	}
	out = a.ApplyGet(t, slug, nil).JSON(t)
	if out["applications_open"] != false || out["closed_reason"] != "applications_off" {
		t.Fatalf("switch off preview: %v", out)
	}
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"applications_open": true}, a.Key(org)); r.Status != 200 {
		t.Fatalf("switch on: %d %s", r.Status, r.Body)
	}

	// Stage closed/judging/results/archived: signup_over. building: open.
	if r := a.SetStage(t, "/v1/hack/events/"+slug, "building", a.Key(org)); r.Status != 200 {
		t.Fatalf("building: %d %s", r.Status, r.Body)
	}
	out = a.ApplyGet(t, slug, nil).JSON(t)
	if out["applications_open"] != true || out["closed_reason"] != nil {
		t.Fatalf("building preview: %v", out)
	}
	for _, stage := range []string{"closed", "judging", "results", "archived"} {
		if r := a.SetStage(t, "/v1/hack/events/"+slug, stage, a.Key(org)); r.Status != 200 {
			t.Fatalf("%s: %d %s", stage, r.Status, r.Body)
		}
		out = a.ApplyGet(t, slug, nil).JSON(t)
		if out["applications_open"] != false || out["closed_reason"] != "signup_over" {
			t.Fatalf("%s preview: %v", stage, out)
		}
	}

	// Taken down: 404, same as unknown.
	takenSlug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, takenSlug, nil)
	a.OpenEvent(t, org, takenSlug)
	a.ListEvent(t, takenSlug)
	if r := a.At(t, "POST", "/v1/admin/hack/events/"+takenSlug+"/takedown", map[string]string{"reason": "spam"}, a.AdminH()); r.Status != 200 {
		t.Fatalf("takedown: %d %s", r.Status, r.Body)
	}
	takenResp := a.ApplyGet(t, takenSlug, nil)
	hacktest.WantTS(t, "taken down 404", takenResp, 404, "event_not_found")
	if string(takenResp.Body) != string(unknownResp.Body) {
		t.Fatalf("taken-down/unknown bodies differ: %s vs %s", takenResp.Body, unknownResp.Body)
	}
}

// TestHackApplyGetMemberApplicationsOffFieldsHidden is pre-ship review item
// 3: signup_questions/coc_text/coc_default are gated on applications being
// open right now even for a signed-in caller (security review #5) -- but
// `me` must still come back for an existing member once applications are
// switched off, so they can see their own pending status.
func TestHackApplyGetMemberApplicationsOffFieldsHidden(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "agmoorg")
	p := a.NewPerson(t, "agmomem")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	hacktest.WantTS(t, "apply", a.Apply(t, slug, p, nil), 200, "")

	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"applications_open": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("switch off: %d %s", r.Status, r.Body)
	}

	out := a.ApplyGet(t, slug, &p).JSON(t)
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
	a := newFeatApp(t)
	org := a.NewPerson(t, "aporg")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	base := "/v1/hack/events/" + slug

	// Happy path, all fields, normalised (country upper-cased, city spaces collapsed).
	happy := a.NewPerson(t, "aphappy")
	r := a.Apply(t, slug, happy, map[string]any{"city": "  Jakarta   Selatan ", "country": "id"})
	hacktest.WantTS(t, "happy path", r, 200, "")
	me := r.JSON(t)["me"].(map[string]any)
	if me["approval_status"] != "pending" {
		t.Fatalf("happy path pending: %v", me)
	}
	var joinedVia, city, country, ageRange, gender, status string
	if err := a.DB.QueryRow(`SELECT joined_via,city,country,age_range,gender,approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`,
		slug, a.UserID(t, happy)).Scan(&joinedVia, &city, &country, &ageRange, &gender, &status); err != nil {
		t.Fatal(err)
	}
	if joinedVia != "application" || city != "Jakarta Selatan" || country != "ID" || ageRange != "under_18" || gender != "woman" || status != "pending" {
		t.Fatalf("stored row: via=%q city=%q country=%q age=%q gender=%q status=%q", joinedVia, city, country, ageRange, gender, status)
	}

	// D1: approval_required=false still leaves the application pending.
	if r := a.At(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("approval off: %d %s", r.Status, r.Body)
	}
	notReq := a.NewPerson(t, "apnotreq")
	r = a.Apply(t, slug, notReq, nil)
	hacktest.WantTS(t, "apply with approval off", r, 200, "")
	if r.JSON(t)["me"].(map[string]any)["approval_status"] != "pending" {
		t.Fatalf("D1 violated: %v", r.JSON(t)["me"])
	}

	// Every optional field empty: 200, blanks stored.
	blank := a.NewPerson(t, "apblank")
	r = a.Apply(t, slug, blank, map[string]any{"city": "", "country": "", "age_range": "", "gender": ""})
	hacktest.WantTS(t, "all optional blank", r, 200, "")
	if err := a.DB.QueryRow(`SELECT city,country,age_range,gender FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`,
		slug, a.UserID(t, blank)).Scan(&city, &country, &ageRange, &gender); err != nil {
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
			who := a.NewPerson(t, "apval"+tc.name)
			hacktest.WantTS(t, tc.name, a.Apply(t, slug, who, tc.extra), 400, tc.code)
		})
	}
	hacktest.WantTS(t, "country gb ok", a.Apply(t, slug, a.NewPerson(t, "apvalgb"), map[string]any{"country": "GB"}), 200, "")

	// Required question.
	q := a.NewPerson(t, "apquestion")
	if r := a.At(t, "PUT", base+"/registration", map[string]any{"questions": []map[string]any{{"id": "why", "prompt": "Why?", "required": true}}, "approval_required": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("set questions: %d %s", r.Status, r.Body)
	}
	hacktest.WantTS(t, "answer required", a.Apply(t, slug, q, map[string]any{"answers": map[string]any{}}), 400, "answer_required")
	hacktest.WantTS(t, "answer given", a.Apply(t, slug, q, map[string]any{"answers": map[string]any{"why": "Because"}}), 200, "")
	if r := a.At(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("clear questions: %d %s", r.Status, r.Body)
	}

	// Switch off -> 409 applications_closed.
	if r := a.At(t, "PATCH", base, map[string]any{"applications_open": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("switch off: %d %s", r.Status, r.Body)
	}
	off := a.NewPerson(t, "apoff")
	hacktest.WantTS(t, "switch off", a.Apply(t, slug, off, nil), 409, "applications_closed")
	if r := a.At(t, "PATCH", base, map[string]any{"applications_open": true}, a.Key(org)); r.Status != 200 {
		t.Fatalf("switch on: %d %s", r.Status, r.Body)
	}

	// Building stage: 200. Closed stage: 409 joining_closed.
	if r := a.SetStage(t, base, "building", a.Key(org)); r.Status != 200 {
		t.Fatalf("building: %d %s", r.Status, r.Body)
	}
	building := a.NewPerson(t, "apbuilding")
	hacktest.WantTS(t, "building ok", a.Apply(t, slug, building, nil), 200, "")
	if r := a.SetStage(t, base, "closed", a.Key(org)); r.Status != 200 {
		t.Fatalf("closed: %d %s", r.Status, r.Body)
	}
	closedP := a.NewPerson(t, "apclosed")
	hacktest.WantTS(t, "closed stage", a.Apply(t, slug, closedP, nil), 409, "joining_closed")
	if r := a.SetStage(t, base, "open", a.Key(org)); r.Status != 200 {
		t.Fatalf("reopen: %d %s", r.Status, r.Body)
	}

	// Idempotent: a second POST by the same applicant changes nothing.
	r1 := a.Apply(t, slug, happy, map[string]any{"city": "Somewhere else"})
	hacktest.WantTS(t, "idempotent repeat", r1, 200, "")
	if err := a.DB.QueryRow(`SELECT city FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, happy)).Scan(&city); err != nil {
		t.Fatal(err)
	}
	if city != "Jakarta Selatan" {
		t.Fatalf("idempotent re-apply overwrote fields: %q", city)
	}

	// Approve happy, reject notReq; both re-apply as 200, unchanged status.
	hacktest.WantTS(t, "approve", a.At(t, "POST", base+"/applications/"+a.UserID(t, happy)+"/decision", map[string]string{"decision": "approved"}, a.Key(org)), 200, "")
	if _, err := a.DB.Exec(`UPDATE event_members SET city='Bandung',country='ID',age_range='under_18',gender='woman' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, notReq)); err != nil {
		t.Fatal(err)
	}
	hacktest.WantTS(t, "reject", a.At(t, "POST", base+"/applications/"+a.UserID(t, notReq)+"/decision", map[string]string{"decision": "rejected"}, a.Key(org)), 200, "")
	// Rejecting an application clears its optional profile fields; approving keeps them.
	var rc, rco, rar, rg string
	if err := a.DB.QueryRow(`SELECT city,country,age_range,gender FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, notReq)).Scan(&rc, &rco, &rar, &rg); err != nil {
		t.Fatal(err)
	}
	if rc != "" || rco != "" || rar != "" || rg != "" {
		t.Fatalf("rejected application kept profile fields: %q %q %q %q", rc, rco, rar, rg)
	}
	if err := a.DB.QueryRow(`SELECT city FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, happy)).Scan(&city); err != nil {
		t.Fatal(err)
	}
	if city != "Jakarta Selatan" {
		t.Fatalf("approved application lost its city: %q", city)
	}
	hacktest.WantTS(t, "approved re-apply", a.Apply(t, slug, happy, nil), 200, "")
	if err := a.DB.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, happy)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "approved" {
		t.Fatalf("approved member changed on re-apply: %q", status)
	}
	hacktest.WantTS(t, "rejected re-apply", a.Apply(t, slug, notReq, nil), 200, "")
	if err := a.DB.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, notReq)).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" {
		t.Fatalf("rejected applicant was re-admitted: %q", status)
	}

	// A different role (the organiser themselves) applying -> already_member.
	hacktest.WantTS(t, "organiser applying", a.Apply(t, slug, org, nil), 409, "already_member")
}

// TestHackApplyApplicationsAndPeopleFields is §8.14-15: GET .../applications
// and GET .../people carry the new fields, role gates stay the same, and a
// decision's effect on team access is unchanged.
func TestHackApplyApplicationsAndPeopleFields(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "aaforg")
	judge := a.NewPerson(t, "aafjudge")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	base := "/v1/hack/events/" + slug

	applicant := a.NewPerson(t, "aafapplicant")
	hacktest.WantTS(t, "apply", a.Apply(t, slug, applicant, map[string]any{"city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman"}), 200, "")

	apps := hacktest.JSONArr(t, a.At(t, "GET", base+"/applications", nil, a.Key(org)))
	var found map[string]any
	for _, row := range apps {
		if row["user_id"] == a.UserID(t, applicant) {
			found = row
		}
	}
	if found == nil {
		t.Fatal("application missing from the list")
	}
	if found["joined_via"] != "application" || found["city"] != "Jakarta" || found["country"] != "ID" || found["age_range"] != "under_18" || found["gender"] != "woman" {
		t.Fatalf("application row: %v", found)
	}

	people := hacktest.JSONArr(t, a.At(t, "GET", base+"/people", nil, a.Key(org)))
	var personRow map[string]any
	for _, row := range people {
		if row["user_id"] == a.UserID(t, applicant) {
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
	if r := a.At(t, "POST", "/v1/hack/judge/"+a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["judge_code"].(string), map[string]any{"accept_coc": true, "display_name": "J"}, a.Key(judge)); r.Status != 200 {
		t.Fatalf("judge join: %d %s", r.Status, r.Body)
	}
	hacktest.WantTS(t, "judge reads applications", a.At(t, "GET", base+"/applications", nil, a.Key(judge)), 404, "event_not_found")
	hacktest.WantTS(t, "applicant reads people", a.At(t, "GET", base+"/people", nil, a.Key(applicant)), 403, "approval_pending")

	// Reject the applicant: blocked from team routes, exactly as today.
	hacktest.WantTS(t, "reject", a.At(t, "POST", base+"/applications/"+a.UserID(t, applicant)+"/decision", map[string]string{"decision": "rejected"}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "rejected team denied", a.At(t, "POST", base+"/teams", map[string]string{"name": "No"}, a.Key(applicant)), 403, "approval_rejected")
}

// TestHackApplyRemoveAndReapply is §8.17: removing an applicant deletes the
// row and its fields; they can apply again, pending again.
func TestHackApplyRemoveAndReapply(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "arrorg")
	p := a.NewPerson(t, "arrmem")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	hacktest.WantTS(t, "apply", a.Apply(t, slug, p, map[string]any{"city": "Jakarta"}), 200, "")
	hacktest.WantTS(t, "remove", a.At(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.UserID(t, p), nil, a.Key(org)), 204, "")
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, p)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("removed row still present")
	}
	r := a.Apply(t, slug, p, map[string]any{"city": "Bandung"})
	hacktest.WantTS(t, "reapply", r, 200, "")
	if r.JSON(t)["me"].(map[string]any)["approval_status"] != "pending" {
		t.Fatalf("reapply status: %v", r.JSON(t)["me"])
	}
}

// TestHackApplyParticipantsCSV is §8.18 (D11): eleven columns in order, the
// right labels, formula-safe cells, pending applicants excluded.
func TestHackApplyParticipantsCSV(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "acsvorg")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	base := "/v1/hack/events/" + slug

	code := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	linked := a.NewPerson(t, "acsvlinked")
	hacktest.WantTS(t, "join", a.At(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "Link"}, a.Key(linked)), 200, "")

	applicant := a.NewPerson(t, "acsvapplicant")
	formula := a.NewPerson(t, "acsvformula")
	// Security review (Grok 7): a leading Unicode format character (U+200E)
	// must not hide a formula from csvSafe either.
	lrmFormula := a.NewPerson(t, "acsvlrmformula")
	hacktest.WantTS(t, "apply", a.Apply(t, slug, applicant, map[string]any{"city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman"}), 200, "")
	hacktest.WantTS(t, "apply formula city", a.Apply(t, slug, formula, map[string]any{"city": "=HYPERLINK(\"https://evil\")", "country": "GB", "age_range": "35_plus", "gender": "man"}), 200, "")
	hacktest.WantTS(t, "apply lrm formula city", a.Apply(t, slug, lrmFormula, map[string]any{"city": "‎=HYPERLINK(\"https://evil\")", "country": "GB", "age_range": "35_plus", "gender": "man"}), 200, "")
	pending := a.NewPerson(t, "acsvpending")
	hacktest.WantTS(t, "pending applicant", a.Apply(t, slug, pending, nil), 200, "")

	hacktest.WantTS(t, "approve applicant", a.At(t, "POST", base+"/applications/"+a.UserID(t, applicant)+"/decision", map[string]string{"decision": "approved"}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "approve formula", a.At(t, "POST", base+"/applications/"+a.UserID(t, formula)+"/decision", map[string]string{"decision": "approved"}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "approve lrm formula", a.At(t, "POST", base+"/applications/"+a.UserID(t, lrmFormula)+"/decision", map[string]string{"decision": "approved"}, a.Key(org)), 200, "")

	r := a.At(t, "GET", base+"/export/participants.csv", nil, a.Key(org))
	if r.Status != 200 {
		t.Fatalf("csv: %d %s", r.Status, r.Body)
	}
	rows, err := csv.NewReader(strings.NewReader(string(r.Body))).ReadAll()
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
	if _, present := byEmail[pending.Email]; present {
		t.Fatalf("pending applicant in CSV: %v", byEmail)
	}
	linkRow, ok := byEmail[linked.Email]
	if !ok {
		t.Fatal("link-joined participant missing from CSV")
	}
	if linkRow[6] != "" || linkRow[7] != "" || linkRow[8] != "" || linkRow[9] != "" || linkRow[10] != "join link" {
		t.Fatalf("link row: %v", linkRow)
	}
	appRow, ok := byEmail[applicant.Email]
	if !ok {
		t.Fatal("applicant missing from CSV")
	}
	if appRow[6] != "Jakarta" || appRow[7] != "Indonesia" || appRow[8] != "Under 18" || appRow[9] != "Woman or girl" || appRow[10] != "application" {
		t.Fatalf("applicant row: %v", appRow)
	}
	formulaRow, ok := byEmail[formula.Email]
	if !ok {
		t.Fatal("formula applicant missing from CSV")
	}
	if !strings.HasPrefix(formulaRow[6], "'=") {
		t.Fatalf("formula city not neutralised: %q", formulaRow[6])
	}
	if formulaRow[7] != "United Kingdom" || formulaRow[8] != "35+" || formulaRow[9] != "Man or boy" {
		t.Fatalf("formula row other fields: %v", formulaRow)
	}
	lrmRow, ok := byEmail[lrmFormula.Email]
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
	a := newFeatApp(t)
	l := productconfig.DefaultLimits()
	l.EventMaxActivePerOrganiser = 10
	l.EventCreatePerDay = 10
	hacktest.SetLimits(a.App, l)
	org := a.NewPerson(t, "adporg")
	openSlug, closedSwitchSlug, unlistedSlug := hacktest.UniqueSlug(), hacktest.UniqueSlug(), hacktest.UniqueSlug()
	for _, slug := range []string{openSlug, closedSwitchSlug, unlistedSlug} {
		if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
			t.Fatalf("create %s: %d %s", slug, r.Status, r.Body)
		}
		a.OpenEvent(t, org, slug)
	}
	for _, slug := range []string{openSlug, closedSwitchSlug} {
		if r := a.At(t, "PATCH", "/v1/hack/events/"+slug+"/directory", map[string]any{"listed": true}, a.Key(org)); r.Status != 200 {
			t.Fatalf("list %s: %d %s", slug, r.Status, r.Body)
		}
	}
	if r := a.At(t, "PATCH", "/v1/hack/events/"+closedSwitchSlug, map[string]any{"applications_open": false}, a.Key(org)); r.Status != 200 {
		t.Fatalf("switch off: %d %s", r.Status, r.Body)
	}

	groups := a.At(t, "GET", "/v1/hack/directory", nil, nil).JSON(t)
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

	pub := a.At(t, "GET", "/v1/hack/events/"+openSlug+"/public", nil, nil).JSON(t)
	if pub["applications_open"] != true || pub["apply_url"] == nil {
		t.Fatalf("public json: %v", pub)
	}
	if _, present := pub["approval_required"]; present {
		t.Fatalf("approval_required leaked into public json: %v", pub)
	}
	pubOff := a.At(t, "GET", "/v1/hack/events/"+closedSwitchSlug+"/public", nil, nil).JSON(t)
	if pubOff["applications_open"] != false {
		t.Fatalf("public json switched off: %v", pubOff)
	}
}

// TestHackApplyPatchEventToggle is §8.21: PATCH applications_open, role and
// stage gates.
func TestHackApplyPatchEventToggle(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "apetorg")
	participant := a.NewPerson(t, "apetpart")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	base := "/v1/hack/events/" + slug

	hacktest.WantTS(t, "organiser off", a.At(t, "PATCH", base, map[string]any{"applications_open": false}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "organiser on", a.At(t, "PATCH", base, map[string]any{"applications_open": true}, a.Key(org)), 200, "")

	code := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	hacktest.WantTS(t, "participant joins", a.At(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, a.Key(participant)), 200, "")
	hacktest.WantTS(t, "participant denied", a.At(t, "PATCH", base, map[string]any{"applications_open": false}, a.Key(participant)), 404, "event_not_found")

	hacktest.WantTS(t, "stage archived", a.SetStage(t, base, "archived", a.Key(org)), 200, "")
	hacktest.WantTS(t, "ended event", a.At(t, "PATCH", base, map[string]any{"applications_open": false}, a.Key(org)), 409, "event_closed")

	// The platform admin reading an event it is not in is read-only: a
	// write (even on a live event) is refused the same as any non-member.
	liveSlug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, liveSlug, nil)
	a.OpenEvent(t, org, liveSlug)
	a.ListEvent(t, liveSlug)
	hacktest.WantTS(t, "admin view read-only", a.At(t, "PATCH", "/v1/hack/events/"+liveSlug, map[string]any{"applications_open": false}, a.AdminH()), 404, "event_not_found")
}

func TestHackJoinJudgeAndCodes(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "jo")
	p1 := a.NewPerson(t, "j1")
	p2 := a.NewPerson(t, "j2")
	slug := hacktest.UniqueSlug()
	cr := a.CreateEvent(t, org, slug, nil)
	if cr.Status != 201 {
		t.Fatalf("create: %d %s", cr.Status, cr.Body)
	}
	orgView := cr.JSON(t)["organiser"].(map[string]any)
	join, judge := orgView["join_code"].(string), orgView["judge_code"].(string)

	r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "A"}, a.Key(p1))
	if r.Status != 409 || r.JSON(t)["code"] != "joining_closed" {
		t.Fatalf("draft join: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"display_name": "A"}, a.Key(p1))
	if r.Status != 400 || r.JSON(t)["code"] != "coc_required" {
		t.Fatalf("coc: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", "/v1/hack/join/not-a-code", nil, nil)
	if r.Status != 404 || r.JSON(t)["code"] != "invalid_code" {
		t.Fatalf("bad code: %d %s", r.Status, r.Body)
	}

	a.OpenEvent(t, org, slug)
	info := a.At(t, "GET", "/v1/hack/join/"+strings.ToUpper(join[:4])+"-"+join[4:], nil, nil)
	if info.Status != 200 || info.JSON(t)["joinable"] != true || info.JSON(t)["role"] != "participant" {
		t.Fatalf("join get: %d %s", info.Status, info.Body)
	}
	r = a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.Key(p1))
	if r.Status != 200 || r.JSON(t)["role"] != "participant" {
		t.Fatalf("join: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.Key(p1))
	if r.Status != 200 {
		t.Fatalf("rejoin: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/judge/"+judge, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.Key(p1))
	if r.Status != 409 || r.JSON(t)["code"] != "already_member" {
		t.Fatalf("already: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "POST", "/v1/hack/judge/"+judge, map[string]any{"accept_coc": true, "display_name": "Jay"}, a.Key(p2))
	if r.Status != 200 || r.JSON(t)["role"] != "judge" {
		t.Fatalf("judge: %d %s", r.Status, r.Body)
	}

	oldJoin := join
	regen := a.At(t, "POST", "/v1/hack/events/"+slug+"/codes/join", nil, a.Key(org))
	if regen.Status != 200 || regen.JSON(t)["code"] == oldJoin {
		t.Fatalf("regen: %d %s", regen.Status, regen.Body)
	}
	r = a.At(t, "GET", "/v1/hack/join/"+oldJoin, nil, nil)
	if r.Status != 404 || r.JSON(t)["code"] != "invalid_code" {
		t.Fatalf("old code: %d %s", r.Status, r.Body)
	}

	// Results is offered since M3; moving an event straight to it is allowed
	// (there is no enforced stage order), and judging closes to new joiners.
	if r := a.SetStage(t, "/v1/hack/events/"+slug, "results", a.Key(org)); r.Status != 200 {
		t.Fatalf("results offered: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/judge/"+judge, map[string]any{"accept_coc": true, "display_name": "New"}, a.Key(a.NewPerson(t, "j3")))
	if r.Status != 409 || r.JSON(t)["code"] != "judging_closed" {
		t.Fatalf("judging closed: %d %s", r.Status, r.Body)
	}

}

// TestHackDeleteFormerJudgeScoresRaceTakenDown is a regression test: a
// takedown that commits while delete-former-judge-scores is waiting on the
// event lock must stop the delete, not just the pre-transaction loadMember
// check it used to rely on alone.
func TestHackDeleteFormerJudgeScoresRaceTakenDown(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvdftd-org")
	p1 := a.NewPerson(t, "lvdftd-p1")
	judge := a.NewPerson(t, "lvdftd-j")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "Delendum")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.JoinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	hacktest.WantTS(t, "score", a.PutScores(t, "/v1/hack/events/"+slug, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.Key(judge)), 200, "")
	hacktest.WantTS(t, "judge leaves", a.Leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	eventID := a.EventID(t, slug)
	judgeID := a.UserID(t, judge)

	lockTx, err := a.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	deleteDone := make(chan hacktest.Resp, 1)
	go func() {
		deleteDone <- a.At(t, "DELETE", "/v1/hack/events/"+slug+"/judging/former-judges/"+judgeID, map[string]any{"confirm": slug}, a.Key(org))
	}()
	hacktest.WaitBlocked()

	if _, err := lockTx.Exec(`UPDATE events SET taken_down_at = now(), taken_down_reason = $2 WHERE id = $1`, eventID, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	r := <-deleteDone
	hacktest.WantTS(t, "delete-scores after takedown wins the race", r, 403, "event_taken_down")

	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("scores must survive a delete that a takedown beat to the lock")
	}
}

// TestHackRemoveCoOrganiserDeletesScoresAndCleansMail: an organiser who
// scored, removed by another organiser (not a voluntary leave), must have
// their scores deleted outright -- TestHackOrganisersCanJudgeAndRemoved
// OrganiserScoresStopCounting (hack_judging_test.go) already establishes "a
// removed organiser's scores stop counting" as the intended behaviour here,
// unlike a voluntary leave's frozen-but-kept scores -- and get the same
// unsent-mail cleanup a self-leave gets, which the old single-statement
// DELETE skipped entirely.
func TestHackRemoveCoOrganiserDeletesScoresAndCleansMail(t *testing.T) {
	a := newFeatApp(t)
	creator := a.NewPerson(t, "lvrco-cr")
	co := a.NewPerson(t, "lvrco-co")
	p1 := a.NewPerson(t, "lvrco-p1")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.At(t, "POST", base+"/organiser-invite", nil, a.Key(creator))
	hacktest.WantTS(t, "invite", inviteR, 201, "")
	code := inviteR.JSON(t)["url"].(string)
	hacktest.WantTS(t, "accept", a.At(t, "POST", "/v1/hack/organiser/"+code[len(code)-32:], map[string]any{"accept_coc": true, "display_name": "Co"}, a.Key(co)), 200, "")
	a.OpenEvent(t, creator, slug)
	join, _ := a.JoinCodes(t, creator, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "Delendum")
	a.GiveSubmittedEntry(t, slug, teamID)
	hacktest.WantTS(t, "select organiser judges", a.At(t, "PATCH", base+"/judging/settings", map[string]any{"include_organisers": true}, a.Key(creator)), 200, "")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	hacktest.WantTS(t, "co scores as organiser", a.PutScores(t, "/v1/hack/events/"+slug, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 4}}}, a.Key(co)), 200, "")

	// A queued, unsent announcement for "co" to clean up, same assertion
	// shape LeaveMembership's own tests use elsewhere.
	eventID := a.EventID(t, slug)
	coID := a.UserID(t, co)
	postAnn := a.At(t, "POST", base+"/announcements", map[string]any{"title": "Hi", "body": "Body", "email_participants": false}, a.Key(creator))
	hacktest.WantTS(t, "post announcement", postAnn, 201, "")
	annID := postAnn.JSON(t)["announcement"].(map[string]any)["id"].(string)
	if _, err := a.DB.Exec(`INSERT INTO event_announcement_deliveries (announcement_id, user_id, recipient) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, annID, coID, a.EmailOf(t, co)); err != nil {
		t.Fatal(err)
	}

	hacktest.WantTS(t, "remove co-organiser", a.At(t, "DELETE", base+"/organisers/"+coID, nil, a.Key(creator)), 204, "")

	var scoreRows int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, coID).Scan(&scoreRows); err != nil {
		t.Fatal(err)
	}
	if scoreRows != 0 {
		t.Fatal("a removed organiser's scores must be deleted outright, stopping them counting")
	}
	var formerRows int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, coID).Scan(&formerRows); err != nil {
		t.Fatal(err)
	}
	if formerRows != 0 {
		t.Fatal("a removed organiser gets no former-judge record -- there is nothing left to freeze")
	}
	var unsent int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_announcement_deliveries WHERE user_id=$1 AND sent_at IS NULL`, coID).Scan(&unsent); err != nil {
		t.Fatal(err)
	}
	if unsent != 0 {
		t.Fatal("removing a co-organiser must clean their unsent mail, same as leave")
	}
}

// TestHackRemovePersonFreezesJudgeScores: an organiser removing a judge (not
// the judge leaving voluntarily) must also freeze that judge's scores via
// the former-judge record.
func TestHackRemovePersonFreezesJudgeScores(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvrpj-org")
	p1 := a.NewPerson(t, "lvrpj-p1")
	judge := a.NewPerson(t, "lvrpj-j")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "Delendum")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.JoinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	hacktest.WantTS(t, "score", a.PutScores(t, "/v1/hack/events/"+slug, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.Key(judge)), 200, "")

	eventID := a.EventID(t, slug)
	judgeID := a.UserID(t, judge)
	hacktest.WantTS(t, "organiser removes judge", a.At(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+judgeID, nil, a.Key(org)), 204, "")

	var formerRole string
	if err := a.DB.QueryRow(`SELECT role FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(&formerRole); err != nil {
		t.Fatalf("expected a former-judge record for the removed judge's scores: %v", err)
	}
	if formerRole != "judge" {
		t.Fatalf("former-judge role: got %q, want judge", formerRole)
	}
}

// TestHackLeaveDoesNotResurrectNonCountingScore: a score that did not count
// at the moment the judge left must not start counting afterwards.
// RecordFormerJudge used to freeze every team event_scores had a row for,
// with no eligibility check at all, so a score cast under "open" mode kept
// counting under a later, narrower mode (with no assignment) even after the
// scoring judge left (ship-blocker B2). The fix intersects the scored teams
// with EligibleJudgeTeamsIgnoringConflicts, read at the moment of leaving.
func TestHackLeaveDoesNotResurrectNonCountingScore(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvresorg")
	p1 := a.NewPerson(t, "lvresp")
	judge := a.NewPerson(t, "lvresj")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "TeamA")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.JoinJudge(t, judgeCode, judge, "Judge")
	base := "/v1/hack/events/" + slug
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	// Open mode (the default): the judge is eligible for every team right
	// now, so this score genuinely counts.
	hacktest.WantTS(t, "score", a.PutScores(t, base, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.Key(judge)), 200, "")
	// Manual mode with no assignment at all: the judge is no longer
	// eligible for this team, so the score they already cast stops
	// counting right now, before they ever leave.
	// SIM-416: judging setup changes only outside Judging.
	if _, err := a.DB.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	hacktest.WantTS(t, "set manual", a.At(t, "PATCH", base+"/judging/settings", map[string]any{"assignment_mode": "manual"}, a.Key(org)), 200, "")

	eventID := a.EventID(t, slug)
	coverage := func() int {
		byTeam, _, err := entries.New(a.DB, nil).ScoreCoverage(context.Background(), eventID)
		if err != nil {
			t.Fatal(err)
		}
		return byTeam[teamID]
	}
	before := coverage()
	if before != 0 {
		t.Fatalf("the score must already not be counting under manual mode with no assignment: coverage=%d", before)
	}

	hacktest.WantTS(t, "judge leaves", a.Leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	if after := coverage(); after != before {
		t.Fatalf("BUG: leaving changed what counts: %d -> %d", before, after)
	}
}

func TestHackDeleteFormerJudgeScoresRequiresConfirm(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvdfc-org")
	p1 := a.NewPerson(t, "lvdfc-p1")
	judge := a.NewPerson(t, "lvdfc-j")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "Delendum")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.JoinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	hacktest.WantTS(t, "score", a.PutScores(t, "/v1/hack/events/"+slug, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.Key(judge)), 200, "")
	hacktest.WantTS(t, "leave", a.Leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	judgeID := a.UserID(t, judge)
	route := "/v1/hack/events/" + slug + "/judging/former-judges/" + judgeID
	hacktest.WantTS(t, "no confirm field", a.At(t, "DELETE", route, map[string]any{}, a.Key(org)), 409, "confirm_required")
	hacktest.WantTS(t, "wrong confirm", a.At(t, "DELETE", route, map[string]any{"confirm": "not-the-slug"}, a.Key(org)), 409, "confirm_required")

	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_former_judges WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("a missing or wrong confirm must not delete anything")
	}

	hacktest.WantTS(t, "correct confirm", a.At(t, "DELETE", route, map[string]any{"confirm": slug}, a.Key(org)), 204, "")
}

func TestHackLeaveParticipantWithTeammates(t *testing.T) {
	a := newFeatApp(t)
	sink := &hacktest.MailSink{}
	a.hack.SetMailer(sink)
	org := a.NewPerson(t, "lv1org")
	p1, p2 := a.NewPerson(t, "lv1p1"), a.NewPerson(t, "lv1p2")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Ayu")
	a.JoinParticipant(t, join, p2, "Nadia")
	teamID, code := a.CreateTeamAndJoin(t, slug, p1, "SheBuilds")
	a.JoinTeamByCode(t, slug, code, p2)

	// An unsent announcement delivery queued for p1 must be dropped when
	// they leave; p2's stays.
	ann := a.At(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Hi", "body": "Welcome", "email_participants": true}, a.Key(org))
	hacktest.WantTS(t, "announcement", ann, 201, "")

	p1ID, p2ID := a.UserID(t, p1), a.UserID(t, p2)
	countPending := func(userID string) int {
		var n int
		if err := a.DB.QueryRow(`SELECT count(*) FROM event_announcement_deliveries d USING event_announcements an WHERE d.announcement_id=an.id AND an.event_id=$1 AND d.user_id=$2 AND d.sent_at IS NULL`, a.EventID(t, slug), userID).Scan(&n); err != nil {
			// USING in a SELECT isn't valid; fall back to a join.
			_ = a.DB.QueryRow(`SELECT count(*) FROM event_announcement_deliveries d JOIN event_announcements an ON an.id=d.announcement_id WHERE an.event_id=$1 AND d.user_id=$2 AND d.sent_at IS NULL`, a.EventID(t, slug), userID).Scan(&n)
		}
		return n
	}
	if countPending(p1ID) == 0 {
		t.Fatal("expected a pending delivery for p1 before leaving")
	}

	prev := a.PreviewLeave(t, slug, p1).JSON(t)
	if prev["case"] != "participant" || prev["allowed"] != true {
		t.Fatalf("preview: %v", prev)
	}
	team := prev["team"].(map[string]any)
	if team["slug"] != "shebuilds" && team["name"] != "SheBuilds" {
		t.Fatalf("preview team: %v", team)
	}
	if team["deleted_on_leave"] != false || team["others_count"] != float64(1) {
		t.Fatalf("preview team others: %v", team)
	}

	out := a.Leave(t, slug, p1, map[string]any{"confirm": slug}).JSON(t)
	if out["left"] != true || out["case"] != "participant" || out["team_deleted"] != false || out["event"] != "kept" {
		t.Fatalf("leave response: %v", out)
	}

	if a.IsMember(t, slug, p1ID) {
		t.Fatal("p1 still a member")
	}
	if !a.IsMember(t, slug, p2ID) {
		t.Fatal("p2 should remain a member")
	}
	// Team, its code and its membership for p2 survive.
	var stillTeam bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if !stillTeam {
		t.Fatal("team should survive when a teammate remains")
	}
	var liveCode string
	if err := a.DB.QueryRow(`SELECT code FROM event_teams WHERE id=$1`, teamID).Scan(&liveCode); err != nil {
		t.Fatal(err)
	}
	if liveCode != code {
		t.Fatalf("team code changed: got %s want %s", liveCode, code)
	}

	if countPending(p1ID) != 0 {
		t.Fatal("p1's unsent announcement delivery should be gone")
	}
	if countPending(p2ID) == 0 {
		t.Fatal("p2's unsent announcement delivery must survive")
	}

	// Leaving again: no longer a member, 404.
	hacktest.WantTS(t, "leave again", a.Leave(t, slug, p1, map[string]any{"confirm": slug}), 404, "event_not_found")
}

func TestHackLeaveFrozenLoneMemberKeepsTeamEntryAndScores(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lv4org")
	p1 := a.NewPerson(t, "lv4p1")
	p2 := a.NewPerson(t, "lv4p2")
	judge := a.NewPerson(t, "lv4j1")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Solo")
	a.JoinParticipant(t, join, p2, "Other")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "LoneFrozenTeam")
	otherTeamID, _ := a.CreateTeamAndJoin(t, slug, p2, "OtherTeam")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.GiveSubmittedEntry(t, slug, otherTeamID)
	a.JoinJudge(t, judgeCode, judge, "Judge One")
	base := "/v1/hack/events/" + slug
	// Through the real stage-transition route, not a raw SQL update: setStage
	// forces submission_deadline to now() the instant the stage becomes
	// closed (hack.go), which is what keeps TeamKeepableSQL/TeamFrozenForLeave
	// in agreement in production. A raw UPDATE events SET stage='closed'
	// would leave the deadline NULL and wrongly let deleteTeamIfEmpty through.
	hacktest.WantTS(t, "close", a.SetStage(t, base, "closed", a.Key(org)), 200, "")
	hacktest.WantTS(t, "start judging", a.SetStage(t, base, "judging", a.Key(org)), 200, "")
	criterion := a.FirstCriterionID(t, slug)
	score := func(teamID string, points int) {
		hacktest.WantTS(t, "score", a.PutScores(t, base, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": points}}}, a.Key(judge)), 200, "")
	}
	score(teamID, 4)
	score(otherTeamID, 2)

	snapshot := func() map[string]map[string]any {
		pub := a.At(t, "POST", base+"/results/publish", map[string]any{}, a.Key(org))
		if pub.Status != 200 {
			t.Fatalf("publish: %d %s", pub.Status, pub.Body)
		}
		out := map[string]map[string]any{}
		for _, raw := range pub.JSON(t)["results"].([]any) {
			row := raw.(map[string]any)
			out[row["team_id"].(string)] = row
		}
		return out
	}

	prev := a.PreviewLeave(t, slug, p1).JSON(t)
	team := prev["team"].(map[string]any)
	if team["frozen"] != true || team["deleted_on_leave"] != false || team["others_count"] != float64(0) {
		t.Fatalf("frozen lone preview: %v", team)
	}

	before := snapshot()
	out := a.Leave(t, slug, p1, map[string]any{"confirm": slug}).JSON(t)
	if out["left"] != true || out["team_deleted"] != false {
		t.Fatalf("frozen lone leave: %v", out)
	}
	var stillTeam bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if !stillTeam {
		t.Fatal("lone team must survive once frozen, even when it's emptied")
	}
	var kept bool
	if err := a.DB.QueryRow(`SELECT kept_after_leave FROM event_teams WHERE id=$1`, teamID).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if !kept {
		t.Fatal("self-leave emptying a kept team must set kept_after_leave, so judging/results keep listing it")
	}
	var members int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_members WHERE team_id=$1`, teamID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("team should now have no members, got %d", members)
	}
	after := snapshot()
	for _, id := range []string{teamID, otherTeamID} {
		b, af := before[id], after[id]
		if b == nil || af == nil {
			t.Fatalf("team %s missing from a snapshot: before=%v after=%v", id, b, af)
		}
		if b["total"] != af["total"] || b["rank"] != af["rank"] || b["judges_scored"] != af["judges_scored"] {
			t.Fatalf("team %s changed across the leave: before=%v after=%v", id, b, af)
		}
	}
}

// Codex review, SIM-340, P2: a team an organiser emptied with Remove person,
// after the deadline, must stay excluded from judging, results and
// dashboards exactly as it always was -- only a team self-leave itself kept
// gets kept_after_leave. ListTeamsWithMembers must not retroactively
// include every memberless frozen team, or republishing results restores a
// team nobody ever asked to keep, moving ranks and ties with no leave
// involved.
func TestHackRemovePersonAfterDeadlineStaysExcludedFromJudging(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvrmorg")
	p1 := a.NewPerson(t, "lvrmp1")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Solo")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "RemovedTeam")

	hacktest.WantTS(t, "close", a.SetStage(t, "/v1/hack/events/"+slug, "closed", a.Key(org)), 200, "")

	p1ID := a.UserID(t, p1)
	hacktest.WantTS(t, "remove", a.At(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+p1ID, nil, a.Key(org)), 204, "")

	var stillTeam bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if !stillTeam {
		t.Fatal("frozen team must survive Remove person too (deleteTeamIfEmpty's own protection, unchanged)")
	}
	var kept bool
	if err := a.DB.QueryRow(`SELECT kept_after_leave FROM event_teams WHERE id=$1`, teamID).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept {
		t.Fatal("Remove person must never set kept_after_leave -- only self-leave does")
	}

	dash := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	for _, raw := range dash["teams"].([]any) {
		row := raw.(map[string]any)
		if row["team_id"] == teamID {
			t.Fatalf("team emptied by Remove person must stay excluded from the judging dashboard: %v", row)
		}
	}
}

func TestHackLeaveJudgeKeepsScoresAndRejoinClears(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lv7org")
	p1 := a.NewPerson(t, "lv7p1")
	judge := a.NewPerson(t, "lv7j1")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "ScoredTeam")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.JoinJudge(t, judgeCode, judge, "Judge One")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}

	criterion := a.FirstCriterionID(t, slug)
	score := a.PutScores(t, "/v1/hack/events/"+slug, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 4}}}, a.Key(judge))
	hacktest.WantTS(t, "score", score, 200, "")

	dashBefore := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	teamsBefore := dashBefore["teams"].([]any)[0].(map[string]any)
	if teamsBefore["judges_scored"] != float64(1) {
		t.Fatalf("coverage before leave: %v", teamsBefore)
	}

	prev := a.PreviewLeave(t, slug, judge).JSON(t)
	if prev["case"] != "judge" || prev["scored_teams"] != float64(1) {
		t.Fatalf("judge preview: %v", prev)
	}
	out := a.Leave(t, slug, judge, map[string]any{"confirm": slug}).JSON(t)
	if out["case"] != "judge" || out["left"] != true {
		t.Fatalf("judge leave: %v", out)
	}

	dashAfter := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	teamsAfter := dashAfter["teams"].([]any)[0].(map[string]any)
	if teamsAfter["judges_scored"] != float64(1) {
		t.Fatalf("coverage after leave should be unchanged: %v", teamsAfter)
	}
	former := dashAfter["former_judges"].([]any)
	if len(former) != 1 {
		t.Fatalf("former_judges: %v", dashAfter["former_judges"])
	}
	formerRow := former[0].(map[string]any)
	if formerRow["counted_teams"] != float64(1) || formerRow["role"] != "judge" {
		t.Fatalf("former judge row: %v", formerRow)
	}

	// Rejoin with the judge link: the former-judge record clears and their
	// score counts as a member's again (same coverage, no former_judges row).
	a.JoinJudge(t, judgeCode, judge, "Judge One")
	dashRejoined := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	if len(dashRejoined["former_judges"].([]any)) != 0 {
		t.Fatalf("former_judges should clear on rejoin: %v", dashRejoined["former_judges"])
	}
	teamsRejoined := dashRejoined["teams"].([]any)[0].(map[string]any)
	if teamsRejoined["judges_scored"] != float64(1) {
		t.Fatalf("coverage after rejoin: %v", teamsRejoined)
	}
}

func TestHackLeaveCoOrganiserAndCreatorHandover(t *testing.T) {
	a := newFeatApp(t)
	creator := a.NewPerson(t, "lv10cr")
	co := a.NewPerson(t, "lv10co")
	part := a.NewPerson(t, "lv10pa")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.At(t, "POST", base+"/organiser-invite", nil, a.Key(creator))
	hacktest.WantTS(t, "invite", inviteR, 201, "")
	code := inviteR.JSON(t)["url"].(string)
	// code is a full URL; reuse the accept path convention from administration tests.
	acceptPath := "/v1/hack/organiser/" + code[len(code)-32:]
	hacktest.WantTS(t, "accept", a.At(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "Co"}, a.Key(co)), 200, "")

	a.OpenEvent(t, creator, slug)
	join, _ := a.JoinCodes(t, creator, slug)
	a.JoinParticipant(t, join, part, "Part")

	// Co-organiser leaves: creator unchanged, co's outstanding invite (if
	// any) stops working. Issue one more invite from co, then have co leave.
	coInviteR := a.At(t, "POST", base+"/organiser-invite", nil, a.Key(co))
	hacktest.WantTS(t, "co invite", coInviteR, 201, "")
	coInviteCode := coInviteR.JSON(t)["url"].(string)
	coAcceptPath := "/v1/hack/organiser/" + coInviteCode[len(coInviteCode)-32:]

	prev := a.PreviewLeave(t, slug, co).JSON(t)
	if prev["case"] != "co_organiser" {
		t.Fatalf("co preview: %v", prev)
	}
	out := a.Leave(t, slug, co, map[string]any{"confirm": slug}).JSON(t)
	if out["case"] != "co_organiser" || out["left"] != true {
		t.Fatalf("co leave: %v", out)
	}
	var createdBy string
	if err := a.DB.QueryRow(`SELECT created_by FROM events WHERE slug=$1`, slug).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != a.UserID(t, creator) {
		t.Fatal("creator must stay unchanged")
	}
	hacktest.WantTS(t, "stale invite dead", a.At(t, "GET", coAcceptPath, nil, nil), 404, "invalid_code")

	// Bring in a second organiser so the creator is "creator" (an organiser
	// with others), not "last_organiser", for the rest of this test.
	invite2 := a.At(t, "POST", base+"/organiser-invite", nil, a.Key(creator))
	hacktest.WantTS(t, "invite2", invite2, 201, "")
	code2 := invite2.JSON(t)["url"].(string)
	accept2 := "/v1/hack/organiser/" + code2[len(code2)-32:]
	co2 := a.NewPerson(t, "lv11co2")
	hacktest.WantTS(t, "accept2", a.At(t, "POST", accept2, map[string]any{"accept_coc": true, "display_name": "Co2"}, a.Key(co2)), 200, "")

	// Creator: no successor.
	hacktest.WantTS(t, "no successor", a.Leave(t, slug, creator, map[string]any{"confirm": slug}), 409, "successor_required")

	// Invalid successor: a participant, a random uuid.
	hacktest.WantTS(t, "invalid successor participant", a.Leave(t, slug, creator, map[string]any{"confirm": slug, "successor_user_id": a.UserID(t, part)}), 400, "invalid_successor")
	hacktest.WantTS(t, "invalid successor uuid", a.Leave(t, slug, creator, map[string]any{"confirm": slug, "successor_user_id": "00000000-0000-0000-0000-000000000000"}), 400, "invalid_successor")

	// Valid handover.
	out = a.Leave(t, slug, creator, map[string]any{"confirm": slug, "successor_user_id": a.UserID(t, co2)}).JSON(t)
	if out["case"] != "creator" || out["left"] != true {
		t.Fatalf("creator leave: %v", out)
	}
	successor := out["successor"].(map[string]any)
	if successor["display_name"] != "Co2" {
		t.Fatalf("successor: %v", successor)
	}
	if err := a.DB.QueryRow(`SELECT created_by FROM events WHERE slug=$1`, slug).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != a.UserID(t, co2) {
		t.Fatal("created_by must move to the successor")
	}
	if a.IsMember(t, slug, a.UserID(t, creator)) {
		t.Fatal("former creator should be gone")
	}
}

func TestHackLeaveLastOrganiserEnd(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lv12org")
	part := a.NewPerson(t, "lv12pa")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")

	prev := a.PreviewLeave(t, slug, org).JSON(t)
	if prev["case"] != "last_organiser" {
		t.Fatalf("preview: %v", prev)
	}
	closeOpts := prev["close_options"].(map[string]any)
	end := closeOpts["end"].(map[string]any)
	if end["allowed"] != false || end["blocked"] != "archive_needs_participants" {
		t.Fatalf("close_options before anyone joined: %v", closeOpts)
	}

	hacktest.WantTS(t, "no close", a.Leave(t, slug, org, map[string]any{"confirm": slug}), 409, "last_organiser")
	hacktest.WantTS(t, "end without participants", a.Leave(t, slug, org, map[string]any{"confirm": slug, "close": "end"}), 409, "archive_needs_participants")

	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, part, "Part")

	out := a.Leave(t, slug, org, map[string]any{"confirm": slug, "close": "end"}).JSON(t)
	if out["event"] != "ended" || out["left"] != true {
		t.Fatalf("end leave: %v", out)
	}
	var stage string
	var closedAt any
	if err := a.DB.QueryRow(`SELECT stage, closed_at FROM events WHERE slug=$1`, slug).Scan(&stage, &closedAt); err != nil {
		t.Fatal(err)
	}
	if stage != "archived" || closedAt == nil {
		t.Fatalf("stage/closed_at: %v %v", stage, closedAt)
	}
	if a.IsMember(t, slug, a.UserID(t, org)) {
		t.Fatal("ended organiser should be gone")
	}
	// The public page still serves.
	hacktest.WantTS(t, "public page", a.At(t, "GET", "/v1/hack/events/"+slug+"/public", nil, nil), 200, "")
}

func TestHackLeaveStaleFieldsAndConfirm(t *testing.T) {
	a := newFeatApp(t)
	creator := a.NewPerson(t, "lv13cr")
	co := a.NewPerson(t, "lv13co")
	part := a.NewPerson(t, "lv13pa")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.At(t, "POST", base+"/organiser-invite", nil, a.Key(creator))
	code := inviteR.JSON(t)["url"].(string)
	acceptPath := "/v1/hack/organiser/" + code[len(code)-32:]
	hacktest.WantTS(t, "accept", a.At(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "Co"}, a.Key(co)), 200, "")
	a.OpenEvent(t, creator, slug)
	join, _ := a.JoinCodes(t, creator, slug)
	a.JoinParticipant(t, join, part, "Part")

	// A co-organiser sending close, or a participant sending successor_user_id.
	hacktest.WantTS(t, "co sends close", a.Leave(t, slug, co, map[string]any{"confirm": slug, "close": "end"}), 409, "leave_state_changed")
	hacktest.WantTS(t, "participant sends successor", a.Leave(t, slug, part, map[string]any{"confirm": slug, "successor_user_id": a.UserID(t, co)}), 409, "leave_state_changed")

	// Confirm missing or wrong.
	hacktest.WantTS(t, "confirm missing", a.Leave(t, slug, part, map[string]any{}), 409, "leave_needs_confirm")
	hacktest.WantTS(t, "confirm wrong", a.Leave(t, slug, part, map[string]any{"confirm": "not-the-slug"}), 409, "leave_needs_confirm")
	if !a.IsMember(t, slug, a.UserID(t, part)) {
		t.Fatal("nothing should change on a bad confirm")
	}
}

func TestHackLeaveEndedTakenDownAndNotMember(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lv15org")
	outsider := a.NewPerson(t, "lv15out")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	part := a.NewPerson(t, "lv15pa")
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, part, "Part")
	hacktest.WantTS(t, "archive", a.SetStage(t, "/v1/hack/events/"+slug, "archived", a.Key(org)), 200, "")
	hacktest.WantTS(t, "ended participant", a.Leave(t, slug, part, map[string]any{"confirm": slug}), 409, "event_closed")

	takenSlug := hacktest.UniqueSlug()
	tOrg := a.NewPerson(t, "lv15torg")
	hacktest.WantTS(t, "create2", a.CreateEvent(t, tOrg, takenSlug, nil), 201, "")
	hacktest.WantTS(t, "takedown", a.At(t, "POST", "/v1/admin/hack/events/"+takenSlug+"/takedown", map[string]string{"reason": "spam"}, a.AdminH()), 200, "")
	hacktest.WantTS(t, "taken down organiser", a.Leave(t, takenSlug, tOrg, map[string]any{"confirm": takenSlug}), 403, "event_taken_down")

	hacktest.WantTS(t, "not a member", a.Leave(t, slug, outsider, map[string]any{"confirm": slug}), 404, "event_not_found")
	hacktest.WantTS(t, "unknown slug", a.Leave(t, "no-such-"+hacktest.UniqueSlug(), org, map[string]any{"confirm": "x"}), 404, "event_not_found")
	hacktest.WantTS(t, "admin not a member", a.Leave(t, slug, hacktest.Person{Key: a.Admin}, map[string]any{"confirm": slug}), 404, "event_not_found")
}

func TestHackDeleteFormerJudgeScoresRoute(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lv20org")
	p1 := a.NewPerson(t, "lv20p1")
	judge := a.NewPerson(t, "lv20j1")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "Delendum")
	a.GiveSubmittedEntry(t, slug, teamID)
	a.JoinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	hacktest.WantTS(t, "score", a.PutScores(t, "/v1/hack/events/"+slug, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.Key(judge)), 200, "")
	hacktest.WantTS(t, "leave", a.Leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	formerUserID := a.UserID(t, judge)
	route := "/v1/hack/events/" + slug + "/judging/former-judges/"
	confirmBody := map[string]any{"confirm": slug}
	hacktest.WantTS(t, "unknown former judge", a.At(t, "DELETE", route+"00000000-0000-0000-0000-000000000000", confirmBody, a.Key(org)), 404, "former_judge_not_found")
	hacktest.WantTS(t, "not organiser", a.At(t, "DELETE", route+formerUserID, nil, a.Key(p1)), 404, "event_not_found")
	hacktest.WantTS(t, "delete", a.At(t, "DELETE", route+formerUserID, confirmBody, a.Key(org)), 204, "")

	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_scores WHERE judge_id=$1`, formerUserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("scores should be deleted")
	}
	dash := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	if len(dash["former_judges"].([]any)) != 0 {
		t.Fatalf("former_judges should be empty: %v", dash["former_judges"])
	}
	teamsAfter := dash["teams"].([]any)[0].(map[string]any)
	if teamsAfter["judges_scored"] != float64(0) {
		t.Fatalf("coverage should drop: %v", teamsAfter)
	}
	// Second delete: already gone.
	hacktest.WantTS(t, "delete again", a.At(t, "DELETE", route+formerUserID, confirmBody, a.Key(org)), 404, "former_judge_not_found")
}

// Turning approval off and admitting pending admits join-link sign-ups only;
// a formal application stays pending.
func TestRhpAdmitPendingLinkOnly(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "rhpaplorg")
	linkPerson := a.NewPerson(t, "rhpaplink")
	appPerson := a.NewPerson(t, "rhpaplapp")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	base := "/v1/hack/events/" + slug

	hacktest.WantCode(t, "approval on", a.At(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": true}, a.Key(org)), 200, "")
	code := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	hacktest.WantCode(t, "link join", a.At(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "L"}, a.Key(linkPerson)), 200, "")
	hacktest.WantCode(t, "apply", a.Apply(t, slug, appPerson, nil), 200, "")
	hacktest.WantCode(t, "admit pending", a.At(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false, "admit_pending": true}, a.Key(org)), 200, "")

	for who, want := range map[string]string{"link": "approved", "application": "pending"} {
		p := linkPerson
		if who == "application" {
			p = appPerson
		}
		var got string
		if err := a.DB.QueryRow(`SELECT approval_status FROM event_members WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.UserID(t, p)).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s status after admit_pending = %q, want %q", who, got, want)
		}
	}
}

// Moving the last admin to another team promotes someone on the old team and
// does not carry the admin rank to the new team.
func TestRhpTeamAdminSuccessionOnOrganiserMove(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "rhpsmorg")
	leader := a.NewPerson(t, "rhpsmleader")
	member := a.NewPerson(t, "rhpsmmember")
	third := a.NewPerson(t, "rhpsmthird")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member, third} {
		a.JoinParticipant(t, join, p, "P")
	}
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	gammaID, _ := a.CreateTeamAndJoin(t, slug, third, "Gamma")
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "organiser moves leader", a.At(t, "POST", base+"/teams/"+a.TeamSlugByID(t, gammaID)+"/members", map[string]any{"user_id": a.UserID(t, leader)}, a.Key(org)), 200, "")
	if !a.IsTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin of the old team")
	}
	if a.TeamOf(t, leader) != gammaID {
		t.Fatal("leader was not moved")
	}
	if a.IsTeamAdmin(t, leader) {
		t.Fatal("leader kept admin on the new team")
	}
}
