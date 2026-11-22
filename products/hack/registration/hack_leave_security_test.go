package registration

import (
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// Regression tests for the Grok security review of SIM-340 (leave this
// event). Each test is named after the finding it covers.

// --- finding 2: delete-former-judge-scores skipped takedown and the ----
// --- caller's current organiser status under its own lock --------------

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
	org := a.NewPerson(t, "lvvoteorg")
	p1 := a.NewPerson(t, "lvvotep1")
	p2 := a.NewPerson(t, "lvvotep2")
	p3 := a.NewPerson(t, "lvvotep3")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "P1")
	a.JoinParticipant(t, join, p2, "P2")
	a.JoinParticipant(t, join, p3, "P3")
	a.CreateTeamAndJoin(t, slug, p1, "VoterTeam")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p2, "VotedForTeam")
	if _, err := a.DB.Exec(`UPDATE events SET voting_eligibility='participants' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	p1ID := a.UserID(t, p1)
	p3ID := a.UserID(t, p3)
	for _, voter := range []string{p1ID, p3ID} {
		if _, err := a.DB.Exec(`INSERT INTO event_votes (event_id, voter_id, team_id) VALUES ((SELECT id FROM events WHERE slug=$1), $2, $3)`,
			slug, voter, teamID); err != nil {
			t.Fatal(err)
		}
	}
	count := func(email string) int {
		var n int
		if err := a.DB.QueryRow(`SELECT count(*) FROM event_votes WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND voter_id=$2`, slug, email).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// p1 leaves: their own vote, cast while eligible, stands -- leaving
	// never touches votes, regardless of eligibility.
	hacktest.WantTS(t, "p1 leaves", a.Leave(t, slug, p1, map[string]any{"confirm": slug}), 200, "")
	if count(p1ID) != 1 {
		t.Fatal("a leaver's own vote must stand -- leaving never touches votes")
	}
	// p3 never joined a team and never leaves; p1's departure must not have
	// swept p3's vote either (the whole-event sweep ship-blocker B1).
	if count(p3ID) != 1 {
		t.Fatal("BUG: an unrelated person's leave deleted someone else's vote")
	}
}

// --- finding 4: RemoveCoOrganiser and removePerson skipped the new cleanup

// --- finding 5: rejoining as a participant kept the score rows ------------

// TestHackParticipantRejoinDeletesFormerScores: a judge who left (scores
// frozen in a former-judge record) and rejoins as a participant must have
// their event_scores rows deleted outright, not just the former-judge
// record -- otherwise a later re-invite as a judge brings the old rows back
// counting, un-frozen.
func TestHackParticipantRejoinDeletesFormerScores(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "lvrejoin-org")
	p1 := a.NewPerson(t, "lvrejoin-p1")
	judge := a.NewPerson(t, "lvrejoin-j")
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

	eventID := a.EventID(t, slug)
	judgeID := a.UserID(t, judge)
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("scores should still exist right after leaving, frozen by the former-judge record")
	}

	// Reopen sign-up so the former judge can rejoin as a participant.
	if _, err := a.DB.Exec(`UPDATE events SET stage='open' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	join2, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join2, judge, "Judge")

	if err := a.DB.QueryRow(`SELECT count(*) FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the former-judge record must be cleared on rejoin")
	}
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, judgeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("rejoining as a participant must delete the old event_scores rows, not just the former-judge record")
	}
}

// --- finding 6: a conflict removed after leaving never came back --------

// --- ship-blocker B2: leaving must not make a non-counting score count ----

// --- ship-blocker B3: Remove person vs self-leave deadlock -----------------

// --- finding 7: the leave preview took a write lock on the team row -------

// --- finding 8: no Cache-Control on either leave response -----------------

// --- finding 9: delete-former-judge-scores had no server-side confirm -----

// --- small helpers ----------------------------------------------------------
