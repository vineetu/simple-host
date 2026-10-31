package handler

import (
	"database/sql"
	"net/http"
	"testing"
)

// Leave this event (SIM-340). The harness is newHackApp (DB_DSN), same as
// the other hack tests.

func (a *hackApp) eventID(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *hackApp) joinCodes(t *testing.T, org person, slug string) (join, judge string) {
	t.Helper()
	view := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)
	orgView := view["organiser"].(map[string]any)
	return orgView["join_code"].(string), orgView["judge_code"].(string)
}

func (a *hackApp) joinParticipant(t *testing.T, join string, p person, name string) {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": name}, a.key(p))
	if r.status != http.StatusOK {
		t.Fatalf("join %s: %d %s", name, r.status, r.body)
	}
}

func (a *hackApp) joinJudge(t *testing.T, judgeCode string, p person, name string) {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": name}, a.key(p))
	if r.status != http.StatusOK {
		t.Fatalf("judge join %s: %d %s", name, r.status, r.body)
	}
}

// createTeamAndJoin has p create "name" and returns its team id and join code.
func (a *hackApp) createTeamAndJoin(t *testing.T, slug string, p person, name string) (teamID, code string) {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.key(p))
	if r.status != http.StatusCreated {
		t.Fatalf("create team %s: %d %s", name, r.status, r.body)
	}
	j := r.json(t)
	return a.teamIDByName(t, slug, name), j["code"].(string)
}

func (a *hackApp) joinTeamByCode(t *testing.T, slug, code string, p person) {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]any{"code": code}, a.key(p))
	if r.status != http.StatusOK {
		t.Fatalf("join team: %d %s", r.status, r.body)
	}
}

func (a *hackApp) leave(t *testing.T, slug string, p person, body map[string]any) resp {
	t.Helper()
	return a.at(t, "POST", "/v1/hack/events/"+slug+"/leave", body, a.key(p))
}

func (a *hackApp) previewLeave(t *testing.T, slug string, p person) resp {
	t.Helper()
	return a.at(t, "GET", "/v1/hack/events/"+slug+"/leave", nil, a.key(p))
}

func (a *hackApp) isMember(t *testing.T, slug, userID string) bool {
	t.Helper()
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id = (SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func (a *hackApp) firstCriterionID(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM rubric_criteria WHERE event_id = $1 ORDER BY position LIMIT 1`, a.eventID(t, slug)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// --- 1: participant on a team with others ---------------------------------

func TestHackLeaveParticipantWithTeammates(t *testing.T) {
	a := newHackApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org := a.newPerson(t, "lv1org")
	p1, p2 := a.newPerson(t, "lv1p1"), a.newPerson(t, "lv1p2")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Ayu")
	a.joinParticipant(t, join, p2, "Nadia")
	teamID, code := a.createTeamAndJoin(t, slug, p1, "SheBuilds")
	a.joinTeamByCode(t, slug, code, p2)

	// An unsent announcement delivery queued for p1 must be dropped when
	// they leave; p2's stays.
	ann := a.at(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Hi", "body": "Welcome", "email_participants": true}, a.key(org))
	wantTS(t, "announcement", ann, 201, "")

	p1ID, p2ID := a.userID(t, p1), a.userID(t, p2)
	countPending := func(userID string) int {
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM event_announcement_deliveries d USING event_announcements an WHERE d.announcement_id=an.id AND an.event_id=$1 AND d.user_id=$2 AND d.sent_at IS NULL`, a.eventID(t, slug), userID).Scan(&n); err != nil {
			// USING in a SELECT isn't valid; fall back to a join.
			_ = a.database.QueryRow(`SELECT count(*) FROM event_announcement_deliveries d JOIN event_announcements an ON an.id=d.announcement_id WHERE an.event_id=$1 AND d.user_id=$2 AND d.sent_at IS NULL`, a.eventID(t, slug), userID).Scan(&n)
		}
		return n
	}
	if countPending(p1ID) == 0 {
		t.Fatal("expected a pending delivery for p1 before leaving")
	}

	prev := a.previewLeave(t, slug, p1).json(t)
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

	out := a.leave(t, slug, p1, map[string]any{"confirm": slug}).json(t)
	if out["left"] != true || out["case"] != "participant" || out["team_deleted"] != false || out["event"] != "kept" {
		t.Fatalf("leave response: %v", out)
	}

	if a.isMember(t, slug, p1ID) {
		t.Fatal("p1 still a member")
	}
	if !a.isMember(t, slug, p2ID) {
		t.Fatal("p2 should remain a member")
	}
	// Team, its code and its membership for p2 survive.
	var stillTeam bool
	if err := a.database.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if !stillTeam {
		t.Fatal("team should survive when a teammate remains")
	}
	var liveCode string
	if err := a.database.QueryRow(`SELECT code FROM event_teams WHERE id=$1`, teamID).Scan(&liveCode); err != nil {
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
	wantTS(t, "leave again", a.leave(t, slug, p1, map[string]any{"confirm": slug}), 404, "event_not_found")
}

// --- 2: lone member's team is deleted --------------------------------------

func TestHackLeaveLoneParticipantDeletesTeam(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv2org")
	p1 := a.newPerson(t, "lv2p1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Solo")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "LoneTeam")

	prev := a.previewLeave(t, slug, p1).json(t)
	team := prev["team"].(map[string]any)
	if team["deleted_on_leave"] != true || team["others_count"] != float64(0) {
		t.Fatalf("preview lone team: %v", team)
	}

	out := a.leave(t, slug, p1, map[string]any{"confirm": slug}).json(t)
	if out["team_deleted"] != true {
		t.Fatalf("leave response: %v", out)
	}
	var stillTeam bool
	if err := a.database.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if stillTeam {
		t.Fatal("lone team should be deleted")
	}
}

// --- 3: frozen team, and a teamless participant in a late stage -----------

func TestHackLeaveParticipantFrozenAndTeamless(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv3org")
	p1, p2 := a.newPerson(t, "lv3p1"), a.newPerson(t, "lv3p2")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Ayu")
	a.joinParticipant(t, join, p2, "Nadia")
	_, code := a.createTeamAndJoin(t, slug, p1, "FrozenTeam")
	a.joinTeamByCode(t, slug, code, p2)

	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	wantHackErr(t, "frozen team", a.leave(t, slug, p1, map[string]any{"confirm": slug}), 409, "submissions_closed",
		"your team's deadline has passed, so your team can't change and you can't leave. Ask an organiser")

	// A teamless participant is never frozen by the team deadline: move them
	// to Judging and confirm they can still leave.
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	p3 := a.newPerson(t, "lv3p3")
	// Join link only works while open/building; put the event back briefly.
	if _, err := a.database.Exec(`UPDATE events SET stage='open' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	a.joinParticipant(t, join, p3, "NoTeam")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	out := a.leave(t, slug, p3, map[string]any{"confirm": slug}).json(t)
	if out["left"] != true || out["case"] != "participant" {
		t.Fatalf("teamless leave: %v", out)
	}
}

// --- 5/6: pending applicant withdraws, rejected is refused -----------------

func TestHackLeaveApplicantWithdrawAndRejected(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv5org")
	pending, rejected := a.newPerson(t, "lv5pend"), a.newPerson(t, "lv5rej")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	a.listEvent(t, slug)
	wantTS(t, "apply pending", a.apply(t, slug, pending, nil), 200, "")
	wantTS(t, "apply rejected", a.apply(t, slug, rejected, nil), 200, "")
	wantTS(t, "reject", a.at(t, "POST", "/v1/hack/events/"+slug+"/applications/"+a.userID(t, rejected)+"/decision", map[string]string{"decision": "rejected"}, a.key(org)), 200, "")

	prev := a.previewLeave(t, slug, pending).json(t)
	if prev["case"] != "applicant" || prev["allowed"] != true {
		t.Fatalf("pending preview: %v", prev)
	}
	out := a.leave(t, slug, pending, map[string]any{"confirm": slug}).json(t)
	if out["left"] != true || out["case"] != "applicant" {
		t.Fatalf("withdraw: %v", out)
	}
	if a.isMember(t, slug, a.userID(t, pending)) {
		t.Fatal("withdrawn applicant should be gone")
	}
	wantTS(t, "re-apply", a.apply(t, slug, pending, nil), 200, "")

	rejPrev := a.previewLeave(t, slug, rejected).json(t)
	if rejPrev["case"] != "rejected" || rejPrev["allowed"] != false {
		t.Fatalf("rejected preview: %v", rejPrev)
	}
	wantHackErr(t, "rejected leave", a.leave(t, slug, rejected, map[string]any{"confirm": slug}), 409, "application_rejected",
		"your application was not accepted, and that decision stays on record")
	if !a.isMember(t, slug, a.userID(t, rejected)) {
		t.Fatal("rejected row must stay")
	}
}

// --- 7/8: judge keeps counted scores, rejoin clears the record ------------

func TestHackLeaveJudgeKeepsScoresAndRejoinClears(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv7org")
	p1 := a.newPerson(t, "lv7p1")
	judge := a.newPerson(t, "lv7j1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "ScoredTeam")
	a.joinJudge(t, judgeCode, judge, "Judge One")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}

	criterion := a.firstCriterionID(t, slug)
	score := a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 4}}}, a.key(judge))
	wantTS(t, "score", score, 200, "")

	dashBefore := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
	teamsBefore := dashBefore["teams"].([]any)[0].(map[string]any)
	if teamsBefore["judges_scored"] != float64(1) {
		t.Fatalf("coverage before leave: %v", teamsBefore)
	}

	prev := a.previewLeave(t, slug, judge).json(t)
	if prev["case"] != "judge" || prev["scored_teams"] != float64(1) {
		t.Fatalf("judge preview: %v", prev)
	}
	out := a.leave(t, slug, judge, map[string]any{"confirm": slug}).json(t)
	if out["case"] != "judge" || out["left"] != true {
		t.Fatalf("judge leave: %v", out)
	}

	dashAfter := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
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
	a.joinJudge(t, judgeCode, judge, "Judge One")
	dashRejoined := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
	if len(dashRejoined["former_judges"].([]any)) != 0 {
		t.Fatalf("former_judges should clear on rejoin: %v", dashRejoined["former_judges"])
	}
	teamsRejoined := dashRejoined["teams"].([]any)[0].(map[string]any)
	if teamsRejoined["judges_scored"] != float64(1) {
		t.Fatalf("coverage after rejoin: %v", teamsRejoined)
	}
}

// --- 10/11: co-organiser leaves, creator must hand over --------------------

func TestHackLeaveCoOrganiserAndCreatorHandover(t *testing.T) {
	a := newHackApp(t)
	creator := a.newPerson(t, "lv10cr")
	co := a.newPerson(t, "lv10co")
	part := a.newPerson(t, "lv10pa")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(creator))
	wantTS(t, "invite", inviteR, 201, "")
	code := inviteR.json(t)["url"].(string)
	// code is a full URL; reuse the accept path convention from administration tests.
	acceptPath := "/v1/hack/organiser/" + code[len(code)-32:]
	wantTS(t, "accept", a.at(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "Co"}, a.key(co)), 200, "")

	a.openEvent(t, creator, slug)
	join, _ := a.joinCodes(t, creator, slug)
	a.joinParticipant(t, join, part, "Part")

	// Co-organiser leaves: creator unchanged, co's outstanding invite (if
	// any) stops working. Issue one more invite from co, then have co leave.
	coInviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(co))
	wantTS(t, "co invite", coInviteR, 201, "")
	coInviteCode := coInviteR.json(t)["url"].(string)
	coAcceptPath := "/v1/hack/organiser/" + coInviteCode[len(coInviteCode)-32:]

	prev := a.previewLeave(t, slug, co).json(t)
	if prev["case"] != "co_organiser" {
		t.Fatalf("co preview: %v", prev)
	}
	out := a.leave(t, slug, co, map[string]any{"confirm": slug}).json(t)
	if out["case"] != "co_organiser" || out["left"] != true {
		t.Fatalf("co leave: %v", out)
	}
	var createdBy string
	if err := a.database.QueryRow(`SELECT created_by FROM events WHERE slug=$1`, slug).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != a.userID(t, creator) {
		t.Fatal("creator must stay unchanged")
	}
	wantTS(t, "stale invite dead", a.at(t, "GET", coAcceptPath, nil, nil), 404, "invalid_code")

	// Bring in a second organiser so the creator is "creator" (an organiser
	// with others), not "last_organiser", for the rest of this test.
	invite2 := a.at(t, "POST", base+"/organiser-invite", nil, a.key(creator))
	wantTS(t, "invite2", invite2, 201, "")
	code2 := invite2.json(t)["url"].(string)
	accept2 := "/v1/hack/organiser/" + code2[len(code2)-32:]
	co2 := a.newPerson(t, "lv11co2")
	wantTS(t, "accept2", a.at(t, "POST", accept2, map[string]any{"accept_coc": true, "display_name": "Co2"}, a.key(co2)), 200, "")

	// Creator: no successor.
	wantHackErr(t, "no successor", a.leave(t, slug, creator, map[string]any{"confirm": slug}), 409, "successor_required",
		"you created this event, so hand it to another organiser first: send successor_user_id")

	// Invalid successor: a participant, a random uuid.
	wantHackErr(t, "invalid successor participant", a.leave(t, slug, creator, map[string]any{"confirm": slug, "successor_user_id": a.userID(t, part)}), 409, "invalid_successor", "pick another organiser of this event")
	wantHackErr(t, "invalid successor uuid", a.leave(t, slug, creator, map[string]any{"confirm": slug, "successor_user_id": "00000000-0000-0000-0000-000000000000"}), 409, "invalid_successor", "pick another organiser of this event")

	// Valid handover.
	out = a.leave(t, slug, creator, map[string]any{"confirm": slug, "successor_user_id": a.userID(t, co2)}).json(t)
	if out["case"] != "creator" || out["left"] != true {
		t.Fatalf("creator leave: %v", out)
	}
	successor := out["successor"].(map[string]any)
	if successor["display_name"] != "Co2" {
		t.Fatalf("successor: %v", successor)
	}
	if err := a.database.QueryRow(`SELECT created_by FROM events WHERE slug=$1`, slug).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != a.userID(t, co2) {
		t.Fatal("created_by must move to the successor")
	}
	if a.isMember(t, slug, a.userID(t, creator)) {
		t.Fatal("former creator should be gone")
	}
}

// --- 12: last organiser ends or deletes ------------------------------------

func TestHackLeaveLastOrganiserEnd(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv12org")
	part := a.newPerson(t, "lv12pa")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")

	prev := a.previewLeave(t, slug, org).json(t)
	if prev["case"] != "last_organiser" {
		t.Fatalf("preview: %v", prev)
	}
	closeOpts := prev["close_options"].(map[string]any)
	end := closeOpts["end"].(map[string]any)
	if end["allowed"] != false || end["blocked"] != "archive_needs_participants" {
		t.Fatalf("close_options before anyone joined: %v", closeOpts)
	}

	wantHackErr(t, "no close", a.leave(t, slug, org, map[string]any{"confirm": slug}), 409, "last_organiser",
		`you are the only organiser, so leaving closes the event: send close "end" or "delete"`)
	wantHackErr(t, "end without participants", a.leave(t, slug, org, map[string]any{"confirm": slug, "close": "end"}), 409, "archive_needs_participants",
		"nobody has joined, so delete the event instead")

	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, part, "Part")

	out := a.leave(t, slug, org, map[string]any{"confirm": slug, "close": "end"}).json(t)
	if out["event"] != "ended" || out["left"] != true {
		t.Fatalf("end leave: %v", out)
	}
	var stage string
	var closedAt any
	if err := a.database.QueryRow(`SELECT stage, closed_at FROM events WHERE slug=$1`, slug).Scan(&stage, &closedAt); err != nil {
		t.Fatal(err)
	}
	if stage != "archived" || closedAt == nil {
		t.Fatalf("stage/closed_at: %v %v", stage, closedAt)
	}
	if a.isMember(t, slug, a.userID(t, org)) {
		t.Fatal("ended organiser should be gone")
	}
	// The public page still serves.
	wantTS(t, "public page", a.at(t, "GET", "/v1/hack/events/"+slug+"/public", nil, nil), 200, "")
}

// TestHackArchiveKeepsFutureDeadlineWhenAlreadyClosed is Nit 3: before
// archiveEventTx was extracted from setStage (git show
// 0339c499:internal/handler/hack.go), archiving an event whose submissions
// were already closed (closed, judging or results) left a future
// submission_deadline alone -- only archiving from draft, open or building
// ever moved it to now. The extraction dropped that guard, so ending an
// event with a deliberately-extended deadline still in the future reset it
// to now regardless of the current stage. Restored exactly: archiveEventTx
// now takes the current stage and skips the reset once submissions were
// already closed. Covers both setStage {"stage":"archived"} and POST
// .../leave's last_organiser "end" case, since both share archiveEventTx.
func TestHackArchiveKeepsFutureDeadlineWhenAlreadyClosed(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvarchorg")
	part := a.newPerson(t, "lvarchp1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, part, "Pat")

	futureDeadline := func(stage string) bool {
		var future bool
		if err := a.database.QueryRow(`SELECT submission_deadline > clock_timestamp() FROM events WHERE slug=$1`, slug).Scan(&future); err != nil {
			t.Fatal(err)
		}
		return future
	}

	for _, stage := range []string{"closed", "judging", "results"} {
		if _, err := a.database.Exec(`UPDATE events SET stage=$2, submission_deadline=clock_timestamp()+interval '1 hour' WHERE slug=$1`, slug, stage); err != nil {
			t.Fatal(err)
		}
		r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]any{"stage": "archived"}, a.key(org))
		if r.status != http.StatusOK {
			t.Fatalf("archive from %s: %d %s", stage, r.status, r.body)
		}
		if !futureDeadline(stage) {
			t.Fatalf("archiving from %s must leave a future submission_deadline alone", stage)
		}
	}

	// Control: archiving from open (submissions not yet closed) still moves
	// a future deadline to now, same as it always has.
	if _, err := a.database.Exec(`UPDATE events SET stage='open', submission_deadline=clock_timestamp()+interval '1 hour' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]any{"stage": "archived"}, a.key(org))
	if r.status != http.StatusOK {
		t.Fatalf("archive from open: %d %s", r.status, r.body)
	}
	if futureDeadline("open") {
		t.Fatal("archiving from open must still move a future submission_deadline to now")
	}
}

// TestHackLeaveEndArchiveKeepsFutureDeadlineWhenAlreadyClosed is the same
// Nit 3 guarantee through POST .../leave's last_organiser "end" case rather
// than setStage directly, since both call archiveEventTx.
func TestHackLeaveEndArchiveKeepsFutureDeadlineWhenAlreadyClosed(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvarchleorg")
	part := a.newPerson(t, "lvarchlep1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, part, "Pat")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging', submission_deadline=clock_timestamp()+interval '1 hour' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}

	out := a.leave(t, slug, org, map[string]any{"confirm": slug, "close": "end"}).json(t)
	if out["event"] != "ended" {
		t.Fatalf("end leave: %v", out)
	}
	var future bool
	if err := a.database.QueryRow(`SELECT submission_deadline > clock_timestamp() FROM events WHERE slug=$1`, slug).Scan(&future); err != nil {
		t.Fatal(err)
	}
	if !future {
		t.Fatal("ending from judging (submissions already closed) must leave a future submission_deadline alone")
	}
}

func TestHackLeaveLastOrganiserDelete(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv12dorg")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	accountID := a.eventAccount(t, slug)

	out := a.leave(t, slug, org, map[string]any{"confirm": slug, "close": "delete"}).json(t)
	if out["event"] != "deleted" || out["left"] != true {
		t.Fatalf("delete leave: %v", out)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM events WHERE slug=$1`, slug).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("event should be gone")
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM users WHERE id=$1`, accountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("holding account should be gone")
	}
}

// --- 13/14: stale fields, confirm ------------------------------------------

func TestHackLeaveStaleFieldsAndConfirm(t *testing.T) {
	a := newHackApp(t)
	creator := a.newPerson(t, "lv13cr")
	co := a.newPerson(t, "lv13co")
	part := a.newPerson(t, "lv13pa")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(creator))
	code := inviteR.json(t)["url"].(string)
	acceptPath := "/v1/hack/organiser/" + code[len(code)-32:]
	wantTS(t, "accept", a.at(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "Co"}, a.key(co)), 200, "")
	a.openEvent(t, creator, slug)
	join, _ := a.joinCodes(t, creator, slug)
	a.joinParticipant(t, join, part, "Part")

	// A co-organiser sending close, or a participant sending successor_user_id.
	wantHackErr(t, "co sends close", a.leave(t, slug, co, map[string]any{"confirm": slug, "close": "end"}), 409, "leave_state_changed", leaveStateChangedMsg)
	wantHackErr(t, "participant sends successor", a.leave(t, slug, part, map[string]any{"confirm": slug, "successor_user_id": a.userID(t, co)}), 409, "leave_state_changed", leaveStateChangedMsg)

	// Confirm missing or wrong.
	wantHackErr(t, "confirm missing", a.leave(t, slug, part, map[string]any{}), 409, "leave_needs_confirm", `to leave, send {"confirm":"<slug>"} with this event's address name`)
	wantHackErr(t, "confirm wrong", a.leave(t, slug, part, map[string]any{"confirm": "not-the-slug"}), 409, "leave_needs_confirm", `to leave, send {"confirm":"<slug>"} with this event's address name`)
	if !a.isMember(t, slug, a.userID(t, part)) {
		t.Fatal("nothing should change on a bad confirm")
	}
}

// --- 15/16: ended, taken down, not a member --------------------------------

func TestHackLeaveEndedTakenDownAndNotMember(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv15org")
	outsider := a.newPerson(t, "lv15out")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	part := a.newPerson(t, "lv15pa")
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, part, "Part")
	wantTS(t, "archive", a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, a.key(org)), 200, "")
	wantHackErr(t, "ended participant", a.leave(t, slug, part, map[string]any{"confirm": slug}), 409, "event_closed", "this event has ended, so nobody can leave it")

	takenSlug := uniqueSlug()
	tOrg := a.newPerson(t, "lv15torg")
	wantTS(t, "create2", a.createEvent(t, tOrg, takenSlug, nil), 201, "")
	wantTS(t, "takedown", a.at(t, "POST", "/v1/admin/hack/events/"+takenSlug+"/takedown", map[string]string{"reason": "spam"}, a.adminH()), 200, "")
	wantHackErr(t, "taken down organiser", a.leave(t, takenSlug, tOrg, map[string]any{"confirm": takenSlug}), 403, "event_taken_down", "this event has been taken down")

	wantTS(t, "not a member", a.leave(t, slug, outsider, map[string]any{"confirm": slug}), 404, "event_not_found")
	wantTS(t, "unknown slug", a.leave(t, "no-such-"+uniqueSlug(), org, map[string]any{"confirm": "x"}), 404, "event_not_found")
	wantTS(t, "admin not a member", a.leave(t, slug, person{key: a.admin}, map[string]any{"confirm": slug}), 404, "event_not_found")
}

// --- 20: the former-judge delete-scores route ------------------------------

func TestHackDeleteFormerJudgeScoresRoute(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lv20org")
	p1 := a.newPerson(t, "lv20p1")
	judge := a.newPerson(t, "lv20j1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.key(judge)), 200, "")
	wantTS(t, "leave", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	formerUserID := a.userID(t, judge)
	route := "/v1/hack/events/" + slug + "/judging/former-judges/"
	confirmBody := map[string]any{"confirm": slug}
	wantTS(t, "unknown former judge", a.at(t, "DELETE", route+"00000000-0000-0000-0000-000000000000", confirmBody, a.key(org)), 404, "former_judge_not_found")
	wantTS(t, "not organiser", a.at(t, "DELETE", route+formerUserID, nil, a.key(p1)), 404, "event_not_found")
	wantTS(t, "delete", a.at(t, "DELETE", route+formerUserID, confirmBody, a.key(org)), 204, "")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE judge_id=$1`, formerUserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("scores should be deleted")
	}
	dash := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
	if len(dash["former_judges"].([]any)) != 0 {
		t.Fatalf("former_judges should be empty: %v", dash["former_judges"])
	}
	teamsAfter := dash["teams"].([]any)[0].(map[string]any)
	if teamsAfter["judges_scored"] != float64(0) {
		t.Fatalf("coverage should drop: %v", teamsAfter)
	}
	// Second delete: already gone.
	wantTS(t, "delete again", a.at(t, "DELETE", route+formerUserID, confirmBody, a.key(org)), 404, "former_judge_not_found")
}

// --- 17/18: races --------------------------------------------------------

// TestHackLeaveTwoOrganisersRaceForcedOrdering is build plan tests item 17's
// first race: two co-organisers of a creator-less event (created_by NULL)
// POST leave at the same time. Forced ordering, the pattern
// hack_apply_test.go's TestHackApplyRace uses for its switch-off race: a
// third connection holds the event row FOR UPDATE so both leave requests
// queue behind it (waitBlocked after each), then the lock is released and
// both proceed together. Whichever commits first finds the other organiser
// still present (case co_organiser, 200); the second re-reads otherOrganisers
// fresh under its own lock on the now-committed state, finds nobody left,
// and must get 409 last_organiser -- never two 200s, which would leave the
// event with zero organisers.
func TestHackLeaveTwoOrganisersRaceForcedOrdering(t *testing.T) {
	a := newHackApp(t)
	orgA := a.newPerson(t, "lv17a")
	orgB := a.newPerson(t, "lv17b")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, orgA, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(orgA))
	wantTS(t, "invite", inviteR, 201, "")
	code := inviteR.json(t)["url"].(string)
	acceptPath := "/v1/hack/organiser/" + code[len(code)-32:]
	wantTS(t, "accept", a.at(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "OrgB"}, a.key(orgB)), 200, "")

	eventID := a.eventID(t, slug)
	if _, err := a.database.Exec(`UPDATE events SET created_by = NULL WHERE id = $1`, eventID); err != nil {
		t.Fatal(err)
	}

	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE slug=$1 FOR UPDATE`, slug).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	results := make(chan resp, 2)
	go func() { results <- a.leave(t, slug, orgA, map[string]any{"confirm": slug}) }()
	waitBlocked()
	go func() { results <- a.leave(t, slug, orgB, map[string]any{"confirm": slug}) }()
	waitBlocked() // both leaves now queue behind lockTx's FOR UPDATE.

	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	first, second := <-results, <-results
	var ok200, err409 int
	for _, r := range []resp{first, second} {
		switch r.status {
		case 200:
			ok200++
			out := r.json(t)
			if out["case"] != "co_organiser" || out["left"] != true {
				t.Fatalf("unexpected 200 body: %v", out)
			}
		case 409:
			err409++
			wantHackErr(t, "loser", r, 409, "last_organiser",
				`you are the only organiser, so leaving closes the event: send close "end" or "delete"`)
		default:
			t.Fatalf("unexpected status %d %s", r.status, r.body)
		}
	}
	if ok200 != 1 || err409 != 1 {
		t.Fatalf("want exactly one 200 and one 409, got %d/%d (first=%d %s, second=%d %s)",
			ok200, err409, first.status, first.body, second.status, second.body)
	}

	var organisers int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=$1 AND role='organiser'`, eventID).Scan(&organisers); err != nil {
		t.Fatal(err)
	}
	if organisers != 1 {
		t.Fatalf("event should still have exactly one organiser, got %d", organisers)
	}
}

// TestHackLeaveOrganiserInviteAcceptRaceForcedOrdering is build plan tests
// item 18: an organiser invite accept races with its inviter leaving. Before
// the 2.4 lock-order fix (acceptOrganiserInvite resolving the invite's event
// id unlocked, then taking the event row FOR UPDATE before the invite and
// inviter-member rows -- the same order POST .../leave already used), a
// leave that is the invite's own inviter could cycle against an accept:
// leave holds the event row and wants the inviter's member row, accept (old
// code) held the invite+inviter-member rows and wanted the event row via its
// insert's FOR KEY SHARE. Forced ordering, same shape as the test above:
// hold the event row FOR UPDATE from a third connection so both requests
// queue behind it, release, and require that each lands a clean, expected
// status -- never a Postgres deadlock error (which would surface as a 500)
// -- and that the two outcomes compose exactly one of the two valid
// orderings: accepted first (then the leave, re-reading fresh, sees two
// other organisers) or left first (then OrganiserInviteEvent's join on the
// inviter's organiser row -- gone -- makes the accept 404 invalid_code).
func TestHackLeaveOrganiserInviteAcceptRaceForcedOrdering(t *testing.T) {
	a := newHackApp(t)
	creator := a.newPerson(t, "lv18cr")
	inviter := a.newPerson(t, "lv18in")
	invitee := a.newPerson(t, "lv18ee")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	invite1 := a.at(t, "POST", base+"/organiser-invite", nil, a.key(creator))
	wantTS(t, "invite1", invite1, 201, "")
	code1 := invite1.json(t)["url"].(string)
	accept1 := "/v1/hack/organiser/" + code1[len(code1)-32:]
	wantTS(t, "inviter joins", a.at(t, "POST", accept1, map[string]any{"accept_coc": true, "display_name": "Inviter"}, a.key(inviter)), 200, "")

	// The inviter (not the last organiser -- creator is still around) sends
	// a second invite, then races leaving against its acceptance.
	invite2 := a.at(t, "POST", base+"/organiser-invite", nil, a.key(inviter))
	wantTS(t, "invite2", invite2, 201, "")
	code2 := invite2.json(t)["url"].(string)
	acceptPath := "/v1/hack/organiser/" + code2[len(code2)-32:]

	eventID := a.eventID(t, slug)
	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	acceptDone := make(chan resp, 1)
	leaveDone := make(chan resp, 1)
	go func() {
		acceptDone <- a.at(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "Invitee"}, a.key(invitee))
	}()
	waitBlocked()
	go func() { leaveDone <- a.leave(t, slug, inviter, map[string]any{"confirm": slug}) }()
	waitBlocked() // both now queue behind lockTx's FOR UPDATE.

	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	acceptResp, leaveResp := <-acceptDone, <-leaveDone

	if leaveResp.status != 200 {
		t.Fatalf("leave (never the last organiser either way): %d %s", leaveResp.status, leaveResp.body)
	}
	leaveOut := leaveResp.json(t)
	if leaveOut["case"] != "co_organiser" || leaveOut["left"] != true {
		t.Fatalf("unexpected leave body: %v", leaveOut)
	}

	var organisers int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE event_id=$1 AND role='organiser'`, eventID).Scan(&organisers); err != nil {
		t.Fatal(err)
	}

	switch acceptResp.status {
	case 200:
		// Accepted before the leave committed: creator, invitee stay; inviter left.
		if organisers != 2 {
			t.Fatalf("accepted-then-left should leave 2 organisers, got %d", organisers)
		}
	case 404:
		wantHackErr(t, "stale invite after inviter left first", acceptResp, 404, "invalid_code", "invalid or expired invitation")
		if organisers != 1 {
			t.Fatalf("left-then-stale-accept should leave 1 organiser, got %d", organisers)
		}
	default:
		t.Fatalf("unexpected accept status (deadlock?): %d %s", acceptResp.status, acceptResp.body)
	}
}

// --- Codex review fixes -----------------------------------------------------

// TestHackScoreEligiblePredicateEventScoped is a regression test for a Codex
// review finding (SIM-340): scoreEligiblePredicate's two OR'd branches were
// not wrapped in an outer pair of parens, so every caller's "WHERE
// s.event_id = $1 AND (member branch) OR (former branch)" let the
// former-judge branch through unscoped by the $1 filter it sits next to --
// a judge who left one event with counted scores leaked into every other
// event's coverage and leave-preview numbers, as long as they also belonged
// to that other event. Judge scores a team in event A, leaves (becomes a
// former judge there), then joins event B as a judge too without ever
// scoring there: event B's leave preview must show scored_teams: 0, not
// event A's count.
func TestHackScoreEligiblePredicateEventScoped(t *testing.T) {
	a := newHackApp(t)
	orgA := a.newPerson(t, "lvsepa")
	p1 := a.newPerson(t, "lvsepp")
	judge := a.newPerson(t, "lvsepj")
	slugA := uniqueSlug()
	wantTS(t, "create A", a.createEvent(t, orgA, slugA, nil), 201, "")
	a.openEvent(t, orgA, slugA)
	joinA, judgeCodeA := a.joinCodes(t, orgA, slugA)
	a.joinParticipant(t, joinA, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slugA, p1, "Delendum")
	a.joinJudge(t, judgeCodeA, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slugA); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slugA)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slugA+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.key(judge)), 200, "")
	wantTS(t, "leave A", a.leave(t, slugA, judge, map[string]any{"confirm": slugA}), 200, "")

	orgB := a.newPerson(t, "lvsepb")
	slugB := uniqueSlug()
	wantTS(t, "create B", a.createEvent(t, orgB, slugB, nil), 201, "")
	a.openEvent(t, orgB, slugB)
	_, judgeCodeB := a.joinCodes(t, orgB, slugB)
	a.joinJudge(t, judgeCodeB, judge, "Judge")

	prev := a.previewLeave(t, slugB, judge).json(t)
	if prev["scored_teams"] != float64(0) {
		t.Fatalf("event B's preview must not see event A's former-judge scores: %v", prev)
	}
}

// TestHackScorePutRaceRejectsDepartedJudge is a regression test for a Codex
// review finding (SIM-340): PUT .../judge/scores only checked membership via
// loadMember, before this transaction's own event lock, and
// db.JudgeTeamEligible (shared with the read side) now also accepts a
// former judge's frozen teams. A judge whose own leave commits first while
// their score write waits on the event lock must not be able to change the
// now-frozen score. Forced ordering, the same shape as
// hack_apply_test.go's TestHackApplyTakedownRaceForcedOrdering: hold the
// event row FOR UPDATE from a third connection, let the PUT block, commit
// the leave's effect directly (member row gone, a former-judge record
// freezing exactly this team -- what db.RecordFormerJudge plus
// db.LeaveMembership would leave behind) while it waits, release, then
// require 403 forbidden, not 200, and the score unchanged.
func TestHackScorePutRaceRejectsDepartedJudge(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvswrorg")
	p1 := a.newPerson(t, "lvswrp")
	judge := a.newPerson(t, "lvswrj")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	// An existing score, so a successful (buggy) write would be a visible change.
	wantTS(t, "initial score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)

	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE slug=$1 FOR UPDATE`, slug).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	putDone := make(chan resp, 1)
	go func() {
		putDone <- a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
			map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 5}}}, a.key(judge))
	}()
	waitBlocked() // the PUT's own event-row lock now waits on lockTx's FOR UPDATE.

	if _, err := lockTx.Exec(`DELETE FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, judgeID); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(`INSERT INTO event_former_judges (event_id, user_id, display_name, role, counted_team_ids, left_at)
		VALUES ($1, $2, 'Judge', 'judge', ARRAY[$3]::uuid[], now())`, eventID, judgeID, teamID); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	putResp := <-putDone
	wantHackErr(t, "score PUT after the judge's own leave committed", putResp, 403, "forbidden", "You are no longer a judge or organiser of this event.")

	var points int
	if err := a.database.QueryRow(`SELECT points FROM event_scores WHERE event_id=$1 AND judge_id=$2 AND team_id=$3 AND criterion_id=$4`,
		eventID, judgeID, teamID, criterion).Scan(&points); err != nil {
		t.Fatal(err)
	}
	if points != 2 {
		t.Fatalf("frozen score must stay at 2, got %d", points)
	}
}

// TestHackOrganiserInviteAcceptRaceLastOrganiserCloses is a regression test
// for a Codex review finding (SIM-340): acceptOrganiserInvite checked
// takedown/archived on the pre-lock event, then GetEventForUpdate's error
// path and the invite re-lock both assumed the event still exists. When the
// last organiser's own "end" or "delete" wins a race with someone accepting
// their invite, the loser must land a clean 404 invalid_code -- never a 409
// event_closed (stale: the real reason is the invite is gone) and never a
// 500 (the event row vanished under the lock). Forced ordering: hold the
// event row FOR UPDATE from a third connection, let accept block, commit
// the end/delete's effect directly while it waits, release, then assert.
func TestHackOrganiserInviteAcceptRaceLastOrganiserCloses(t *testing.T) {
	a := newHackApp(t)

	run := func(t *testing.T, simulateClose func(tx *sql.Tx, eventID string)) resp {
		org := a.newPerson(t, "lvoiend")
		invitee := a.newPerson(t, "lvoiee")
		slug := uniqueSlug()
		wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
		base := "/v1/hack/events/" + slug
		inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(org))
		wantTS(t, "invite", inviteR, 201, "")
		code := inviteR.json(t)["url"].(string)
		acceptPath := "/v1/hack/organiser/" + code[len(code)-32:]

		eventID := a.eventID(t, slug)
		lockTx, err := a.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		var lockedID string
		if err := lockTx.QueryRow(`SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
			t.Fatal(err)
		}

		acceptDone := make(chan resp, 1)
		go func() {
			acceptDone <- a.at(t, "POST", acceptPath, map[string]any{"accept_coc": true, "display_name": "Invitee"}, a.key(invitee))
		}()
		waitBlocked() // the accept's event-row lock now waits on lockTx's FOR UPDATE.

		// Mutate through lockTx itself -- the main pool would try to take
		// the same lock lockTx already holds and block right alongside the
		// accept goroutine, since nothing would ever release it.
		simulateClose(lockTx, eventID)
		if err := lockTx.Commit(); err != nil {
			t.Fatal(err)
		}
		return <-acceptDone
	}

	t.Run("end", func(t *testing.T) {
		resp := run(t, func(tx *sql.Tx, eventID string) {
			// What the last organiser's close:"end" leaves behind by the
			// time accept's blocked transaction gets the lock: archived,
			// and (section 2.3) its outstanding invites deleted.
			if _, err := tx.Exec(`UPDATE events SET stage='archived', closed_at=now() WHERE id=$1`, eventID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`DELETE FROM event_organiser_invites WHERE event_id=$1`, eventID); err != nil {
				t.Fatal(err)
			}
		})
		wantHackErr(t, "accept after end wins the race", resp, 404, "invalid_code", "invalid or expired invitation")
	})

	t.Run("delete", func(t *testing.T) {
		resp := run(t, func(tx *sql.Tx, eventID string) {
			// What the last organiser's close:"delete" leaves behind: the
			// event row (and, by cascade, its invite) gone entirely.
			if _, err := tx.Exec(`DELETE FROM events WHERE id=$1`, eventID); err != nil {
				t.Fatal(err)
			}
		})
		wantHackErr(t, "accept after delete wins the race", resp, 404, "invalid_code", "invalid or expired invitation")
	})
}

// TestHackJudgeJoinRaceLastOrganiserEnds is a regression test for a Codex
// review finding (SIM-340): the judge-join branch took the event row FOR
// SHARE but discarded the row, never rechecking stage or takedown against
// the state as of the lock -- so a judge-link join that was waiting on a
// last organiser's concurrent "end" could insert a new judge into the
// now-archived event once the lock released. Forced ordering, the same
// shape as the organiser-invite race above: hold the event row FOR UPDATE
// from a third connection, let the join block, archive the event directly
// while it waits, release, then require 409 judging_closed and no member
// row inserted.
func TestHackJudgeJoinRaceLastOrganiserEnds(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvjjorg")
	newJudge := a.newPerson(t, "lvjjj")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	_, judgeCode := a.joinCodes(t, org, slug)

	eventID := a.eventID(t, slug)
	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	joinDone := make(chan resp, 1)
	go func() {
		joinDone <- a.at(t, "POST", "/v1/hack/judge/"+judgeCode, map[string]any{"accept_coc": true, "display_name": "Late Judge"}, a.key(newJudge))
	}()
	waitBlocked() // the join's FOR SHARE now waits on lockTx's FOR UPDATE.

	// Mutate through lockTx itself -- the main pool would block on the same
	// lock lockTx already holds (nothing would ever release it).
	if _, err := lockTx.Exec(`UPDATE events SET stage='archived', closed_at=now() WHERE id=$1`, eventID); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	joinResp := <-joinDone
	wantHackErr(t, "judge join after end wins the race", joinResp, 409, "judging_closed", "judging is closed for this event")

	if a.isMember(t, slug, a.userID(t, newJudge)) {
		t.Fatal("the judge must not have been inserted into the now-archived event")
	}
}
