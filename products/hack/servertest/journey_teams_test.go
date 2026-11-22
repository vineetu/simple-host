package servertest

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/hacktest"
)

func TestHackTeamLinkPreviewReasons(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "tlrorg")
	leader := a.NewPerson(t, "tlrleader")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, leader, "Leader")
	teamID, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	base := "/v1/hack/events/" + slug

	// Full.
	hacktest.WantCode(t, "max 1", a.At(t, "PATCH", base, map[string]any{"team_size_max": 1}, a.Key(org)), 200, "")
	r := a.At(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.JSON(t)["reason"] != "full" || r.JSON(t)["joinable"] != false {
		t.Fatalf("full: %v", r.JSON(t))
	}
	hacktest.WantCode(t, "restore max", a.At(t, "PATCH", base, map[string]any{"team_size_max": 4}, a.Key(org)), 200, "")

	// Locked.
	hacktest.WantCode(t, "lock", a.At(t, "PUT", base+"/team/lock", map[string]any{"locked": true}, a.Key(leader)), 200, "")
	r = a.At(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.JSON(t)["reason"] != "locked" {
		t.Fatalf("locked: %v", r.JSON(t))
	}
	hacktest.WantCode(t, "unlock", a.At(t, "PUT", base+"/team/lock", map[string]any{"locked": false}, a.Key(leader)), 200, "")

	// Teams closed: stage not open/building.
	hacktest.WantCode(t, "to closed", a.SetStage(t, base, "closed", a.Key(org)), 200, "")
	r = a.At(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.JSON(t)["reason"] != "teams_closed" {
		t.Fatalf("stage closed: %v", r.JSON(t))
	}
	hacktest.WantCode(t, "back to open", a.SetStage(t, base, "open", a.Key(org)), 200, "")

	// Teams closed: the team's own deadline passed.
	if _, err := a.DB.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	r = a.At(t, "GET", "/v1/hack/team-links/"+code, nil, nil)
	if r.JSON(t)["reason"] != "teams_closed" {
		t.Fatalf("deadline passed: %v", r.JSON(t))
	}
	_ = teamID
}

func TestHackTeamLinkTakenDownAndUnknown(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "tltdorg")
	leader := a.NewPerson(t, "tltdleader")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, leader, "Leader")
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	hacktest.WantCode(t, "unknown code", a.At(t, "GET", "/v1/hack/team-links/doesnotexist", nil, nil), 404, "team_link_not_found")
	hacktest.WantCode(t, "take down", a.At(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "test"}, a.AdminH()), 200, "")
	hacktest.WantCode(t, "taken down team link", a.At(t, "GET", "/v1/hack/team-links/"+code, nil, nil), 404, "team_link_not_found")
}

// TestHackTeamFindingFillAndRegistrationPermissions: only members see open
// teams, team finding off stops requests, and only an organiser may run
// Fill teams or change registration.
func TestHackTeamFindingFillAndRegistrationPermissions(t *testing.T) {
	a := newFeatApp(t)
	org, leader, solo, stranger := a.NewPerson(t, "tfo"), a.NewPerson(t, "tfl"), a.NewPerson(t, "tfs"), a.NewPerson(t, "tfx")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, leader, "Leader")
	a.JoinParticipant(t, join, solo, "Solo")
	a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	team := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(leader)).JSON(t)["me"].(map[string]any)["team"].(map[string]any)["slug"].(string)
	base := "/v1/hack/events/" + slug

	hacktest.WantTS(t, "stranger open teams", a.At(t, "GET", base+"/teams/open", nil, a.Key(stranger)), 404, "")
	hacktest.WantTS(t, "member open teams", a.At(t, "GET", base+"/teams/open", nil, a.Key(solo)), 200, "")
	hacktest.WantTS(t, "looking", a.At(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "I design"}, a.Key(solo)), 200, "")

	hacktest.WantTS(t, "participant fill", a.At(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, a.Key(leader)), 404, "")
	hacktest.WantTS(t, "organiser fill preview", a.At(t, "POST", base+"/teams/fill", map[string]any{"apply": false}, a.Key(org)), 200, "")

	hacktest.WantTS(t, "participant registration", a.At(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": true}, a.Key(leader)), 404, "")
	hacktest.WantTS(t, "organiser registration", a.At(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": false}, a.Key(org)), 200, "")

	hacktest.WantTS(t, "finding off", a.At(t, "PATCH", base, map[string]any{"team_finding": false}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "request with finding off", a.At(t, "POST", base+"/teams/"+team+"/requests", nil, a.Key(solo)), 409, "team_finding_off")
}

// TestHackSoloTeamCannotLeaveAfterDeadline: past the deadline a solo team's
// only member cannot leave the team (which would take the submission with it).
func TestHackSoloTeamCannotLeaveAfterDeadline(t *testing.T) {
	a := newFeatApp(t)
	org, p := a.NewPerson(t, "slo"), a.NewPerson(t, "slp")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p, "Solo")
	a.CreateTeamAndJoin(t, slug, p, "Lonely")
	base := "/v1/hack/events/" + slug
	hacktest.WantTS(t, "deadline passed", a.At(t, "PATCH", base, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "leave after deadline", a.At(t, "POST", base+"/teams/leave", nil, a.Key(p)), 409, "submissions_closed")
}

// TestHackDeadlineAcrossDST: a wall-clock deadline is read in the event's
// zone on its own date, so the offset follows daylight saving, and a time
// the clocks skip is refused.
func TestHackDeadlineAcrossDST(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "dst")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, map[string]any{"starts_at": "2027-03-20", "ends_at": "2027-04-02", "time_zone": "Europe/London"}), 201, "")
	base := "/v1/hack/events/" + slug
	for _, tc := range []struct{ in, want string }{
		{"2027-03-27T12:00", "2027-03-27T12:00:00Z"}, // GMT, before the change
		{"2027-03-29T12:00", "2027-03-29T11:00:00Z"}, // BST, after it
	} {
		r := a.At(t, "PATCH", base, map[string]string{"submission_deadline": tc.in}, a.Key(org))
		hacktest.WantTS(t, tc.in, r, 200, "")
		if got := r.JSON(t)["event"].(map[string]any)["submission_deadline"]; got != tc.want {
			t.Fatalf("%s stored as %v, want %s", tc.in, got, tc.want)
		}
	}
	hacktest.WantTS(t, "dst gap", a.At(t, "PATCH", base, map[string]string{"submission_deadline": "2027-03-28T01:30"}, a.Key(org)), 400, "invalid_submission_deadline")
}

func TestHackTeamCreatorIsAdmin(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "tcaorg")
	leader := a.NewPerson(t, "tcaleader")
	joiner := a.NewPerson(t, "tcajoiner")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, leader, "Leader")
	a.JoinParticipant(t, join, joiner, "Joiner")
	teamID, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	if !a.IsTeamAdmin(t, leader) {
		t.Fatal("creator is not admin")
	}
	view := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(leader)).JSON(t)
	team := view["me"].(map[string]any)["team"].(map[string]any)
	if team["admin"] != true || team["locked"] != false || team["kept"] != false {
		t.Fatalf("leader's team view: %v", team)
	}
	link, _ := team["link"].(string)
	if !strings.HasSuffix(link, "/team/"+code) {
		t.Fatalf("team link: %v", team)
	}
	a.JoinTeamByCode(t, slug, code, joiner)
	if a.IsTeamAdmin(t, joiner) {
		t.Fatal("code joiner became admin")
	}
	joinerView := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(joiner)).JSON(t)
	joinerTeam := joinerView["me"].(map[string]any)["team"].(map[string]any)
	if joinerTeam["admin"] != false {
		t.Fatalf("joiner admin flag: %v", joinerTeam)
	}
	for _, m := range joinerTeam["members"].([]any) {
		mm := m.(map[string]any)
		wantAdmin := mm["user_id"] == a.UserID(t, leader)
		if mm["admin"] != wantAdmin {
			t.Fatalf("member admin flags: %v", joinerTeam["members"])
		}
	}
	_ = teamID
}

func TestHackTeamRemovalAdminGating(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "trmorg")
	leader := a.NewPerson(t, "trmleader")
	member := a.NewPerson(t, "trmmember")
	other := a.NewPerson(t, "trmother")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member, other} {
		a.JoinParticipant(t, join, p, "P")
	}
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	a.JoinTeamByCode(t, slug, code, other)
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "member removes", a.At(t, "DELETE", base+"/team/members/"+a.UserID(t, other), nil, a.Key(member)), 403, "team_admin_only")
	// Admin removes self is refused (self-removal; use leave instead).
	hacktest.WantCode(t, "admin removes self", a.At(t, "DELETE", base+"/team/members/"+a.UserID(t, leader), nil, a.Key(leader)), 403, "")
	r := a.At(t, "DELETE", base+"/team/members/"+a.UserID(t, other), nil, a.Key(leader))
	hacktest.WantCode(t, "admin removes member", r, 204, "")
	if a.TeamOf(t, other) != "" {
		t.Fatal("removed member still on team")
	}
	notice := a.At(t, "GET", base, nil, a.Key(other)).JSON(t)["me"].(map[string]any)["team_removal_notice"]
	if !strings.Contains(notice.(string), "took you off Alpha") {
		t.Fatalf("notice: %v", notice)
	}
	oldCode := code
	r = a.At(t, "POST", base+"/teams/join", map[string]any{"code": oldCode}, a.Key(other))
	hacktest.WantCode(t, "old code rotated", r, 404, "team_not_found")
}

func TestHackTeamLock(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lockorg")
	leader := a.NewPerson(t, "lockleader")
	asker := a.NewPerson(t, "lockasker")
	invitee := a.NewPerson(t, "lockinvitee")
	codeJoiner := a.NewPerson(t, "lockcodejoiner")
	linkJoiner := a.NewPerson(t, "locklinkjoiner")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, asker, invitee, codeJoiner, linkJoiner} {
		a.JoinParticipant(t, join, p, "P")
	}
	teamID, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	base := "/v1/hack/events/" + slug
	// A pending request and invite exist before the lock, to prove locking
	// clears them.
	hacktest.WantCode(t, "ask before lock", a.At(t, "POST", base+"/teams/alpha/requests", nil, a.Key(asker)), 201, "")
	hacktest.WantCode(t, "invitee opts into looking", a.At(t, "PUT", base+"/me/looking", map[string]any{"looking": true}, a.Key(invitee)), 200, "")
	hacktest.WantCode(t, "invite before lock", a.At(t, "POST", base+"/team/invites", map[string]any{"user_id": a.UserID(t, invitee)}, a.Key(leader)), 201, "")
	r := a.At(t, "PUT", base+"/team/lock", map[string]any{"locked": true}, a.Key(leader))
	hacktest.WantCode(t, "lock", r, 200, "")
	cleared := r.JSON(t)["cleared"].(map[string]any)
	if cleared["requests"] != float64(1) || cleared["invites"] != float64(1) {
		t.Fatalf("cleared counts: %v", cleared)
	}
	var offers int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_team_requests WHERE team_id=$1`, teamID).Scan(&offers); err != nil || offers != 0 {
		t.Fatalf("offers not cleared: %d %v", offers, err)
	}
	hacktest.WantCode(t, "code join locked", a.At(t, "POST", base+"/teams/join", map[string]any{"code": code}, a.Key(codeJoiner)), 409, "team_locked")
	hacktest.WantCode(t, "link join locked", a.At(t, "POST", base+"/teams/join", map[string]any{"code": a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(leader)).JSON(t)["me"].(map[string]any)["team"].(map[string]any)["link"].(string)}, a.Key(linkJoiner)), 409, "team_locked")
	hacktest.WantCode(t, "request locked", a.At(t, "POST", base+"/teams/alpha/requests", nil, a.Key(codeJoiner)), 409, "team_locked")
	hacktest.WantCode(t, "invite locked", a.At(t, "POST", base+"/team/invites", map[string]any{"user_id": a.UserID(t, codeJoiner)}, a.Key(leader)), 409, "team_locked")
	// Organiser hand placement still works on a plain locked team.
	hacktest.WantCode(t, "organiser places into locked team", a.At(t, "POST", base+"/teams/alpha/members", map[string]any{"user_id": a.UserID(t, codeJoiner)}, a.Key(org)), 200, "")
	// Unlock re-allows ordinary joining.
	hacktest.WantCode(t, "unlock", a.At(t, "PUT", base+"/team/lock", map[string]any{"locked": false}, a.Key(leader)), 200, "")
	hacktest.WantCode(t, "join after unlock", a.At(t, "POST", base+"/teams/join", map[string]any{"code": code}, a.Key(linkJoiner)), 200, "")
}

func TestHackTeamKeptCannotBeUnlockedOrPlaced(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "keptorg")
	leader := a.NewPerson(t, "keptleader")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, leader, "Leader")
	teamID, _ := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	// Past its deadline, leaving keeps (and now also locks) the team.
	if _, err := a.DB.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "leader leaves event past deadline", a.Leave(t, slug, leader, map[string]any{"confirm": slug}), 200, "")
	var kept, locked bool
	if err := a.DB.QueryRow(`SELECT kept_after_leave, locked_at IS NOT NULL FROM event_teams WHERE id=$1`, teamID).Scan(&kept, &locked); err != nil {
		t.Fatal(err)
	}
	if !kept || !locked {
		t.Fatalf("kept=%v locked=%v", kept, locked)
	}
	newcomer := a.NewPerson(t, "keptnewcomer")
	a.JoinParticipant(t, join, newcomer, "New")
	slugOfTeam := a.TeamSlugByID(t, teamID)
	hacktest.WantCode(t, "organiser cannot place into kept team", a.At(t, "POST", base+"/teams/"+slugOfTeam+"/members", map[string]any{"user_id": a.UserID(t, newcomer)}, a.Key(org)), 409, "team_kept")
	hacktest.WantCode(t, "cannot unlock a kept team", a.At(t, "PUT", base+"/teams/"+slugOfTeam+"/lock", map[string]any{"locked": false}, a.Key(org)), 409, "team_kept")
}

func TestHackResultsStageLocksJudging(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "reslockorg")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	for _, stage := range []string{"open", "building", "closed", "judging"} {
		if r := a.SetStage(t, "/v1/hack/events/"+slug, stage, a.Key(org)); r.Status != 200 {
			t.Fatalf("stage %s: %d %s", stage, r.Status, r.Body)
		}
	}
	eventID := a.EventID(t, slug)
	var locked bool
	if err := a.DB.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&locked); err != nil || locked {
		t.Fatalf("locked before results: %v %v", locked, err)
	}
	hacktest.WantCode(t, "to results", a.SetStage(t, "/v1/hack/events/"+slug, "results", a.Key(org)), 200, "")
	if err := a.DB.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&locked); err != nil || !locked {
		t.Fatalf("not locked after results: %v %v", locked, err)
	}
	hacktest.WantCode(t, "back to judging", a.SetStage(t, "/v1/hack/events/"+slug, "judging", a.Key(org)), 200, "")
	if err := a.DB.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&locked); err != nil || locked {
		t.Fatalf("lock not cleared going back to judging: %v %v", locked, err)
	}
	hacktest.WantCode(t, "to results again", a.SetStage(t, "/v1/hack/events/"+slug, "results", a.Key(org)), 200, "")
	hacktest.WantCode(t, "publish at results", a.At(t, "POST", "/v1/hack/events/"+slug+"/results/publish", map[string]any{}, a.Key(org)), 200, "")
}

func TestHackTeams(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "to")
	people := []*hacktest.Person{}
	for i := 0; i < 5; i++ {
		p := a.NewPerson(t, "tm")
		people = append(people, &p)
	}
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	for _, p := range people {
		r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "M"}, a.Key(*p))
		if r.Status != 200 {
			t.Fatalf("join %s: %d %s", p.Email, r.Status, r.Body)
		}
	}
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"team_size_max": 2}, a.Key(org)); r.Status != 200 {
		t.Fatalf("size: %d %s", r.Status, r.Body)
	}

	r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Red Pandas"}, a.Key(*people[0]))
	if r.Status != 201 {
		t.Fatalf("create team: %d %s", r.Status, r.Body)
	}
	team := r.JSON(t)
	code := team["code"].(string)
	teamSlug := team["slug"].(string)
	if teamSlug == "" || code == "" {
		t.Fatalf("team: %v", team)
	}

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Other"}, a.Key(*people[0]))
	if r.Status != 409 || r.JSON(t)["code"] != "already_in_team" {
		t.Fatalf("second team: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.Key(*people[1]))
	if r.Status != 200 {
		t.Fatalf("join team: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.Key(*people[1]))
	if r.Status != 200 {
		t.Fatalf("idempotent join: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.Key(*people[2]))
	if r.Status != 409 || r.JSON(t)["code"] != "team_full" {
		t.Fatalf("full: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": "zzzzzz"}, a.Key(*people[2]))
	if r.Status != 404 || r.JSON(t)["code"] != "team_not_found" {
		t.Fatalf("missing: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Blue"}, a.Key(*people[2]))
	if r.Status != 201 {
		t.Fatalf("blue: %d %s", r.Status, r.Body)
	}
	blue := r.JSON(t)["slug"].(string)

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.Key(*people[2]))
	if r.Status != 409 || r.JSON(t)["code"] != "already_in_team" {
		t.Fatalf("one team: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/"+teamSlug+"/members", map[string]string{"user_id": a.UserID(t, *people[3])}, a.Key(org))
	if r.Status != 409 || r.JSON(t)["code"] != "team_full" {
		t.Fatalf("org full: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+teamSlug+"/members/"+a.UserID(t, *people[1]), nil, a.Key(org))
	if r.Status != http.StatusNoContent {
		t.Fatalf("remove member: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/"+teamSlug+"/members", map[string]string{"user_id": a.UserID(t, *people[3])}, a.Key(org))
	if r.Status != 200 {
		t.Fatalf("move: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, a.Key(*people[0]))
	if r.Status != 200 {
		t.Fatalf("leave: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+blue, nil, a.Key(org))
	if r.Status != http.StatusNoContent {
		t.Fatalf("delete team: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, slug, blue).Scan(&n); err != nil || n != 0 {
		t.Fatalf("blue still there: %d %v", n, err)
	}

	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Solo"}, a.Key(*people[4]))
	if r.Status != 201 {
		t.Fatalf("solo: %d %s", r.Status, r.Body)
	}
	solo := r.JSON(t)["slug"].(string)
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, a.Key(*people[4]))
	if r.Status != 200 {
		t.Fatalf("leave solo: %d %s", r.Status, r.Body)
	}
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, slug, solo).Scan(&n); err != nil || n != 0 {
		t.Fatalf("empty team not deleted: %d %v", n, err)
	}

	// Submissions closed is not offered yet (M2); its team lock still holds.
	if _, err := a.DB.Exec(`UPDATE events SET stage = 'closed' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Late"}, a.Key(*people[0]))
	if r.Status != 409 || r.JSON(t)["code"] != "teams_locked" {
		t.Fatalf("locked: %d %s", r.Status, r.Body)
	}

	r = a.At(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.UserID(t, org), nil, a.Key(org))
	if r.Status != 409 || r.JSON(t)["code"] != "cannot_remove_organiser" {
		t.Fatalf("remove org: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.UserID(t, *people[0]), nil, a.Key(org))
	if r.Status != http.StatusNoContent {
		t.Fatalf("remove person: %d %s", r.Status, r.Body)
	}
}
