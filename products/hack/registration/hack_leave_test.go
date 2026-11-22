package registration

import (
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// Leave this event (SIM-340). The harness is newHackApp (DB_DSN), same as
// the other hack tests.

// --- 1: participant on a team with others ---------------------------------

// --- 2: lone member's team is deleted --------------------------------------

func TestHackLeaveLoneParticipantDeletesTeam(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "lv2org")
	p1 := a.NewPerson(t, "lv2p1")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Solo")
	teamID, _ := a.CreateTeamAndJoin(t, slug, p1, "LoneTeam")

	prev := a.PreviewLeave(t, slug, p1).JSON(t)
	team := prev["team"].(map[string]any)
	if team["deleted_on_leave"] != true || team["others_count"] != float64(0) {
		t.Fatalf("preview lone team: %v", team)
	}

	out := a.Leave(t, slug, p1, map[string]any{"confirm": slug}).JSON(t)
	if out["team_deleted"] != true {
		t.Fatalf("leave response: %v", out)
	}
	var stillTeam bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if stillTeam {
		t.Fatal("lone team should be deleted")
	}
}

// --- 3: frozen team with a teammate, and a teamless participant in a late
// stage. Owner decision 2026-10-31: past the deadline leaving is still
// allowed -- it just never deletes, empties-and-keeps or otherwise changes
// the team, its entry, its site or its scores. -----------------------------

func TestHackLeaveParticipantFrozenAndTeamless(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "lv3org")
	p1, p2 := a.NewPerson(t, "lv3p1"), a.NewPerson(t, "lv3p2")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Ayu")
	a.JoinParticipant(t, join, p2, "Nadia")
	teamID, code := a.CreateTeamAndJoin(t, slug, p1, "FrozenTeam")
	a.JoinTeamByCode(t, slug, code, p2)

	if _, err := a.DB.Exec(`UPDATE events SET submission_deadline = now() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}

	prev := a.PreviewLeave(t, slug, p1).JSON(t)
	if prev["allowed"] != true || prev["blocked"] != nil {
		t.Fatalf("frozen preview should not be blocked: %v", prev)
	}
	team := prev["team"].(map[string]any)
	if team["frozen"] != true || team["deleted_on_leave"] != false || team["others_count"] != float64(1) {
		t.Fatalf("frozen preview team: %v", team)
	}

	out := a.Leave(t, slug, p1, map[string]any{"confirm": slug}).JSON(t)
	if out["left"] != true || out["case"] != "participant" || out["team_deleted"] != false {
		t.Fatalf("frozen leave: %v", out)
	}
	p1ID := a.UserID(t, p1)
	if a.IsMember(t, slug, p1ID) {
		t.Fatal("p1 should no longer be a member")
	}
	if !a.IsMember(t, slug, a.UserID(t, p2)) {
		t.Fatal("p2 should remain a member")
	}
	var stillTeam bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id=$1)`, teamID).Scan(&stillTeam); err != nil {
		t.Fatal(err)
	}
	if !stillTeam {
		t.Fatal("frozen team must survive leaving even past the deadline")
	}

	// A teamless participant is never frozen by the team deadline: move them
	// to Judging and confirm they can still leave.
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	p3 := a.NewPerson(t, "lv3p3")
	// Join link only works while open/building; put the event back briefly.
	if _, err := a.DB.Exec(`UPDATE events SET stage='open' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	a.JoinParticipant(t, join, p3, "NoTeam")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	outTeamless := a.Leave(t, slug, p3, map[string]any{"confirm": slug}).JSON(t)
	if outTeamless["left"] != true || outTeamless["case"] != "participant" {
		t.Fatalf("teamless leave: %v", outTeamless)
	}
}

// --- 4: frozen lone member keeps the team, entry and scores, with the
// published snapshot's totals and ranks identical before and after -- the
// case the ListTeamsWithMembers fix exists for: a frozen team emptied by its
// last member leaving must still be judged and ranked exactly as if they
// hadn't left. ----------------------------------------------------------

// --- 5/6: pending applicant withdraws; a rejected applicant can now leave
// too (owner decision 2026-10-31, superseding the earlier "rejected can
// never leave" rule): same mechanics as a withdraw -- their row is deleted
// and they can apply again, where the organiser decides again. -----------

func TestHackLeaveApplicantWithdrawAndRejectedCanLeave(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "lv5org")
	pending, rejected := a.NewPerson(t, "lv5pend"), a.NewPerson(t, "lv5rej")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	hacktest.WantTS(t, "apply pending", a.Apply(t, slug, pending, nil), 200, "")
	hacktest.WantTS(t, "apply rejected", a.Apply(t, slug, rejected, nil), 200, "")
	hacktest.WantTS(t, "reject", a.At(t, "POST", "/v1/hack/events/"+slug+"/applications/"+a.UserID(t, rejected)+"/decision", map[string]string{"decision": "rejected"}, a.Key(org)), 200, "")

	prev := a.PreviewLeave(t, slug, pending).JSON(t)
	if prev["case"] != "applicant" || prev["allowed"] != true {
		t.Fatalf("pending preview: %v", prev)
	}
	out := a.Leave(t, slug, pending, map[string]any{"confirm": slug}).JSON(t)
	if out["left"] != true || out["case"] != "applicant" {
		t.Fatalf("withdraw: %v", out)
	}
	if a.IsMember(t, slug, a.UserID(t, pending)) {
		t.Fatal("withdrawn applicant should be gone")
	}
	hacktest.WantTS(t, "re-apply", a.Apply(t, slug, pending, nil), 200, "")

	// A rejected applicant is the same "applicant" case as a pending one: no
	// special refusal left, and leaving deletes the row just like a withdraw.
	rejPrev := a.PreviewLeave(t, slug, rejected).JSON(t)
	if rejPrev["case"] != "applicant" || rejPrev["allowed"] != true {
		t.Fatalf("rejected preview: %v", rejPrev)
	}
	rejOut := a.Leave(t, slug, rejected, map[string]any{"confirm": slug}).JSON(t)
	if rejOut["left"] != true || rejOut["case"] != "applicant" {
		t.Fatalf("rejected leave: %v", rejOut)
	}
	if a.IsMember(t, slug, a.UserID(t, rejected)) {
		t.Fatal("rejected applicant's row should be gone after leaving")
	}
	// No extra machinery blocks re-applying: the organiser decides again.
	hacktest.WantTS(t, "re-apply after rejection", a.Apply(t, slug, rejected, nil), 200, "")
}

// --- 7/8: judge keeps counted scores, rejoin clears the record ------------

// --- 10/11: co-organiser leaves, creator must hand over --------------------

// --- 12: last organiser ends or deletes ------------------------------------

func TestHackLeaveLastOrganiserDelete(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "lv12dorg")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	accountID := a.EventAccount(t, slug)

	out := a.Leave(t, slug, org, map[string]any{"confirm": slug, "close": "delete"}).JSON(t)
	if out["event"] != "deleted" || out["left"] != true {
		t.Fatalf("delete leave: %v", out)
	}
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM events WHERE slug=$1`, slug).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("event should be gone")
	}
	if err := a.DB.QueryRow(`SELECT count(*) FROM users WHERE id=$1`, accountID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("holding account should be gone")
	}
}

// --- 13/14: stale fields, confirm ------------------------------------------

// --- 15/16: ended, taken down, not a member --------------------------------

// --- 20: the former-judge delete-scores route ------------------------------

// --- 17/18: races --------------------------------------------------------

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
	orgA := a.NewPerson(t, "lvsepa")
	p1 := a.NewPerson(t, "lvsepp")
	judge := a.NewPerson(t, "lvsepj")
	slugA := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create A", a.CreateEvent(t, orgA, slugA, nil), 201, "")
	a.OpenEvent(t, orgA, slugA)
	joinA, judgeCodeA := a.JoinCodes(t, orgA, slugA)
	a.JoinParticipant(t, joinA, p1, "Pat")
	teamID, _ := a.CreateTeamAndJoin(t, slugA, p1, "Delendum")
	a.GiveSubmittedEntry(t, slugA, teamID)
	a.JoinJudge(t, judgeCodeA, judge, "Judge")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slugA); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slugA)
	hacktest.WantTS(t, "score", a.PutScores(t, "/v1/hack/events/"+slugA, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.Key(judge)), 200, "")
	hacktest.WantTS(t, "leave A", a.Leave(t, slugA, judge, map[string]any{"confirm": slugA}), 200, "")

	orgB := a.NewPerson(t, "lvsepb")
	slugB := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create B", a.CreateEvent(t, orgB, slugB, nil), 201, "")
	a.OpenEvent(t, orgB, slugB)
	_, judgeCodeB := a.JoinCodes(t, orgB, slugB)
	a.JoinJudge(t, judgeCodeB, judge, "Judge")

	prev := a.PreviewLeave(t, slugB, judge).JSON(t)
	if prev["scored_teams"] != float64(0) {
		t.Fatalf("event B's preview must not see event A's former-judge scores: %v", prev)
	}
}
