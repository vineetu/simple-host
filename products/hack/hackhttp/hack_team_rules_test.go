package hackhttp

import (
	"testing"
	"time"
)

// TestHackTeamFindingFillAndRegistrationPermissions: only members see open
// teams, team finding off stops requests, and only an organiser may run
// Fill teams or change registration.
func TestHackTeamFindingFillAndRegistrationPermissions(t *testing.T) {
	a := newHackApp(t)
	org, leader, solo, stranger := a.newPerson(t, "tfo"), a.newPerson(t, "tfl"), a.newPerson(t, "tfs"), a.newPerson(t, "tfx")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	a.joinParticipant(t, join, solo, "Solo")
	a.createTeamAndJoin(t, slug, leader, "Alpha")
	team := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(leader)).json(t)["me"].(map[string]any)["team"].(map[string]any)["slug"].(string)
	base := "/v1/hack/events/" + slug

	wantTS(t, "stranger open teams", a.at(t, "GET", base+"/teams/open", nil, a.key(stranger)), 404, "")
	wantTS(t, "member open teams", a.at(t, "GET", base+"/teams/open", nil, a.key(solo)), 200, "")
	wantTS(t, "looking", a.at(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "I design"}, a.key(solo)), 200, "")

	wantTS(t, "participant fill", a.at(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, a.key(leader)), 404, "")
	wantTS(t, "organiser fill preview", a.at(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, a.key(org)), 200, "")

	wantTS(t, "participant registration", a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": true}, a.key(leader)), 404, "")
	wantTS(t, "organiser registration", a.at(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false}, a.key(org)), 200, "")

	wantTS(t, "finding off", a.at(t, "PATCH", base, map[string]any{"team_finding": false}, a.key(org)), 200, "")
	wantTS(t, "request with finding off", a.at(t, "POST", base+"/teams/"+team+"/requests", nil, a.key(solo)), 409, "team_finding_off")
}

// TestHackSoloTeamCannotLeaveAfterDeadline: past the deadline a solo team's
// only member cannot leave the team (which would take the submission with it).
func TestHackSoloTeamCannotLeaveAfterDeadline(t *testing.T) {
	a := newHackApp(t)
	org, p := a.newPerson(t, "slo"), a.newPerson(t, "slp")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p, "Solo")
	a.createTeamAndJoin(t, slug, p, "Lonely")
	base := "/v1/hack/events/" + slug
	wantTS(t, "deadline passed", a.at(t, "PATCH", base, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, a.key(org)), 200, "")
	wantTS(t, "leave after deadline", a.at(t, "POST", base+"/teams/leave", nil, a.key(p)), 409, "submissions_closed")
}

// TestHackDeadlineAcrossDST: a wall-clock deadline is read in the event's
// zone on its own date, so the offset follows daylight saving, and a time
// the clocks skip is refused.
func TestHackDeadlineAcrossDST(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "dst")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, map[string]any{"starts_at": "2027-03-20", "ends_at": "2027-04-02", "time_zone": "Europe/London"}), 201, "")
	base := "/v1/hack/events/" + slug
	for _, tc := range []struct{ in, want string }{
		{"2027-03-27T12:00", "2027-03-27T12:00:00Z"}, // GMT, before the change
		{"2027-03-29T12:00", "2027-03-29T11:00:00Z"}, // BST, after it
	} {
		r := a.at(t, "PATCH", base, map[string]string{"submission_deadline": tc.in}, a.key(org))
		wantTS(t, tc.in, r, 200, "")
		if got := r.json(t)["event"].(map[string]any)["submission_deadline"]; got != tc.want {
			t.Fatalf("%s stored as %v, want %s", tc.in, got, tc.want)
		}
	}
	wantTS(t, "dst gap", a.at(t, "PATCH", base, map[string]string{"submission_deadline": "2027-03-28T01:30"}, a.key(org)), 400, "invalid_submission_deadline")
}
