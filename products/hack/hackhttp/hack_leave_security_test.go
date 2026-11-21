package hackhttp

import (
	"context"
	"testing"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/entries"
)

// Regression tests for the Grok security review of SIM-340 (leave this
// event). Each test is named after the finding it covers.

// --- finding 2: delete-former-judge-scores skipped takedown and the ----
// --- caller's current organiser status under its own lock --------------

// TestHackDeleteFormerJudgeScoresRaceTakenDown is a regression test: a
// takedown that commits while delete-former-judge-scores is waiting on the
// event lock must stop the delete, not just the pre-transaction loadMember
// check it used to rely on alone.
func TestHackDeleteFormerJudgeScoresRaceTakenDown(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvdftd-org")
	p1 := a.newPerson(t, "lvdftd-p1")
	judge := a.newPerson(t, "lvdftd-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.giveSubmittedEntry(t, slug, teamID)
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.key(judge)), 200, "")
	wantTS(t, "judge leaves", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)

	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	deleteDone := make(chan resp, 1)
	go func() {
		deleteDone <- a.at(t, "DELETE", "/v1/hack/events/"+slug+"/judging/former-judges/"+judgeID, map[string]any{"confirm": slug}, a.key(org))
	}()
	waitBlocked()

	if _, err := lockTx.Exec(`UPDATE events SET taken_down_at = now(), taken_down_reason = $2 WHERE id = $1`, eventID, "spam"); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	r := <-deleteDone
	wantTS(t, "delete-scores after takedown wins the race", r, 403, "event_taken_down")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("scores must survive a delete that a takedown beat to the lock")
	}
}

// --- finding 3 / ship-blocker B1: leaving must never touch votes ----------

// TestHackLeaveNeverTouchesVotes: leaving is not a vote-eligibility sweep.
// Before the fix, POST .../leave called IneligibleVotes(..., remove=true),
// which deletes every ineligible vote in the WHOLE event, not just the
// leaver's -- so one unrelated participant leaving could delete someone
// else's vote outright (ship-blocker B1). The fix is to never call it from
// leave at all: a vote cast while eligible stands, the leaver's own
// included, exactly as the organiser's own Remove person already leaves
// votes untouched. Proven under "participants" eligibility, the strictest
// setting and the one the old code targeted.
func TestHackLeaveNeverTouchesVotes(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvvoteorg")
	p1 := a.newPerson(t, "lvvotep1")
	p2 := a.newPerson(t, "lvvotep2")
	p3 := a.newPerson(t, "lvvotep3")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "P1")
	a.joinParticipant(t, join, p2, "P2")
	a.joinParticipant(t, join, p3, "P3")
	a.createTeamAndJoin(t, slug, p1, "VoterTeam")
	teamID, _ := a.createTeamAndJoin(t, slug, p2, "VotedForTeam")
	if _, err := a.database.Exec(`UPDATE events SET voting_eligibility='participants' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	p1Email := model.BaseEmail(a.emailOf(t, p1))
	p3Email := model.BaseEmail(a.emailOf(t, p3))
	for _, email := range []string{p1Email, p3Email} {
		if _, err := a.database.Exec(`INSERT INTO event_votes (event_id, voter_email, team_id) VALUES ((SELECT id FROM events WHERE slug=$1), $2, $3)`,
			slug, email, teamID); err != nil {
			t.Fatal(err)
		}
	}
	count := func(email string) int {
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM event_votes WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND voter_email=$2`, slug, email).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// p1 leaves: their own vote, cast while eligible, stands -- leaving
	// never touches votes, regardless of eligibility.
	wantTS(t, "p1 leaves", a.leave(t, slug, p1, map[string]any{"confirm": slug}), 200, "")
	if count(p1Email) != 1 {
		t.Fatal("a leaver's own vote must stand -- leaving never touches votes")
	}
	// p3 never joined a team and never leaves; p1's departure must not have
	// swept p3's vote either (the whole-event sweep ship-blocker B1).
	if count(p3Email) != 1 {
		t.Fatal("BUG: an unrelated person's leave deleted someone else's vote")
	}
}

// --- finding 4: RemoveCoOrganiser and removePerson skipped the new cleanup

// TestHackRemoveCoOrganiserDeletesScoresAndCleansMail: an organiser who
// scored, removed by another organiser (not a voluntary leave), must have
// their scores deleted outright -- TestHackOrganisersCanJudgeAndRemoved
// OrganiserScoresStopCounting (hack_judging_test.go) already establishes "a
// removed organiser's scores stop counting" as the intended behaviour here,
// unlike a voluntary leave's frozen-but-kept scores -- and get the same
// unsent-mail cleanup a self-leave gets, which the old single-statement
// DELETE skipped entirely.
func TestHackRemoveCoOrganiserDeletesScoresAndCleansMail(t *testing.T) {
	a := newHackApp(t)
	creator := a.newPerson(t, "lvrco-cr")
	co := a.newPerson(t, "lvrco-co")
	p1 := a.newPerson(t, "lvrco-p1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(creator))
	wantTS(t, "invite", inviteR, 201, "")
	code := inviteR.json(t)["url"].(string)
	wantTS(t, "accept", a.at(t, "POST", "/v1/hack/organiser/"+code[len(code)-32:], map[string]any{"accept_coc": true, "display_name": "Co"}, a.key(co)), 200, "")
	a.openEvent(t, creator, slug)
	join, _ := a.joinCodes(t, creator, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.giveSubmittedEntry(t, slug, teamID)
	wantTS(t, "select organiser judges", a.at(t, "PATCH", base+"/judging/settings", map[string]any{"include_organisers": true}, a.key(creator)), 200, "")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "co scores as organiser", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 4}}}, a.key(co)), 200, "")

	// A queued, unsent announcement for "co" to clean up, same assertion
	// shape LeaveMembership's own tests use elsewhere.
	eventID := a.eventID(t, slug)
	coID := a.userID(t, co)
	postAnn := a.at(t, "POST", base+"/announcements", map[string]any{"title": "Hi", "body": "Body", "email_participants": false}, a.key(creator))
	wantTS(t, "post announcement", postAnn, 201, "")
	annID := postAnn.json(t)["announcement"].(map[string]any)["id"].(string)
	if _, err := a.database.Exec(`INSERT INTO event_announcement_deliveries (announcement_id, user_id, recipient) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, annID, coID, a.emailOf(t, co)); err != nil {
		t.Fatal(err)
	}

	wantTS(t, "remove co-organiser", a.at(t, "DELETE", base+"/organisers/"+coID, nil, a.key(creator)), 204, "")

	var scoreRows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, coID).Scan(&scoreRows); err != nil {
		t.Fatal(err)
	}
	if scoreRows != 0 {
		t.Fatal("a removed organiser's scores must be deleted outright, stopping them counting")
	}
	var formerRows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, coID).Scan(&formerRows); err != nil {
		t.Fatal(err)
	}
	if formerRows != 0 {
		t.Fatal("a removed organiser gets no former-judge record -- there is nothing left to freeze")
	}
	var unsent int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_announcement_deliveries WHERE user_id=$1 AND sent_at IS NULL`, coID).Scan(&unsent); err != nil {
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
	a := newHackApp(t)
	org := a.newPerson(t, "lvrpj-org")
	p1 := a.newPerson(t, "lvrpj-p1")
	judge := a.newPerson(t, "lvrpj-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.giveSubmittedEntry(t, slug, teamID)
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)
	wantTS(t, "organiser removes judge", a.at(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+judgeID, nil, a.key(org)), 204, "")

	var formerRole string
	if err := a.database.QueryRow(`SELECT role FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(&formerRole); err != nil {
		t.Fatalf("expected a former-judge record for the removed judge's scores: %v", err)
	}
	if formerRole != "judge" {
		t.Fatalf("former-judge role: got %q, want judge", formerRole)
	}
}

// --- finding 5: rejoining as a participant kept the score rows ------------

// TestHackParticipantRejoinDeletesFormerScores: a judge who left (scores
// frozen in a former-judge record) and rejoins as a participant must have
// their event_scores rows deleted outright, not just the former-judge
// record -- otherwise a later re-invite as a judge brings the old rows back
// counting, un-frozen.
func TestHackParticipantRejoinDeletesFormerScores(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvrejoin-org")
	p1 := a.newPerson(t, "lvrejoin-p1")
	judge := a.newPerson(t, "lvrejoin-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.giveSubmittedEntry(t, slug, teamID)
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")
	wantTS(t, "leave", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("scores should still exist right after leaving, frozen by the former-judge record")
	}

	// Reopen sign-up so the former judge can rejoin as a participant.
	if _, err := a.database.Exec(`UPDATE events SET stage='open' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	join2, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join2, judge, "Judge")

	if err := a.database.QueryRow(`SELECT count(*) FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the former-judge record must be cleared on rejoin")
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("rejoining as a participant must delete the old event_scores rows, not just the former-judge record")
	}
}

// --- finding 6: a conflict removed after leaving never came back --------

// --- ship-blocker B2: leaving must not make a non-counting score count ----

// TestHackLeaveDoesNotResurrectNonCountingScore: a score that did not count
// at the moment the judge left must not start counting afterwards.
// RecordFormerJudge used to freeze every team event_scores had a row for,
// with no eligibility check at all, so a score cast under "open" mode kept
// counting under a later, narrower mode (with no assignment) even after the
// scoring judge left (ship-blocker B2). The fix intersects the scored teams
// with EligibleJudgeTeamsIgnoringConflicts, read at the moment of leaving.
func TestHackLeaveDoesNotResurrectNonCountingScore(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvresorg")
	p1 := a.newPerson(t, "lvresp")
	judge := a.newPerson(t, "lvresj")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "TeamA")
	a.giveSubmittedEntry(t, slug, teamID)
	a.joinJudge(t, judgeCode, judge, "Judge")
	base := "/v1/hack/events/" + slug
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	// Open mode (the default): the judge is eligible for every team right
	// now, so this score genuinely counts.
	wantTS(t, "score", a.at(t, "PUT", base+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")
	// Manual mode with no assignment at all: the judge is no longer
	// eligible for this team, so the score they already cast stops
	// counting right now, before they ever leave.
	// SIM-416: judging setup changes only outside Judging.
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "set manual", a.at(t, "PATCH", base+"/judging/settings", map[string]any{"assignment_mode": "manual"}, a.key(org)), 200, "")

	eventID := a.eventID(t, slug)
	coverage := func() int {
		byTeam, _, err := entries.New(a.database, nil).ScoreCoverage(context.Background(), eventID)
		if err != nil {
			t.Fatal(err)
		}
		return byTeam[teamID]
	}
	before := coverage()
	if before != 0 {
		t.Fatalf("the score must already not be counting under manual mode with no assignment: coverage=%d", before)
	}

	wantTS(t, "judge leaves", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	if after := coverage(); after != before {
		t.Fatalf("BUG: leaving changed what counts: %d -> %d", before, after)
	}
}

// --- ship-blocker B3: Remove person vs self-leave deadlock -----------------

// --- finding 7: the leave preview took a write lock on the team row -------

// --- finding 8: no Cache-Control on either leave response -----------------

// --- finding 9: delete-former-judge-scores had no server-side confirm -----

func TestHackDeleteFormerJudgeScoresRequiresConfirm(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvdfc-org")
	p1 := a.newPerson(t, "lvdfc-p1")
	judge := a.newPerson(t, "lvdfc-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.giveSubmittedEntry(t, slug, teamID)
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")
	wantTS(t, "leave", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	judgeID := a.userID(t, judge)
	route := "/v1/hack/events/" + slug + "/judging/former-judges/" + judgeID
	wantTS(t, "no confirm field", a.at(t, "DELETE", route, map[string]any{}, a.key(org)), 409, "confirm_required")
	wantTS(t, "wrong confirm", a.at(t, "DELETE", route, map[string]any{"confirm": "not-the-slug"}, a.key(org)), 409, "confirm_required")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_former_judges WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("a missing or wrong confirm must not delete anything")
	}

	wantTS(t, "correct confirm", a.at(t, "DELETE", route, map[string]any{"confirm": slug}, a.key(org)), 204, "")
}

// --- small helpers ----------------------------------------------------------

func (a *hackApp) emailOf(t *testing.T, p person) string {
	t.Helper()
	return p.email
}
