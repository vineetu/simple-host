package handler

import (
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/db"
)

// Regression tests for the Grok security review of SIM-340 (leave this
// event). Each test is named after the finding it covers.

// --- finding 1: same-user deadlock minting a team key while leaving -------

// TestHackMakeTeamKeyLocksEventFirst proves makeTeamKey now takes the event
// row FOR SHARE before the member row FOR UPDATE, the same order leave
// (event FOR UPDATE, then member) uses. Before the fix, makeTeamKey never
// touched the event row until ReplaceTeamKey's INSERT into event_team_keys,
// whose foreign key takes an implicit FOR KEY SHARE on events -- so a
// concurrent leave by the same person, holding the event row FOR UPDATE and
// waiting on the same member row, could cycle with it at that point.
//
// Forced ordering: hold the event row FOR UPDATE from a third connection
// (as leave would mid-transaction), start makeTeamKey, and poll
// pg_stat_activity for it waiting specifically on GetEventForShare's own
// query (wait_event_type='Lock', matched by query text -- a row-lock wait
// shows up there, not as a relation-level pg_locks row, and other
// connections in this shared, busy database can be waiting on something
// else at the same moment too). The lock is always released and the
// request always drained before any assertion fires, so a failing run
// reports cleanly instead of leaving the background request -- and this
// test -- hanging against a lock nothing will ever release.
func TestHackMakeTeamKeyLocksEventFirst(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvkdorg")
	p1 := a.newPerson(t, "lvkdp1")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "P1")
	a.createTeamAndJoin(t, slug, p1, "KeyTeam")

	eventID := a.eventID(t, slug)
	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}

	keyDone := make(chan resp, 1)
	go func() { keyDone <- a.at(t, "POST", "/v1/hack/events/"+slug+"/key", nil, a.key(p1)) }()

	waiting, finishedEarly := false, false
	deadline := time.Now().Add(2 * time.Second)
poll:
	for time.Now().Before(deadline) {
		select {
		case <-keyDone:
			finishedEarly = true
			break poll
		default:
		}
		var n int
		if err := a.database.QueryRow(`
			SELECT count(*) FROM pg_stat_activity
			 WHERE wait_event_type = 'Lock' AND query ILIKE '%FROM events WHERE id = $1 FOR SHARE%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			waiting = true
			break poll
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Release unconditionally, before any assertion: a Fatal below must not
	// leave the goroutine's request blocked on a lock this test alone holds.
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-keyDone

	if finishedEarly {
		t.Fatalf("makeTeamKey finished (status %d) before ever waiting on the event row's lock -- "+
			"it must lock the event row before the member row, same order as leave, or two concurrent "+
			"requests from the same person (leave, and mint/rotate a key) can deadlock", r.status)
	}
	if !waiting {
		t.Fatal("makeTeamKey never showed up waiting on the event row's lock")
	}
	if r.status != 201 {
		t.Fatalf("makeTeamKey after the lock released: %d %s", r.status, r.body)
	}
}

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
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
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
	wantHackErr(t, "delete-scores after takedown wins the race", r, 403, "event_taken_down", "this event has been taken down")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("scores must survive a delete that a takedown beat to the lock")
	}
}

// TestHackDeleteFormerJudgeScoresRaceOrganiserRemoved is the same shape for
// the caller losing organiser status: a remove-organiser that commits while
// delete-former-judge-scores is waiting on the event lock must stop the
// delete too, not just the pre-transaction loadMember check.
func TestHackDeleteFormerJudgeScoresRaceOrganiserRemoved(t *testing.T) {
	a := newHackApp(t)
	creator := a.newPerson(t, "lvdfro-cr")
	ousted := a.newPerson(t, "lvdfro-co")
	p1 := a.newPerson(t, "lvdfro-p1")
	judge := a.newPerson(t, "lvdfro-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, creator, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(creator))
	wantTS(t, "invite", inviteR, 201, "")
	code := inviteR.json(t)["url"].(string)
	wantTS(t, "accept", a.at(t, "POST", "/v1/hack/organiser/"+code[len(code)-32:], map[string]any{"accept_coc": true, "display_name": "Ousted"}, a.key(ousted)), 200, "")
	a.openEvent(t, creator, slug)
	join, judgeCode := a.joinCodes(t, creator, slug)
	a.joinParticipant(t, join, p1, "Pat")
	teamID, _ := a.createTeamAndJoin(t, slug, p1, "Delendum")
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.key(judge)), 200, "")
	wantTS(t, "judge leaves", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)
	oustedID := a.userID(t, ousted)

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
		deleteDone <- a.at(t, "DELETE", "/v1/hack/events/"+slug+"/judging/former-judges/"+judgeID, map[string]any{"confirm": slug}, a.key(ousted))
	}()
	waitBlocked()

	// What the creator removing "ousted" leaves behind, directly through
	// lockTx (the main pool would block on the same lock lockTx holds).
	if _, err := lockTx.Exec(`DELETE FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, oustedID); err != nil {
		t.Fatal(err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatal(err)
	}

	r := <-deleteDone
	wantTS(t, "delete-scores after the caller's own removal wins the race", r, 404, "event_not_found")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("scores must survive a delete issued by someone who stopped being an organiser mid-request")
	}
}

// --- finding 3: a leaver's vote outlives their membership -----------------

// TestHackLeaveRemovesIneligibleVote: with voting_eligibility set to
// "participants", a participant's vote is tied to their membership --
// leaving must remove it, the same way the organiser's own eligibility
// switch (IneligibleVotes) already would.
func TestHackLeaveRemovesIneligibleVote(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvvoteorg")
	p1 := a.newPerson(t, "lvvotep1")
	p2 := a.newPerson(t, "lvvotep2")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "P1")
	a.joinParticipant(t, join, p2, "P2")
	a.createTeamAndJoin(t, slug, p1, "VoterTeam")
	teamID, _ := a.createTeamAndJoin(t, slug, p2, "VotedForTeam")
	if _, err := a.database.Exec(`UPDATE events SET voting_eligibility='participants' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	voterEmail := db.BaseEmail(a.emailOf(t, p1))
	if _, err := a.database.Exec(`INSERT INTO event_votes (event_id, voter_email, team_id) VALUES ((SELECT id FROM events WHERE slug=$1), $2, $3)`,
		slug, voterEmail, teamID); err != nil {
		t.Fatal(err)
	}

	wantTS(t, "leave", a.leave(t, slug, p1, map[string]any{"confirm": slug}), 200, "")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_votes WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND voter_email=$2`, slug, voterEmail).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a leaver's vote must not outlive their membership under participants eligibility")
	}
}

// TestHackLeaveKeepsVoteWhenAllSignedIn: eligibility "all_signed_in" ties a
// ballot to being signed in generally, not to membership, so leaving must
// not remove it.
func TestHackLeaveKeepsVoteWhenAllSignedIn(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvvoteasi-org")
	p1 := a.newPerson(t, "lvvoteasi-p1")
	p2 := a.newPerson(t, "lvvoteasi-p2")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "P1")
	a.joinParticipant(t, join, p2, "P2")
	a.createTeamAndJoin(t, slug, p1, "VoterTeam2")
	teamID, _ := a.createTeamAndJoin(t, slug, p2, "VotedForTeam2")
	// all_signed_in is the schema default; set it explicitly so the test
	// does not depend on that default never changing.
	if _, err := a.database.Exec(`UPDATE events SET voting_eligibility='all_signed_in' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	voterEmail := db.BaseEmail(a.emailOf(t, p1))
	if _, err := a.database.Exec(`INSERT INTO event_votes (event_id, voter_email, team_id) VALUES ((SELECT id FROM events WHERE slug=$1), $2, $3)`,
		slug, voterEmail, teamID); err != nil {
		t.Fatal(err)
	}

	wantTS(t, "leave", a.leave(t, slug, p1, map[string]any{"confirm": slug}), 200, "")

	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_votes WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND voter_email=$2`, slug, voterEmail).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("a vote under all_signed_in eligibility must stand after the voter leaves")
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
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
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
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
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
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
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

// TestHackFormerJudgeConflictReversible: counted_team_ids must include
// every team the judge actually scored, even one they had a live conflict
// on at the moment they left -- the conflict is the only thing that should
// exclude it, checked live on every read, so removing the conflict later
// brings that team's score back into coverage.
func TestHackFormerJudgeConflictReversible(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvconf-org")
	p1 := a.newPerson(t, "lvconf-p1")
	p2 := a.newPerson(t, "lvconf-p2")
	judge := a.newPerson(t, "lvconf-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	a.joinParticipant(t, join, p2, "Quinn")
	teamX, _ := a.createTeamAndJoin(t, slug, p1, "TeamX")
	teamY, _ := a.createTeamAndJoin(t, slug, p2, "TeamY")
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score X", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamX,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")
	wantTS(t, "score Y", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamY,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.key(judge)), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)
	// A conflict declared on X by mistake, in place when the judge leaves.
	if _, err := a.database.Exec(`INSERT INTO event_conflicts (event_id, judge_id, team_id) VALUES ($1, $2, $3)`, eventID, judgeID, teamX); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "leave", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	var countedIDs []string
	if err := a.database.QueryRow(`SELECT counted_team_ids FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(pq.Array(&countedIDs)); err != nil {
		t.Fatal(err)
	}
	has := func(id string) bool {
		for _, c := range countedIDs {
			if c == id {
				return true
			}
		}
		return false
	}
	if !has(teamX) || !has(teamY) {
		t.Fatalf("counted_team_ids must include every scored team regardless of a conflict at leave time: %v", countedIDs)
	}

	// With the conflict still in place, X must not count (live check).
	dash := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
	teamsByID := map[string]map[string]any{}
	for _, raw := range dash["teams"].([]any) {
		team := raw.(map[string]any)
		teamsByID[team["team_id"].(string)] = team
	}
	if teamsByID[teamX]["judges_scored"] != float64(0) {
		t.Fatalf("X should not count while the conflict stands: %v", teamsByID[teamX])
	}
	if teamsByID[teamY]["judges_scored"] != float64(1) {
		t.Fatalf("Y should count: %v", teamsByID[teamY])
	}

	// Remove the conflict: X must come back, because it was never dropped
	// from counted_team_ids to begin with.
	wantTS(t, "remove conflict", a.at(t, "DELETE", "/v1/hack/events/"+slug+"/conflicts/"+teamX+"?judge_user_id="+judgeID, nil, a.key(org)), 204, "")
	dash2 := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
	for _, raw := range dash2["teams"].([]any) {
		team := raw.(map[string]any)
		if team["team_id"] == teamX {
			if team["judges_scored"] != float64(1) {
				t.Fatalf("X should count again once the conflict is gone: %v", team)
			}
		}
	}
}

// --- finding 7: the leave preview took a write lock on the team row -------

// TestHackLeavePreviewDoesNotLockTeam: GET .../leave must not block behind
// another transaction's FOR UPDATE on the participant's team row -- that
// write lock belongs to POST only.
func TestHackLeavePreviewDoesNotLockTeam(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvprev-org")
	p1 := a.newPerson(t, "lvprev-p1")
	p2 := a.newPerson(t, "lvprev-p2")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "P1")
	a.joinParticipant(t, join, p2, "P2")
	teamID, code := a.createTeamAndJoin(t, slug, p1, "PreviewTeam")
	_ = code

	lockTx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := lockTx.QueryRow(`SELECT id FROM event_teams WHERE id=$1 FOR UPDATE`, teamID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback()

	previewDone := make(chan resp, 1)
	go func() { previewDone <- a.previewLeave(t, slug, p1) }()

	select {
	case r := <-previewDone:
		if r.status != 200 {
			t.Fatalf("preview: %d %s", r.status, r.body)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("GET .../leave blocked behind another transaction's lock on the team row -- it must use a non-locking read")
	}
}

// --- finding 8: no Cache-Control on either leave response -----------------

func TestHackLeaveResponsesAreNoStore(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvcc-org")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")

	get := a.at(t, "GET", "/v1/hack/events/"+slug+"/leave", nil, a.key(org))
	if cc := get.header.Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("GET .../leave Cache-Control: got %q", cc)
	}

	co := a.newPerson(t, "lvcc-co")
	base := "/v1/hack/events/" + slug
	inviteR := a.at(t, "POST", base+"/organiser-invite", nil, a.key(org))
	code := inviteR.json(t)["url"].(string)
	wantTS(t, "accept", a.at(t, "POST", "/v1/hack/organiser/"+code[len(code)-32:], map[string]any{"accept_coc": true, "display_name": "Co"}, a.key(co)), 200, "")

	post := a.leave(t, slug, co, map[string]any{"confirm": slug})
	if post.status != 200 {
		t.Fatalf("leave: %d %s", post.status, post.body)
	}
	if cc := post.header.Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("POST .../leave Cache-Control: got %q", cc)
	}
}

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
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamID,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")
	wantTS(t, "leave", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	judgeID := a.userID(t, judge)
	route := "/v1/hack/events/" + slug + "/judging/former-judges/" + judgeID
	wantHackErr(t, "no confirm field", a.at(t, "DELETE", route, map[string]any{}, a.key(org)), 409, "confirm_required",
		`to delete this judge's scores, send {"confirm":"`+slug+`"} with this event's address name`)
	wantHackErr(t, "wrong confirm", a.at(t, "DELETE", route, map[string]any{"confirm": "not-the-slug"}, a.key(org)), 409, "confirm_required",
		`to delete this judge's scores, send {"confirm":"`+slug+`"} with this event's address name`)

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
