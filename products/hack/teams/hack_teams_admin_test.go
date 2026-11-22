package teams

import (
	"context"
	"database/sql"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	"github.com/vsriram/simple-host/host/ops"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/products/hack/app/members"
)

// Teams redesign (owner 2026-10-31): team admins, locking, the team link,
// the Overview next_step and Results locking judging. The harness is
// newHackApp (DB_DSN), same as hack_test.go and hack_leave_test.go.

// --- 1: the creator is admin; a code or link joiner is not ----------------

// --- 2: resolving a request now needs a team admin -------------------------

func TestHackTeamRequestAdminGating(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "trgorg")
	leader := a.NewPerson(t, "trgleader")
	member := a.NewPerson(t, "trgmember")
	asker := a.NewPerson(t, "trgasker")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member, asker} {
		a.JoinParticipant(t, join, p, "P")
	}
	teamID, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	r := a.At(t, "POST", base+"/teams/alpha/requests", nil, a.Key(asker))
	if r.Status != 201 {
		t.Fatalf("ask: %d %s", r.Status, r.Body)
	}
	var id string
	if err := a.DB.QueryRow(`SELECT id FROM event_team_requests WHERE team_id=$1 AND kind='request'`, teamID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	hacktest.WantCode(t, "member accept", a.At(t, "POST", base+"/requests/"+id+"/accept", nil, a.Key(member)), 403, "team_admin_only")
	hacktest.WantCode(t, "member decline", a.At(t, "POST", base+"/requests/"+id+"/decline", nil, a.Key(member)), 403, "team_admin_only")
	hacktest.WantCode(t, "admin accept", a.At(t, "POST", base+"/requests/"+id+"/accept", nil, a.Key(leader)), 200, "")
	if a.TeamOf(t, asker) != teamID {
		t.Fatal("asker did not join")
	}
	// Invite: any teammate may send one; accepting it stays the invitee's own.
	other := a.NewPerson(t, "trgother")
	a.JoinParticipant(t, join, other, "Other")
	hacktest.WantCode(t, "other opts into looking", a.At(t, "PUT", base+"/me/looking", map[string]any{"looking": true}, a.Key(other)), 200, "")
	r = a.At(t, "POST", base+"/team/invites", map[string]any{"user_id": a.UserID(t, other)}, a.Key(member))
	hacktest.WantCode(t, "teammate invites", r, 201, "")
	var inviteID string
	if err := a.DB.QueryRow(`SELECT id FROM event_team_requests WHERE team_id=$1 AND kind='invite'`, teamID).Scan(&inviteID); err != nil {
		t.Fatal(err)
	}
	hacktest.WantCode(t, "invitee accepts own invite", a.At(t, "POST", base+"/requests/"+inviteID+"/accept", nil, a.Key(other)), 200, "")
	if a.TeamOf(t, other) != teamID {
		t.Fatal("invitee did not join")
	}
}

// --- 3: removing a member now needs a team admin ---------------------------

// --- 4: admin toggle and the last-admin invariant ---------------------------

func TestHackTeamAdminToggleAndLastAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "ltaorg")
	leader := a.NewPerson(t, "ltaleader")
	member := a.NewPerson(t, "ltamember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "member makes self admin", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, member)+"/admin", map[string]any{"admin": true}, a.Key(member)), 403, "team_admin_only")
	hacktest.WantCode(t, "leader makes member admin", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, member)+"/admin", map[string]any{"admin": true}, a.Key(leader)), 200, "")
	if !a.IsTeamAdmin(t, member) {
		t.Fatal("member not admin")
	}
	hacktest.WantCode(t, "member removes leader admin", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, leader)+"/admin", map[string]any{"admin": false}, a.Key(member)), 200, "")
	if a.IsTeamAdmin(t, leader) {
		t.Fatal("leader still admin")
	}
	// member is now the only admin: unmaking it is refused.
	hacktest.WantCode(t, "last admin refused", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, member)+"/admin", map[string]any{"admin": false}, a.Key(member)), 409, "last_team_admin")
	// A wrong user_id (not on the team) is 404, never 403.
	stranger := a.NewPerson(t, "ltastranger")
	hacktest.WantCode(t, "wrong user_id is not found", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, stranger)+"/admin", map[string]any{"admin": true}, a.Key(member)), 404, "")
}

// setMyTeammateAdmin follows the same participant stage and frozen-team
// gate as patch/lock/new-link (Codex review, 2026-11-01: it had neither).
func TestHackTeammateAdminStageAndFreezeChecks(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "ltafreezeorg")
	leader := a.NewPerson(t, "ltafreezeleader")
	member := a.NewPerson(t, "ltafreezemember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "to closed", a.SetStage(t, base, "closed", a.Key(org)), 200, "")
	hacktest.WantCode(t, "stage closed refuses admin toggle", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, member)+"/admin", map[string]any{"admin": true}, a.Key(leader)), 409, "teams_locked")
	hacktest.WantCode(t, "back to open", a.SetStage(t, base, "open", a.Key(org)), 200, "")
	if _, err := a.DB.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	hacktest.WantCode(t, "frozen team refuses admin toggle", a.At(t, "PUT", base+"/team/members/"+a.UserID(t, member)+"/admin", map[string]any{"admin": true}, a.Key(leader)), 409, "submissions_closed")
	if a.IsTeamAdmin(t, member) {
		t.Fatal("admin toggle applied despite the freeze")
	}
}

// --- 5: admin succession on every exit path ---------------------------------

// EnsureTeamAdmin picks the longest-standing remaining member
// (team_joined_at, then user_id); joining order here is leader, then
// member (admin stepped down or removed), so member becomes admin.
func TestHackTeamAdminSuccessionOnLeave(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "succleaveorg")
	leader := a.NewPerson(t, "succleaveleader")
	member := a.NewPerson(t, "succleavemember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "leader leaves team", a.At(t, "POST", base+"/teams/leave", nil, a.Key(leader)), 200, "")
	if !a.IsTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after leader left the team")
	}
}

func TestHackTeamAdminSuccessionOnOrganiserRemove(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "succremoveorg")
	leader := a.NewPerson(t, "succremoveleader")
	member := a.NewPerson(t, "succremovemember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	_, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "organiser removes leader", a.At(t, "DELETE", base+"/people/"+a.UserID(t, leader), nil, a.Key(org)), 204, "")
	if !a.IsTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after organiser removed leader")
	}
}

// --- 6: locking ---------------------------------------------------------

// --- 7: rename -----------------------------------------------------------

func TestHackTeamRename(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "renorg")
	leader := a.NewPerson(t, "renleader")
	member := a.NewPerson(t, "renmember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	teamID, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	other := a.NewPerson(t, "renother")
	a.JoinParticipant(t, join, other, "Other")
	a.CreateTeamAndJoin(t, slug, other, "Taken")
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "member rename", a.At(t, "PATCH", base+"/team", map[string]any{"name": "Nope"}, a.Key(member)), 403, "team_admin_only")
	hacktest.WantCode(t, "duplicate name", a.At(t, "PATCH", base+"/team", map[string]any{"name": "Taken"}, a.Key(leader)), 409, "team_name_taken")
	r := a.At(t, "PATCH", base+"/team", map[string]any{"name": "Renamed Alpha"}, a.Key(leader))
	hacktest.WantCode(t, "admin rename", r, 200, "")
	var name, slugCol string
	if err := a.DB.QueryRow(`SELECT name, slug FROM event_teams WHERE id=$1`, teamID).Scan(&name, &slugCol); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed Alpha" || slugCol != "alpha" {
		t.Fatalf("rename changed slug: name=%q slug=%q", name, slugCol)
	}
	if _, err := a.DB.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	hacktest.WantCode(t, "rename past deadline", a.At(t, "PATCH", base+"/team", map[string]any{"name": "Too Late"}, a.Key(leader)), 409, "submissions_closed")
}

// --- 8: new team link ------------------------------------------------------

func TestHackNewTeamLink(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "linkorg")
	leader := a.NewPerson(t, "linkleader")
	member := a.NewPerson(t, "linkmember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	teamID, oldCode := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, oldCode, member)
	base := "/v1/hack/events/" + slug
	hacktest.WantCode(t, "member rotates link", a.At(t, "POST", base+"/team/link", nil, a.Key(member)), 403, "team_admin_only")
	r := a.At(t, "POST", base+"/team/link", nil, a.Key(leader))
	hacktest.WantCode(t, "admin rotates link", r, 200, "")
	newCode := r.JSON(t)["code"].(string)
	if newCode == oldCode {
		t.Fatal("code did not change")
	}
	if got := a.TeamCode(t, teamID); got != newCode {
		t.Fatalf("stored code %q, want %q", got, newCode)
	}
	oldJoiner := a.NewPerson(t, "linkoldjoiner")
	a.JoinParticipant(t, join, oldJoiner, "Old Joiner")
	hacktest.WantCode(t, "old code join 404", a.At(t, "POST", base+"/teams/join", map[string]any{"code": oldCode}, a.Key(oldJoiner)), 404, "team_not_found")
	hacktest.WantCode(t, "old code preview 404", a.At(t, "GET", "/v1/hack/team-links/"+oldCode, nil, nil), 404, "team_link_not_found")
	newJoiner := a.NewPerson(t, "linknewjoiner")
	a.JoinParticipant(t, join, newJoiner, "New Joiner")
	hacktest.WantCode(t, "new code join", a.At(t, "POST", base+"/teams/join", map[string]any{"code": newCode}, a.Key(newJoiner)), 200, "")
}

// --- 9: joining with the full link string as the code ----------------------

// --- 10: deprecated open field ---------------------------------------------

// --- 11: listOpenTeams excludes locked, includes former code-only, full ----

// --- 12: results locks judging, back to judging unlocks ---------------------

// --- 13: next_step for every stage ------------------------------------------

// --- 14: new event team size default -----------------------------------

// --- 15: account erasure promotes a successor on every affected team -------

// Deleting a person's whole platform account (not just leaving a team)
// cascades event_members.user_id's ON DELETE CASCADE, which takes their
// membership -- including team_admin -- off every team they were on, with
// no hack write path and so no EnsureTeamAdmin call in between (Codex
// review, 2026-11-01). This proves EraseAccount now runs it itself for
// every team the erased account admins.
func TestHackAccountErasurePromotesNewAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "eraseorg")
	leader := a.NewPerson(t, "eraseleader")
	member := a.NewPerson(t, "erasemember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	for _, p := range []hacktest.Person{leader, member} {
		a.JoinParticipant(t, join, p, "P")
	}
	teamID, code := a.CreateTeamAndJoin(t, slug, leader, "Alpha")
	a.JoinTeamByCode(t, slug, code, member)
	leaderID := a.UserID(t, leader)

	accounts.SetGroupHooks(accounts.GroupHooks{Erasure: afterCascadeErasure{}})
	_, err := ops.Cutoff{DB: a.DB}.ErasePerson(context.Background(), accounts.DeleteAccountInput{ID: leaderID, Operator: true})
	if err != nil {
		t.Fatal(err)
	}

	var stillMember bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_members WHERE team_id = $1 AND user_id = $2)`, teamID, leaderID).Scan(&stillMember); err != nil {
		t.Fatal(err)
	}
	if stillMember {
		t.Fatal("erased account's own membership was not removed")
	}
	if !a.IsTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after leader's whole account was erased")
	}
}

// TestHackAccountErasureSoleMemberBeforeDeadlineDeletesTeam is Grok's Medium
// finding (security review, 2026-11-01): erasing the account of a team's
// only member must run the same path LeaveMembership runs for a voluntary
// last-member leave, not just EnsureTeamAdmin (which is a no-op with zero
// members left). Before the deadline, the team is not "keepable" and must
// be deleted outright, exactly as a self-leave would delete it, and its
// event's account surfaces in AffectedEventAccounts so the caller can sweep
// any orphan team site.
func TestHackAccountErasureSoleMemberBeforeDeadlineDeletesTeam(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "erasesoloorg")
	solo := a.NewPerson(t, "erasesolomember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, solo, "Solo")
	teamID, _ := a.CreateTeamAndJoin(t, slug, solo, "SoloTeam")
	soloID := a.UserID(t, solo)

	var eventAccountID string
	if err := a.DB.QueryRow(`SELECT e.account_id FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&eventAccountID); err != nil {
		t.Fatal(err)
	}

	accounts.SetGroupHooks(accounts.GroupHooks{Erasure: afterCascadeErasure{}})
	out, err := ops.Cutoff{DB: a.DB}.ErasePerson(context.Background(), accounts.DeleteAccountInput{ID: soloID, Operator: true})
	if err != nil {
		t.Fatal(err)
	}
	erased := out.Erased

	var stillExists bool
	if err := a.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, teamID).Scan(&stillExists); err != nil {
		t.Fatal(err)
	}
	if stillExists {
		t.Fatal("a sole member's erased team must be deleted outright before the deadline, same as a self-leave")
	}
	found := false
	for _, id := range erased.AffectedEventAccounts {
		if id == eventAccountID {
			found = true
		}
	}
	if !found {
		t.Fatalf("AffectedEventAccounts must include the event's account so orphan team sites get swept, got %v", erased.AffectedEventAccounts)
	}
}

// TestHackAccountErasureSoleMemberAfterDeadlineKeepsAndLocksTeam is the other
// half of the same Grok finding: past the deadline, the team is not deleted
// (its submission must survive for judging/results, same as a self-leave) --
// it is marked kept_after_leave, locked for good, and its code rotated so
// the dead account's old link cannot hand the empty team to anyone else.
func TestHackAccountErasureSoleMemberAfterDeadlineKeepsAndLocksTeam(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "erasesolodlorg")
	solo := a.NewPerson(t, "erasesolodlmember")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, solo, "Solo")
	teamID, oldCode := a.CreateTeamAndJoin(t, slug, solo, "SoloTeamDL")
	soloID := a.UserID(t, solo)

	base := "/v1/hack/events/" + slug
	// Through the real stage-transition route, same as
	// TestHackLeaveFrozenLoneMemberKeepsTeamEntryAndScores: setStage forces
	// submission_deadline to now() the instant the stage becomes closed, so
	// TeamKeepableSQL/TeamFrozenForLeave agree with what a raw UPDATE would not.
	hacktest.WantTS(t, "close", a.SetStage(t, base, "closed", a.Key(org)), 200, "")

	accounts.SetGroupHooks(accounts.GroupHooks{Erasure: afterCascadeErasure{}})
	_, err := ops.Cutoff{DB: a.DB}.ErasePerson(context.Background(), accounts.DeleteAccountInput{ID: soloID, Operator: true})
	if err != nil {
		t.Fatal(err)
	}

	var kept, locked bool
	var newCode string
	if err := a.DB.QueryRow(`SELECT kept_after_leave, locked_at IS NOT NULL, code FROM event_teams WHERE id = $1`, teamID).Scan(&kept, &locked, &newCode); err != nil {
		t.Fatal(err)
	}
	if !kept {
		t.Fatal("erasing a frozen team's sole member must set kept_after_leave, so judging/results keep listing it")
	}
	if !locked {
		t.Fatal("a kept team must be locked for good")
	}
	if newCode == oldCode {
		t.Fatal("the dead account's old team code must be rotated, so it can't hand the empty team to anyone else")
	}
	var members int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_members WHERE team_id = $1`, teamID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("team should now have no members, got %d", members)
	}
}

// These fixtures isolate the after-cascade cleanup, as their old
// HoldingAccounts:false input did before the hook extraction.
type afterCascadeErasure struct{ members.AccountErasure }

func (afterCascadeErasure) Block(context.Context, *sql.Tx, string, bool) (string, string, error) {
	return "", "", nil
}
