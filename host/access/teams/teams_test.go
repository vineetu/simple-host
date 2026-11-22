package teams

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Teams (owner decisions 2026-11-19): an account that owns sites and that
// people join; members act with their own credentials and owner=<team>.
// Deploying for a team is Sites' use of Access's owner resolution; here the
// rule is proven through the Access routes that take owner= (the site's
// access settings and editors), on a team site made at repo level.

func memberRows(t *testing.T, a *App, team string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.handle = $1`, team).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTeamNamesFollowAddressRules(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-names")
	for name, code := range map[string]string{
		"design.team": "invalid_name",
		"-design":     "invalid_name",
		ann.Handle:    "name_taken",
		"www":         "name_conflict",
	} {
		r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": name}, TeamKey(ann))
		if r.Status < 400 || r.Code() != code {
			t.Fatalf("%q: want %s, got %d %s", name, code, r.Status, r.Body)
		}
	}
	taken := NewTeam(t, a.Base, ann, "names")
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": taken}, TeamKey(ann)); r.Code() != "name_taken" {
		t.Fatalf("second team of one name: %s", r.Body)
	}
}

func TestTeamLimitIsTenPerPerson(t *testing.T) {
	a := NewApp(t)
	ann, bea := a.NewPerson(t, "team-limit"), a.NewPerson(t, "team-limit-b")
	for i := 0; i < 10; i++ {
		NewTeam(t, a.Base, ann, "limit")
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": "limit-eleven-x"}, TeamKey(ann)); r.Status != http.StatusConflict || r.Code() != "team_limit" {
		t.Fatalf("eleventh team: %d %s", r.Status, r.Body)
	}
	other := NewTeam(t, a.Base, bea, "limit-other")
	Invite(t, a.Base, bea, other, ann.Email)
	if r := Answer(t, a.Base, ann, InvitationTo(t, a.Base, ann, other), "accept"); r.Code() != "team_limit" {
		t.Fatalf("accepting at the limit: %d %s", r.Status, r.Body)
	}
}

func TestTeamRemovedMemberIsRefusedOnNextRequest(t *testing.T) {
	a := NewApp(t)
	ann, ben := a.NewPerson(t, "team-remover"), a.NewPerson(t, "team-removed")
	team := NewTeam(t, a.Base, ann, "removal")
	AddMember(t, a.Base, ann, team, ben)
	TeamSite(t, a.Base, team, "shop")
	if r := TeamActs(t, a.Base, ben, team, "shop"); r.Status != http.StatusOK {
		t.Fatalf("member: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ben.Handle, nil, TeamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := TeamActs(t, a.Base, ben, team, "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("removed member's next request: %d %s", r.Status, r.Body)
	}
	var actions string
	if err := a.Database.QueryRow(`SELECT string_agg(action, ',' ORDER BY id) FROM team_audit WHERE team_handle = $1`, team).Scan(&actions); err != nil || actions != "create,invite,accept,remove_member" {
		t.Fatalf("audit: %q %v", actions, err)
	}
}

func TestTeamKeepsAMemberAndDeletesOnlyWhenEmpty(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-last")
	team := NewTeam(t, a.Base, ann, "guards")
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ann.Handle, nil, TeamKey(ann)); r.Status != http.StatusConflict || r.Code() != "last_member" {
		t.Fatalf("last member leaves: %d %s", r.Status, r.Body)
	}
	TeamSite(t, a.Base, team, "shop")
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team, nil, TeamKey(ann)); r.Status != http.StatusConflict || r.Code() != "team_has_sites" {
		t.Fatalf("delete with sites: %d %s", r.Status, r.Body)
	}
	// A site in Recently deleted does not block; it goes for good with the team.
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() WHERE user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team, nil, TeamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("delete team with a deleted site: %d %s", r.Status, r.Body)
	}
	var left int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1`, team).Scan(&left); err != nil || left != 0 {
		t.Fatalf("team's deleted site left behind: %d %v", left, err)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": team}, TeamKey(ann)); r.Code() != "name_taken" {
		t.Fatalf("a deleted team's name is retired: %s", r.Body)
	}
}

func TestTeamDatabaseGuards(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-db")
	team := NewTeam(t, a.Base, ann, "db")
	var teamID string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE handle = $1`, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	for what, stmt := range map[string]string{
		"a team holds a key":            `INSERT INTO api_keys (key_hash, user_id) VALUES ('tms1-` + teamID + `', '` + teamID + `')`,
		"a person used as a team":       `INSERT INTO team_members (team_id, user_id) VALUES ('` + ann.ID + `', '` + ann.ID + `')`,
		"a team used as a member":       `INSERT INTO team_members (team_id, user_id) VALUES ('` + teamID + `', '` + teamID + `')`,
		"a member stops being a person": `UPDATE users SET is_team = TRUE WHERE id = '` + ann.ID + `'`,
		"a team stops being a team":     `UPDATE users SET is_team = FALSE WHERE id = '` + teamID + `'`,
	} {
		if _, err := a.Database.Exec(stmt); err == nil {
			t.Fatalf("%s: accepted", what)
		}
	}
}

func TestTeamsAbsentOnProductEdition(t *testing.T) {
	a := NewApp(t, GroupTestEdition())
	ann := a.NewPerson(t, "team-product")
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/teams", nil, TeamKey(ann)); r.Status != http.StatusNotFound {
		t.Fatalf("team routes on the product program: %d %s", r.Status, r.Body)
	}
	// owner is resolved on every install.
	if r := TeamActs(t, a.Base, ann, "someone-else", "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("owner on the product program: %d %s", r.Status, r.Body)
	}
}

func TestTeamInviteAnswersAlikeForAnyone(t *testing.T) {
	a := NewApp(t)
	ann, cal := a.NewPerson(t, "team-inviter"), a.NewPerson(t, "team-invitee")
	team := NewTeam(t, a.Base, ann, "invites")
	var first Resp
	for i, whom := range []string{cal.Email, "nobody-x9@example.com"} {
		r := Invite(t, a.Base, ann, team, whom)
		if r.Status != http.StatusAccepted {
			t.Fatalf("invite %q: %d %s", whom, r.Status, r.Body)
		}
		if i == 0 {
			first = r
		} else if string(r.Body) != string(first.Body) {
			t.Fatalf("invite %q answered differently:\n%s\n%s", whom, r.Body, first.Body)
		}
	}
	if strings.Contains(string(first.Body), cal.Email) {
		t.Fatalf("the answer names the invitee: %s", first.Body)
	}
	if n := memberRows(t, a, team); n != 1 {
		t.Fatalf("an invitation made a member: %d members", n)
	}
	if r := Invite(t, a.Base, ann, team, cal.Handle); r.Status != http.StatusBadRequest || r.Code() != "invalid_email" {
		t.Fatalf("invitation to a handle: %d %s", r.Status, r.Body)
	}
	// The members see what was typed; the invitee sees the team.
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/teams/"+team+"/members", nil, TeamKey(ann)); len(r.JSON(t)["invitations"].([]any)) != 2 {
		t.Fatalf("team invitations: %s", r.Body)
	}
	if InvitationTo(t, a.Base, cal, team) == "" {
		t.Fatal("the invitee does not see the invitation")
	}
}

func TestTeamInvitationAcceptDeclineCancelExpire(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal, dee := a.NewPerson(t, "team-ans"), a.NewPerson(t, "team-ans-b"), a.NewPerson(t, "team-ans-c"), a.NewPerson(t, "team-ans-d")
	team := NewTeam(t, a.Base, ann, "answers")
	TeamSite(t, a.Base, team, "shop")
	for _, p := range []Person{ben, cal, dee} {
		Invite(t, a.Base, ann, team, p.Email)
	}
	benInv, calInv, deeInv := InvitationTo(t, a.Base, ben, team), InvitationTo(t, a.Base, cal, team), InvitationTo(t, a.Base, dee, team)
	// Only the person addressed can answer.
	if r := Answer(t, a.Base, cal, benInv, "accept"); r.Status != http.StatusNotFound || r.Code() != "invitation_not_found" {
		t.Fatalf("someone else's invitation: %d %s", r.Status, r.Body)
	}
	if r := Answer(t, a.Base, ben, benInv, "accept"); r.Status != http.StatusOK || r.JSON(t)["team"] != team {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
	if r := TeamActs(t, a.Base, ben, team, "shop"); r.Status != http.StatusOK {
		t.Fatalf("new member acts for the team: %d %s", r.Status, r.Body)
	}
	if r := Answer(t, a.Base, cal, calInv, "decline"); r.Status != http.StatusOK {
		t.Fatalf("decline: %d %s", r.Status, r.Body)
	}
	if r := TeamActs(t, a.Base, cal, team, "shop"); r.Status != http.StatusNotFound {
		t.Fatalf("decliner acts for the team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/invitations/"+deeInv, nil, TeamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("cancel: %d %s", r.Status, r.Body)
	}
	if InvitationTo(t, a.Base, dee, team) != "" || Answer(t, a.Base, dee, deeInv, "accept").Status != http.StatusNotFound {
		t.Fatal("a cancelled invitation is still offered")
	}
	Invite(t, a.Base, ann, team, dee.Email)
	if _, err := a.Database.Exec(`UPDATE team_invitations SET expires_at = now() - interval '1 second' WHERE addressed_to = $1`, strings.ToLower(dee.Email)); err != nil {
		t.Fatal(err)
	}
	if id := InvitationTo(t, a.Base, dee, team); id != "" {
		t.Fatal("an expired invitation is still offered")
	}
	if n := memberRows(t, a, team); n != 2 {
		t.Fatalf("members: %d", n)
	}
}

func TestTeamInvitationGoesWhenInviterLeaves(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-void"), a.NewPerson(t, "team-void-b"), a.NewPerson(t, "team-void-c")
	team := NewTeam(t, a.Base, ann, "void")
	AddMember(t, a.Base, ann, team, ben)
	Invite(t, a.Base, ben, team, cal.Email)
	id := InvitationTo(t, a.Base, cal, team)
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ben.Handle, nil, TeamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("inviter leaves: %d %s", r.Status, r.Body)
	}
	AddMember(t, a.Base, ann, team, ben)
	if r := Answer(t, a.Base, cal, id, "accept"); r.Status != http.StatusNotFound || memberRows(t, a, team) != 2 {
		t.Fatalf("accepting an invitation whose sender left and rejoined: %d %s", r.Status, r.Body)
	}
}

func TestTeamInvitationCaps(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-caps")
	team := NewTeam(t, a.Base, ann, "caps")
	for i := 0; i <= 20; i++ {
		want, code := http.StatusAccepted, ""
		if i == 20 {
			want, code = http.StatusConflict, "invitation_limit"
		}
		if r := Invite(t, a.Base, ann, team, fmt.Sprintf("someone-%d@example.com", i)); r.Status != want || (code != "" && r.Code() != code) {
			t.Fatalf("invitation %d: %d %s", i+1, r.Status, r.Body)
		}
	}
}

// An invitation reaches only the account whose verified sign-in email it
// is, exactly: no +tag folding, and never an unverified self-signup address.
func TestTeamInvitationOnlyForVerifiedSignInEmail(t *testing.T) {
	a := NewApp(t)
	ann, bob := a.NewPerson(t, "team-email"), a.NewPerson(t, "team-email-b")
	team := NewTeam(t, a.Base, ann, "email")
	Invite(t, a.Base, ann, team, strings.Replace(bob.Email, "@", "+x@", 1))
	if InvitationTo(t, a.Base, bob, team) != "" {
		t.Fatal("an invitation to a +tag address reached the plain one")
	}
	Invite(t, a.Base, ann, team, strings.ToUpper(bob.Email))
	if InvitationTo(t, a.Base, bob, team) == "" {
		t.Fatal("an invitation to the sign-in email, in capitals, did not reach it")
	}
	eve := a.NewPerson(t, "team-email-e")
	if _, err := a.Database.Exec(`UPDATE users SET signup_method = 'password', signup_source = 'website' WHERE username = $1`, eve.Email); err != nil {
		t.Fatal(err)
	}
	Invite(t, a.Base, ann, team, eve.Email)
	if InvitationTo(t, a.Base, eve, team) != "" {
		t.Fatal("an unverified self-signup email sees an invitation")
	}
}

func TestTeamOwnerThatIsNoHandle(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-query")
	team := NewTeam(t, a.Base, ann, "query")
	TeamSite(t, a.Base, team, "shop")
	if r := TeamActs(t, a.Base, ann, team+"%00", "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("owner with a NUL byte: %d %s", r.Status, r.Body)
	}
}
