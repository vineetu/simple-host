package hackhttp

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
)

// Teams redesign (owner 2026-10-31): team admins, locking, the team link,
// the Overview next_step and Results locking judging. The harness is
// newHackApp (DB_DSN), same as hack_test.go and hack_leave_test.go.

func (a *hackApp) teamSlugByID(t *testing.T, teamID string) string {
	t.Helper()
	var slug string
	if err := a.database.QueryRow(`SELECT slug FROM event_teams WHERE id = $1`, teamID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	return slug
}

func (a *hackApp) teamCode(t *testing.T, teamID string) string {
	t.Helper()
	var code string
	if err := a.database.QueryRow(`SELECT code FROM event_teams WHERE id = $1`, teamID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	return code
}

func (a *hackApp) teamLocked(t *testing.T, teamID string) bool {
	t.Helper()
	var locked bool
	if err := a.database.QueryRow(`SELECT locked_at IS NOT NULL FROM event_teams WHERE id = $1`, teamID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	return locked
}

func (a *hackApp) isTeamAdmin(t *testing.T, p person) bool {
	t.Helper()
	var admin bool
	if err := a.database.QueryRow(`SELECT team_admin FROM event_members WHERE user_id = $1`, a.userID(t, p)).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	return admin
}

func (a *hackApp) teamOf(t *testing.T, p person) string {
	t.Helper()
	var team sql.NullString
	if err := a.database.QueryRow(`SELECT team_id FROM event_members WHERE user_id = $1`, a.userID(t, p)).Scan(&team); err != nil {
		t.Fatal(err)
	}
	return team.String
}

// --- 1: the creator is admin; a code or link joiner is not ----------------

func TestHackTeamCreatorIsAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "tcaorg")
	leader := a.newPerson(t, "tcaleader")
	joiner := a.newPerson(t, "tcajoiner")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	a.joinParticipant(t, join, joiner, "Joiner")
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	if !a.isTeamAdmin(t, leader) {
		t.Fatal("creator is not admin")
	}
	view := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(leader)).json(t)
	team := view["me"].(map[string]any)["team"].(map[string]any)
	if team["admin"] != true || team["locked"] != false || team["kept"] != false {
		t.Fatalf("leader's team view: %v", team)
	}
	link, _ := team["link"].(string)
	if !strings.HasSuffix(link, "/team/"+code) {
		t.Fatalf("team link: %v", team)
	}
	a.joinTeamByCode(t, slug, code, joiner)
	if a.isTeamAdmin(t, joiner) {
		t.Fatal("code joiner became admin")
	}
	joinerView := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(joiner)).json(t)
	joinerTeam := joinerView["me"].(map[string]any)["team"].(map[string]any)
	if joinerTeam["admin"] != false {
		t.Fatalf("joiner admin flag: %v", joinerTeam)
	}
	for _, m := range joinerTeam["members"].([]any) {
		mm := m.(map[string]any)
		wantAdmin := mm["user_id"] == a.userID(t, leader)
		if mm["admin"] != wantAdmin {
			t.Fatalf("member admin flags: %v", joinerTeam["members"])
		}
	}
	_ = teamID
}

// --- 2: resolving a request now needs a team admin -------------------------

func TestHackTeamRequestAdminGating(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "trgorg")
	leader := a.newPerson(t, "trgleader")
	member := a.newPerson(t, "trgmember")
	asker := a.newPerson(t, "trgasker")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member, asker} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	r := a.at(t, "POST", base+"/teams/alpha/requests", nil, a.key(asker))
	if r.status != 201 {
		t.Fatalf("ask: %d %s", r.status, r.body)
	}
	var id string
	if err := a.database.QueryRow(`SELECT id FROM event_team_requests WHERE team_id=$1 AND kind='request'`, teamID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "member accept", a.at(t, "POST", base+"/requests/"+id+"/accept", nil, a.key(member)), 403, "team_admin_only")
	wantCode(t, "member decline", a.at(t, "POST", base+"/requests/"+id+"/decline", nil, a.key(member)), 403, "team_admin_only")
	wantCode(t, "admin accept", a.at(t, "POST", base+"/requests/"+id+"/accept", nil, a.key(leader)), 200, "")
	if a.teamOf(t, asker) != teamID {
		t.Fatal("asker did not join")
	}
	// Invite: any teammate may send one; accepting it stays the invitee's own.
	other := a.newPerson(t, "trgother")
	a.joinParticipant(t, join, other, "Other")
	wantCode(t, "other opts into looking", a.at(t, "PUT", base+"/me/looking", map[string]any{"looking": true}, a.key(other)), 200, "")
	r = a.at(t, "POST", base+"/team/invites", map[string]any{"user_id": a.userID(t, other)}, a.key(member))
	wantCode(t, "teammate invites", r, 201, "")
	var inviteID string
	if err := a.database.QueryRow(`SELECT id FROM event_team_requests WHERE team_id=$1 AND kind='invite'`, teamID).Scan(&inviteID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "invitee accepts own invite", a.at(t, "POST", base+"/requests/"+inviteID+"/accept", nil, a.key(other)), 200, "")
	if a.teamOf(t, other) != teamID {
		t.Fatal("invitee did not join")
	}
}

// --- 3: removing a member now needs a team admin ---------------------------

func TestHackTeamRemovalAdminGating(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "trmorg")
	leader := a.newPerson(t, "trmleader")
	member := a.newPerson(t, "trmmember")
	other := a.newPerson(t, "trmother")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member, other} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	a.joinTeamByCode(t, slug, code, other)
	base := "/v1/hack/events/" + slug
	wantCode(t, "member removes", a.at(t, "DELETE", base+"/team/members/"+a.userID(t, other), nil, a.key(member)), 403, "team_admin_only")
	// Admin removes self is refused (self-removal; use leave instead).
	wantCode(t, "admin removes self", a.at(t, "DELETE", base+"/team/members/"+a.userID(t, leader), nil, a.key(leader)), 403, "")
	r := a.at(t, "DELETE", base+"/team/members/"+a.userID(t, other), nil, a.key(leader))
	wantCode(t, "admin removes member", r, 204, "")
	if a.teamOf(t, other) != "" {
		t.Fatal("removed member still on team")
	}
	notice := a.at(t, "GET", base, nil, a.key(other)).json(t)["me"].(map[string]any)["team_removal_notice"]
	if !strings.Contains(notice.(string), "took you off Alpha") {
		t.Fatalf("notice: %v", notice)
	}
	oldCode := code
	r = a.at(t, "POST", base+"/teams/join", map[string]any{"code": oldCode}, a.key(other))
	wantCode(t, "old code rotated", r, 404, "team_not_found")
}

// --- 4: admin toggle and the last-admin invariant ---------------------------

func TestHackTeamAdminToggleAndLastAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "ltaorg")
	leader := a.newPerson(t, "ltaleader")
	member := a.newPerson(t, "ltamember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	wantCode(t, "member makes self admin", a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": true}, a.key(member)), 403, "team_admin_only")
	wantCode(t, "leader makes member admin", a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": true}, a.key(leader)), 200, "")
	if !a.isTeamAdmin(t, member) {
		t.Fatal("member not admin")
	}
	wantCode(t, "member removes leader admin", a.at(t, "PUT", base+"/team/members/"+a.userID(t, leader)+"/admin", map[string]any{"admin": false}, a.key(member)), 200, "")
	if a.isTeamAdmin(t, leader) {
		t.Fatal("leader still admin")
	}
	// member is now the only admin: unmaking it is refused.
	wantCode(t, "last admin refused", a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": false}, a.key(member)), 409, "last_team_admin")
	// A wrong user_id (not on the team) is 404, never 403.
	stranger := a.newPerson(t, "ltastranger")
	wantCode(t, "wrong user_id is not found", a.at(t, "PUT", base+"/team/members/"+a.userID(t, stranger)+"/admin", map[string]any{"admin": true}, a.key(member)), 404, "")
}

// setMyTeammateAdmin follows the same participant stage and frozen-team
// gate as patch/lock/new-link (Codex review, 2026-11-01: it had neither).
func TestHackTeammateAdminStageAndFreezeChecks(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "ltafreezeorg")
	leader := a.newPerson(t, "ltafreezeleader")
	member := a.newPerson(t, "ltafreezemember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	wantCode(t, "to closed", a.at(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, a.key(org)), 200, "")
	wantCode(t, "stage closed refuses admin toggle", a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": true}, a.key(leader)), 409, "teams_locked")
	wantCode(t, "back to open", a.at(t, "POST", base+"/stage", map[string]string{"stage": "open"}, a.key(org)), 200, "")
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "frozen team refuses admin toggle", a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": true}, a.key(leader)), 409, "submissions_closed")
	if a.isTeamAdmin(t, member) {
		t.Fatal("admin toggle applied despite the freeze")
	}
}

// Two admins each trying to unmake the other at once must leave at least
// one admin standing (security review 3, 4: the last-admin invariant under
// concurrency, serialized by the team row lock).
func TestHackTeamConcurrentAdminRemovalLeavesOneAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "ccorg")
	leader := a.newPerson(t, "ccleader")
	member := a.newPerson(t, "ccmember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	wantCode(t, "make member admin", a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": true}, a.key(leader)), 200, "")
	// A removes B's admin while B, at the same moment, removes A's: each
	// request locks its own caller's row then the team row (lockCallerTeam),
	// then SetTeamAdmin's own lock-then-update touches the OTHER party's
	// row. Before the lock-order fix (Codex review, 2026-11-01), A holds
	// the team lock waiting for B's row (held by B's request) and B holds
	// B's row waiting for the team lock (held by A's request): a genuine
	// Postgres deadlock, which aborts one side with a 500 rather than a
	// clean 200 or 409. Both responses must be a real answer, never a 500.
	var leaderStatus, memberStatus int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		leaderStatus = a.at(t, "PUT", base+"/team/members/"+a.userID(t, member)+"/admin", map[string]any{"admin": false}, a.key(leader)).status
	}()
	go func() {
		defer wg.Done()
		memberStatus = a.at(t, "PUT", base+"/team/members/"+a.userID(t, leader)+"/admin", map[string]any{"admin": false}, a.key(member)).status
	}()
	wg.Wait()
	if leaderStatus == 500 || memberStatus == 500 {
		t.Fatalf("concurrent admin removal deadlocked: leader=%d member=%d", leaderStatus, memberStatus)
	}
	// Both requests lock the same two member rows, sorted the same way, so
	// Postgres serialises them instead of deadlocking: whichever commits
	// first can legitimately cost the other its own admin status before it
	// gets to check (403 team_admin_only), on top of the ordinary 200 or
	// 409 last_team_admin outcomes -- never a 500.
	for _, s := range []int{leaderStatus, memberStatus} {
		if s != 200 && s != 403 && s != 409 {
			t.Fatalf("unexpected status in concurrent admin removal: %d", s)
		}
	}
	var admins int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE team_admin AND user_id IN ($1,$2)`, a.userID(t, leader), a.userID(t, member)).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins < 1 {
		t.Fatalf("concurrent removal left %d admins", admins)
	}
}

// --- 5: admin succession on every exit path ---------------------------------

// EnsureTeamAdmin picks the longest-standing remaining member
// (team_joined_at, then user_id); joining order here is leader, then
// member (admin stepped down or removed), so member becomes admin.
func TestHackTeamAdminSuccessionOnLeave(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "succleaveorg")
	leader := a.newPerson(t, "succleaveleader")
	member := a.newPerson(t, "succleavemember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	wantCode(t, "leader leaves team", a.at(t, "POST", base+"/teams/leave", nil, a.key(leader)), 200, "")
	if !a.isTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after leader left the team")
	}
}

func TestHackTeamAdminSuccessionOnLeaveEvent(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "succeventorg")
	leader := a.newPerson(t, "succeventleader")
	member := a.newPerson(t, "succeventmember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	wantCode(t, "leader leaves event", a.leave(t, slug, leader, map[string]any{"confirm": slug}), 200, "")
	if !a.isTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after leader left the event")
	}
}

func TestHackTeamAdminSuccessionOnOrganiserRemove(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "succremoveorg")
	leader := a.newPerson(t, "succremoveleader")
	member := a.newPerson(t, "succremovemember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	_, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	wantCode(t, "organiser removes leader", a.at(t, "DELETE", base+"/people/"+a.userID(t, leader), nil, a.key(org)), 204, "")
	if !a.isTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after organiser removed leader")
	}
}

func TestHackTeamAdminSuccessionOnOrganiserMove(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "succmoveorg")
	leader := a.newPerson(t, "succmoveleader")
	member := a.newPerson(t, "succmovemember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	alphaID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	base := "/v1/hack/events/" + slug
	// Move leader (Alpha's admin) onto a brand-new team by hand; Alpha keeps
	// member, who should succeed as its admin. The destination team needs
	// its own participant to create it (the organiser account isn't one).
	thirdPerson := a.newPerson(t, "succmovethird")
	a.joinParticipant(t, join, thirdPerson, "Third")
	gammaID, _ := a.createTeamAndJoin(t, slug, thirdPerson, "Gamma")
	gammaSlug := a.teamSlugByID(t, gammaID)
	wantCode(t, "organiser moves leader", a.at(t, "POST", base+"/teams/"+gammaSlug+"/members", map[string]any{"user_id": a.userID(t, leader)}, a.key(org)), 200, "")
	if !a.isTeamAdmin(t, member) {
		t.Fatal("member did not succeed as admin after organiser moved leader off Alpha")
	}
	if a.teamOf(t, leader) != gammaID {
		t.Fatal("leader was not moved to Gamma")
	}
	if a.isTeamAdmin(t, leader) {
		t.Fatal("leader kept admin after moving to a team they didn't create")
	}
	_ = alphaID
}

// --- 6: locking ---------------------------------------------------------

func TestHackTeamLock(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lockorg")
	leader := a.newPerson(t, "lockleader")
	asker := a.newPerson(t, "lockasker")
	invitee := a.newPerson(t, "lockinvitee")
	codeJoiner := a.newPerson(t, "lockcodejoiner")
	linkJoiner := a.newPerson(t, "locklinkjoiner")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, asker, invitee, codeJoiner, linkJoiner} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	base := "/v1/hack/events/" + slug
	// A pending request and invite exist before the lock, to prove locking
	// clears them.
	wantCode(t, "ask before lock", a.at(t, "POST", base+"/teams/alpha/requests", nil, a.key(asker)), 201, "")
	wantCode(t, "invitee opts into looking", a.at(t, "PUT", base+"/me/looking", map[string]any{"looking": true}, a.key(invitee)), 200, "")
	wantCode(t, "invite before lock", a.at(t, "POST", base+"/team/invites", map[string]any{"user_id": a.userID(t, invitee)}, a.key(leader)), 201, "")
	r := a.at(t, "PUT", base+"/team/lock", map[string]any{"locked": true}, a.key(leader))
	wantCode(t, "lock", r, 200, "")
	cleared := r.json(t)["cleared"].(map[string]any)
	if cleared["requests"] != float64(1) || cleared["invites"] != float64(1) {
		t.Fatalf("cleared counts: %v", cleared)
	}
	var offers int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_requests WHERE team_id=$1`, teamID).Scan(&offers); err != nil || offers != 0 {
		t.Fatalf("offers not cleared: %d %v", offers, err)
	}
	wantCode(t, "code join locked", a.at(t, "POST", base+"/teams/join", map[string]any{"code": code}, a.key(codeJoiner)), 409, "team_locked")
	wantCode(t, "link join locked", a.at(t, "POST", base+"/teams/join", map[string]any{"code": a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(leader)).json(t)["me"].(map[string]any)["team"].(map[string]any)["link"].(string)}, a.key(linkJoiner)), 409, "team_locked")
	wantCode(t, "request locked", a.at(t, "POST", base+"/teams/alpha/requests", nil, a.key(codeJoiner)), 409, "team_locked")
	wantCode(t, "invite locked", a.at(t, "POST", base+"/team/invites", map[string]any{"user_id": a.userID(t, codeJoiner)}, a.key(leader)), 409, "team_locked")
	// Organiser hand placement still works on a plain locked team.
	wantCode(t, "organiser places into locked team", a.at(t, "POST", base+"/teams/alpha/members", map[string]any{"user_id": a.userID(t, codeJoiner)}, a.key(org)), 200, "")
	// Unlock re-allows ordinary joining.
	wantCode(t, "unlock", a.at(t, "PUT", base+"/team/lock", map[string]any{"locked": false}, a.key(leader)), 200, "")
	wantCode(t, "join after unlock", a.at(t, "POST", base+"/teams/join", map[string]any{"code": code}, a.key(linkJoiner)), 200, "")
}

func TestHackTeamKeptCannotBeUnlockedOrPlaced(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "keptorg")
	leader := a.newPerson(t, "keptleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	teamID, _ := a.createTeamAndJoin(t, slug, leader, "Alpha")
	// Past its deadline, leaving keeps (and now also locks) the team.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	base := "/v1/hack/events/" + slug
	wantCode(t, "leader leaves event past deadline", a.leave(t, slug, leader, map[string]any{"confirm": slug}), 200, "")
	var kept, locked bool
	if err := a.database.QueryRow(`SELECT kept_after_leave, locked_at IS NOT NULL FROM event_teams WHERE id=$1`, teamID).Scan(&kept, &locked); err != nil {
		t.Fatal(err)
	}
	if !kept || !locked {
		t.Fatalf("kept=%v locked=%v", kept, locked)
	}
	newcomer := a.newPerson(t, "keptnewcomer")
	a.joinParticipant(t, join, newcomer, "New")
	slugOfTeam := a.teamSlugByID(t, teamID)
	wantCode(t, "organiser cannot place into kept team", a.at(t, "POST", base+"/teams/"+slugOfTeam+"/members", map[string]any{"user_id": a.userID(t, newcomer)}, a.key(org)), 409, "team_kept")
	wantCode(t, "cannot unlock a kept team", a.at(t, "PUT", base+"/teams/"+slugOfTeam+"/lock", map[string]any{"locked": false}, a.key(org)), 409, "team_kept")
}

// --- 7: rename -----------------------------------------------------------

func TestHackTeamRename(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "renorg")
	leader := a.newPerson(t, "renleader")
	member := a.newPerson(t, "renmember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	other := a.newPerson(t, "renother")
	a.joinParticipant(t, join, other, "Other")
	a.createTeamAndJoin(t, slug, other, "Taken")
	base := "/v1/hack/events/" + slug
	wantCode(t, "member rename", a.at(t, "PATCH", base+"/team", map[string]any{"name": "Nope"}, a.key(member)), 403, "team_admin_only")
	wantCode(t, "duplicate name", a.at(t, "PATCH", base+"/team", map[string]any{"name": "Taken"}, a.key(leader)), 409, "team_name_taken")
	r := a.at(t, "PATCH", base+"/team", map[string]any{"name": "Renamed Alpha"}, a.key(leader))
	wantCode(t, "admin rename", r, 200, "")
	var name, slugCol string
	if err := a.database.QueryRow(`SELECT name, slug FROM event_teams WHERE id=$1`, teamID).Scan(&name, &slugCol); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed Alpha" || slugCol != "alpha" {
		t.Fatalf("rename changed slug: name=%q slug=%q", name, slugCol)
	}
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "rename past deadline", a.at(t, "PATCH", base+"/team", map[string]any{"name": "Too Late"}, a.key(leader)), 409, "submissions_closed")
}

// --- 8: new team link ------------------------------------------------------

func TestHackNewTeamLink(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "linkorg")
	leader := a.newPerson(t, "linkleader")
	member := a.newPerson(t, "linkmember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, oldCode := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, oldCode, member)
	base := "/v1/hack/events/" + slug
	wantCode(t, "member rotates link", a.at(t, "POST", base+"/team/link", nil, a.key(member)), 403, "team_admin_only")
	r := a.at(t, "POST", base+"/team/link", nil, a.key(leader))
	wantCode(t, "admin rotates link", r, 200, "")
	newCode := r.json(t)["code"].(string)
	if newCode == oldCode {
		t.Fatal("code did not change")
	}
	if got := a.teamCode(t, teamID); got != newCode {
		t.Fatalf("stored code %q, want %q", got, newCode)
	}
	oldJoiner := a.newPerson(t, "linkoldjoiner")
	a.joinParticipant(t, join, oldJoiner, "Old Joiner")
	wantCode(t, "old code join 404", a.at(t, "POST", base+"/teams/join", map[string]any{"code": oldCode}, a.key(oldJoiner)), 404, "team_not_found")
	wantCode(t, "old code preview 404", a.at(t, "GET", "/v1/hack/team-links/"+oldCode, nil, nil), 404, "team_link_not_found")
	newJoiner := a.newPerson(t, "linknewjoiner")
	a.joinParticipant(t, join, newJoiner, "New Joiner")
	wantCode(t, "new code join", a.at(t, "POST", base+"/teams/join", map[string]any{"code": newCode}, a.key(newJoiner)), 200, "")
}

// --- 9: joining with the full link string as the code ----------------------

func TestHackJoinTeamWithFullLink(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "linkstrorg")
	leader := a.newPerson(t, "linkstrleader")
	joiner := a.newPerson(t, "linkstrjoiner")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	a.joinParticipant(t, join, joiner, "Joiner")
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	base := "/v1/hack/events/" + slug
	link := a.at(t, "GET", base, nil, a.key(leader)).json(t)["me"].(map[string]any)["team"].(map[string]any)["link"].(string)
	if !strings.Contains(link, code) {
		t.Fatalf("link %q missing code %q", link, code)
	}
	wantCode(t, "join by full link", a.at(t, "POST", base+"/teams/join", map[string]any{"code": link}, a.key(joiner)), 200, "")
	if a.teamOf(t, joiner) != teamID {
		t.Fatal("joiner via link did not join the team")
	}
}

// --- 10: deprecated open field ---------------------------------------------

func TestHackDeprecatedOpenFieldIgnored(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "depopenorg")
	leader := a.newPerson(t, "depopenleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]any{"name": "Closed Club", "open": false}, a.key(leader))
	wantCode(t, "create with open:false", r, 201, "")
	base := "/v1/hack/events/" + slug
	solo := a.newPerson(t, "depopensolo")
	a.joinParticipant(t, join, solo, "Solo")
	listed := a.at(t, "GET", base+"/teams/open", nil, a.key(solo)).json(t)
	found := false
	for _, team := range listed["teams"].([]any) {
		if team.(map[string]any)["name"] == "Closed Club" {
			found = true
		}
	}
	if !found {
		t.Fatalf("open:false team not listed: %v", listed["teams"])
	}
	// Deprecated alias: admin only, ignores open, saves looking_for.
	r = a.at(t, "PUT", base+"/team/finding", map[string]any{"open": true, "looking_for": "We build things"}, a.key(leader))
	wantCode(t, "deprecated finding alias", r, 200, "")
	if r.json(t)["deprecated"] == nil {
		t.Fatalf("missing deprecated hint: %v", r.json(t))
	}
	var open bool
	var lookingFor string
	teamID := a.teamOf(t, leader)
	if err := a.database.QueryRow(`SELECT open, looking_for FROM event_teams WHERE id=$1`, teamID).Scan(&open, &lookingFor); err != nil {
		t.Fatal(err)
	}
	if open != false || lookingFor != "We build things" {
		t.Fatalf("open=%v looking_for=%q", open, lookingFor)
	}
}

// --- 11: listOpenTeams excludes locked, includes former code-only, full ----

func TestHackListOpenTeamsLockedAndFull(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lotorg")
	leader := a.newPerson(t, "lotleader")
	lockedLeader := a.newPerson(t, "lotlockedleader")
	solo := a.newPerson(t, "lotsolo")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, lockedLeader, solo} {
		a.joinParticipant(t, join, p, "P")
	}
	wantCode(t, "max 1", a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"team_size_max": 1}, a.key(org)), 200, "")
	a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.createTeamAndJoin(t, slug, lockedLeader, "Locked Out")
	base := "/v1/hack/events/" + slug
	wantCode(t, "lock", a.at(t, "PUT", base+"/team/lock", map[string]any{"locked": true}, a.key(lockedLeader)), 200, "")
	listed := a.at(t, "GET", base+"/teams/open", nil, a.key(solo)).json(t)
	names := map[string]map[string]any{}
	for _, team := range listed["teams"].([]any) {
		m := team.(map[string]any)
		names[m["name"].(string)] = m
	}
	if _, ok := names["Locked Out"]; ok {
		t.Fatalf("locked team listed: %v", listed["teams"])
	}
	alpha, ok := names["Alpha"]
	if !ok {
		t.Fatalf("unlocked team missing: %v", listed["teams"])
	}
	if alpha["full"] != true {
		t.Fatalf("team at its cap not full: %v", alpha)
	}
}

// --- 12: results locks judging ---------------------------------------------

func TestHackResultsStageLocksJudging(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "reslockorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	for _, stage := range []string{"open", "building", "closed", "judging"} {
		if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": stage}, a.key(org)); r.status != 200 {
			t.Fatalf("stage %s: %d %s", stage, r.status, r.body)
		}
	}
	eventID := a.eventID(t, slug)
	var locked bool
	if err := a.database.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&locked); err != nil || locked {
		t.Fatalf("locked before results: %v %v", locked, err)
	}
	wantCode(t, "to results", a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "results"}, a.key(org)), 200, "")
	if err := a.database.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&locked); err != nil || !locked {
		t.Fatalf("not locked after results: %v %v", locked, err)
	}
	wantCode(t, "back to judging", a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "judging"}, a.key(org)), 200, "")
	if err := a.database.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE id=$1`, eventID).Scan(&locked); err != nil || !locked {
		t.Fatalf("lock not kept going back to judging: %v %v", locked, err)
	}
	wantCode(t, "to results again", a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "results"}, a.key(org)), 200, "")
	wantCode(t, "publish at results", a.at(t, "POST", "/v1/hack/events/"+slug+"/results/publish", map[string]any{}, a.key(org)), 200, "")
}

// --- 13: next_step for every stage ------------------------------------------

func TestHackNextStepEveryStage(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "nextorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	base := "/v1/hack/events/" + slug
	next := func() map[string]any {
		return a.at(t, "GET", base, nil, a.key(org)).json(t)["next_step"].(map[string]any)
	}
	cases := []struct {
		stage, kind, nextStage, label string
	}{
		{"draft", "stage", "open", "Open sign-up"},
		{"open", "stage", "building", "Start building"},
		{"building", "stage", "closed", "Close entries"},
		{"closed", "stage", "judging", "Start judging"},
		{"judging", "stage", "results", "Finish judging"},
	}
	for _, c := range cases {
		wantCode(t, "set "+c.stage, a.at(t, "POST", base+"/stage", map[string]string{"stage": c.stage}, a.key(org)), 200, "")
		n := next()
		if n["kind"] != c.kind || n["stage"] != c.nextStage || n["label"] != c.label {
			t.Fatalf("stage %s: next_step %v", c.stage, n)
		}
	}
	// Now at judging; move to results: kind becomes publish_results.
	wantCode(t, "to results", a.at(t, "POST", base+"/stage", map[string]string{"stage": "results"}, a.key(org)), 200, "")
	n := next()
	if n["kind"] != "publish_results" || n["label"] != "Publish results" {
		t.Fatalf("results unpublished next_step: %v", n)
	}
	// No participants: the event will publish, but then End is blocked.
	wantCode(t, "publish", a.at(t, "POST", base+"/results/publish", map[string]any{}, a.key(org)), 200, "")
	n = next()
	if n["kind"] != "stage" || n["stage"] != "archived" || n["label"] != "End event" || n["blocked"] != "archive_needs_participants" {
		t.Fatalf("published results, nobody joined: %v", n)
	}
	// Add a participant: End is no longer blocked.
	p := a.newPerson(t, "nextparticipant")
	// Participants can't join once results are in; add them earlier instead.
	_ = p
}

// A separate event proves the unblocked End case and the deadline-passed
// flag on Close entries, since the above event has nobody in it by design.
func TestHackNextStepUnblockedEndAndDeadlinePassed(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "next2org")
	p := a.newPerson(t, "next2participant")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p, "P")
	base := "/v1/hack/events/" + slug
	wantCode(t, "to building", a.at(t, "POST", base+"/stage", map[string]string{"stage": "building"}, a.key(org)), 200, "")
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	n := a.at(t, "GET", base, nil, a.key(org)).json(t)["next_step"].(map[string]any)
	if n["deadline_passed"] != true || n["stage"] != "closed" {
		t.Fatalf("deadline_passed not set: %v", n)
	}
	for _, stage := range []string{"closed", "judging", "results"} {
		wantCode(t, "to "+stage, a.at(t, "POST", base+"/stage", map[string]string{"stage": stage}, a.key(org)), 200, "")
	}
	wantCode(t, "publish", a.at(t, "POST", base+"/results/publish", map[string]any{}, a.key(org)), 200, "")
	n = a.at(t, "GET", base, nil, a.key(org)).json(t)["next_step"].(map[string]any)
	if n["blocked"] != nil || n["stage"] != "archived" || n["label"] != "End event" {
		t.Fatalf("End should not be blocked with a participant in the event: %v", n)
	}
	wantCode(t, "end", a.at(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, a.key(org)), 200, "")
	n = a.at(t, "GET", base, nil, a.key(org)).json(t)["next_step"].(map[string]any)
	if n["kind"] != "none" {
		t.Fatalf("ended event should offer no button: %v", n)
	}
}

// --- 14: new event team size default -----------------------------------

func TestHackNewEventTeamSizeDefaultIsTwenty(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "defaultsizeorg")
	slug := uniqueSlug()
	r := a.createEvent(t, org, slug, nil)
	ev := r.json(t)["event"].(map[string]any)
	if ev["team_size_max"] != float64(20) {
		t.Fatalf("team_size_max default: %v", ev["team_size_max"])
	}
}

// --- 15: account erasure promotes a successor on every affected team -------

// Deleting a person's whole platform account (not just leaving a team)
// cascades event_members.user_id's ON DELETE CASCADE, which takes their
// membership -- including team_admin -- off every team they were on, with
// no hack write path and so no EnsureTeamAdmin call in between (Codex
// review, 2026-11-01). This proves EraseAccount now runs it itself for
// every team the erased account admins.
func TestHackAccountErasurePromotesNewAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "eraseorg")
	leader := a.newPerson(t, "eraseleader")
	member := a.newPerson(t, "erasemember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	leaderID := a.userID(t, leader)

	ctx := context.Background()
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	acct, err := db.LockAccountForDelete(ctx, tx, leaderID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EraseAccount(ctx, tx, acct); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var stillMember bool
	if err := a.database.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_members WHERE team_id = $1 AND user_id = $2)`, teamID, leaderID).Scan(&stillMember); err != nil {
		t.Fatal(err)
	}
	if stillMember {
		t.Fatal("erased account's own membership was not removed")
	}
	if !a.isTeamAdmin(t, member) {
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
	org := a.newPerson(t, "erasesoloorg")
	solo := a.newPerson(t, "erasesolomember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, solo, "Solo")
	teamID, _ := a.createTeamAndJoin(t, slug, solo, "SoloTeam")
	soloID := a.userID(t, solo)

	var eventAccountID string
	if err := a.database.QueryRow(`SELECT e.account_id FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&eventAccountID); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	acct, err := db.LockAccountForDelete(ctx, tx, soloID)
	if err != nil {
		t.Fatal(err)
	}
	erased, err := db.EraseAccount(ctx, tx, acct)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var stillExists bool
	if err := a.database.QueryRow(`SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, teamID).Scan(&stillExists); err != nil {
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
	org := a.newPerson(t, "erasesolodlorg")
	solo := a.newPerson(t, "erasesolodlmember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, solo, "Solo")
	teamID, oldCode := a.createTeamAndJoin(t, slug, solo, "SoloTeamDL")
	soloID := a.userID(t, solo)

	base := "/v1/hack/events/" + slug
	// Through the real stage-transition route, same as
	// TestHackLeaveFrozenLoneMemberKeepsTeamEntryAndScores: setStage forces
	// submission_deadline to now() the instant the stage becomes closed, so
	// TeamKeepableSQL/TeamFrozenForLeave agree with what a raw UPDATE would not.
	wantTS(t, "close", a.at(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, a.key(org)), 200, "")

	ctx := context.Background()
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	acct, err := db.LockAccountForDelete(ctx, tx, soloID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EraseAccount(ctx, tx, acct); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var kept, locked bool
	var newCode string
	if err := a.database.QueryRow(`SELECT kept_after_leave, locked_at IS NOT NULL, code FROM event_teams WHERE id = $1`, teamID).Scan(&kept, &locked, &newCode); err != nil {
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
	if err := a.database.QueryRow(`SELECT count(*) FROM event_members WHERE team_id = $1`, teamID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("team should now have no members, got %d", members)
	}
}
