package accesstest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

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

// teamSite publishes a site for the team at repo level and returns its id.
func teamSite(t *testing.T, a *App, team, name string) string {
	t.Helper()
	return a.Deploy(t, teamPerson(t, a, team), name)
}

// teamActs is p acting for owner on site with owner=: GET .../access, the
// plainest Access route that resolves it.
func teamActs(t *testing.T, a *App, p Person, owner, site string) Resp {
	t.Helper()
	return a.At(t, "GET", rt.SiteDomain, "/v1/sites/"+site+"/access?owner="+owner, nil, p.Header())
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
		r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": name}, teamKey(ann))
		if r.Status < 400 || r.Code() != code {
			t.Fatalf("%q: want %s, got %d %s", name, code, r.Status, r.Body)
		}
	}
	taken := newTeam(t, a, ann, "names")
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": taken}, teamKey(ann)); r.Code() != "name_taken" {
		t.Fatalf("second team of one name: %s", r.Body)
	}
}

func TestTeamLimitIsTenPerPerson(t *testing.T) {
	a := NewApp(t)
	ann, bea := a.NewPerson(t, "team-limit"), a.NewPerson(t, "team-limit-b")
	for i := 0; i < 10; i++ {
		newTeam(t, a, ann, "limit")
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": "limit-eleven-x"}, teamKey(ann)); r.Status != http.StatusConflict || r.Code() != "team_limit" {
		t.Fatalf("eleventh team: %d %s", r.Status, r.Body)
	}
	other := newTeam(t, a, bea, "limit-other")
	invite(t, a, bea, other, ann.Email)
	if r := answer(t, a, ann, invitationTo(t, a, ann, other), "accept"); r.Code() != "team_limit" {
		t.Fatalf("accepting at the limit: %d %s", r.Status, r.Body)
	}
}

// A member acts for the team with their own key and owner=<team>; an
// outsider, the admin key and a team routes outsider are refused alike.
func TestTeamMembershipGatesOwner(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-ann"), a.NewPerson(t, "team-ben"), a.NewPerson(t, "team-cal")
	team := newTeam(t, a, ann, "studio")
	addMember(t, a, ann, team, ben)
	teamSite(t, a, team, "shop")

	for _, p := range []Person{ann, ben} {
		if r := teamActs(t, a, p, team, "shop"); r.Status != http.StatusOK {
			t.Fatalf("member acts for the team: %d %s", r.Status, r.Body)
		}
	}
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/access?owner="+team, map[string]any{"access": "specific"}, ben.Header()); r.Status != http.StatusOK {
		t.Fatalf("member changes the team site's access: %d %s", r.Status, r.Body)
	}
	// Without owner= the same call is the member's own (no such site).
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/sites/shop/access", nil, ben.Header()); r.Status != http.StatusNotFound {
		t.Fatalf("a team site answered as the member's own: %d %s", r.Status, r.Body)
	}

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/sites/shop/access?owner=" + team, nil},
		{"PUT", "/v1/sites/shop/access?owner=" + team, map[string]any{"access": "anyone"}},
		{"GET", "/v1/sites/shop/editors?owner=" + team, nil},
		{"GET", "/v1/teams/" + team + "/members", nil},
		{"POST", "/v1/teams/" + team + "/members", map[string]string{"email": cal.Email}},
		{"DELETE", "/v1/teams/" + team, nil},
	} {
		r := a.At(t, c.method, rt.SiteDomain, c.path, c.body, teamKey(cal))
		if r.Status != http.StatusNotFound {
			t.Fatalf("outsider %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if teamActs(t, a, cal, team, "shop").Code() != "owner_not_found" {
		t.Fatal("outsider's owner= was not refused as owner_not_found")
	}
}

func TestTeamRemovedMemberIsRefusedOnNextRequest(t *testing.T) {
	a := NewApp(t)
	ann, ben := a.NewPerson(t, "team-remover"), a.NewPerson(t, "team-removed")
	team := newTeam(t, a, ann, "removal")
	addMember(t, a, ann, team, ben)
	teamSite(t, a, team, "shop")
	if r := teamActs(t, a, ben, team, "shop"); r.Status != http.StatusOK {
		t.Fatalf("member: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ben.Handle, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := teamActs(t, a, ben, team, "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("removed member's next request: %d %s", r.Status, r.Body)
	}
	var actions string
	if err := a.Database.QueryRow(`SELECT string_agg(action, ',' ORDER BY id) FROM team_audit WHERE team_handle = $1`, team).Scan(&actions); err != nil || actions != "create,invite,accept,remove_member" {
		t.Fatalf("audit: %q %v", actions, err)
	}
}

func TestTeamDeployOnlyKeyCannotActForTeam(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-ci")
	team := newTeam(t, a, ann, "ci")
	teamSite(t, a, team, "shop")
	r := a.At(t, "POST", rt.SiteDomain, "/v1/me/keys", map[string]string{"name": "CI", "scope": "deploy"}, teamKey(ann))
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy key: %d %s", r.Status, r.Body)
	}
	deploy := Person{Email: ann.Email, Key: r.JSON(t)["api_key"].(string)}
	if r := teamActs(t, a, deploy, team, "shop"); r.Status != http.StatusForbidden || r.Code() != "deploy_only_key" {
		t.Fatalf("deploy-only key for a team: %d %s", r.Status, r.Body)
	}
}

func TestTeamKeepsAMemberAndDeletesOnlyWhenEmpty(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-last")
	team := newTeam(t, a, ann, "guards")
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ann.Handle, nil, teamKey(ann)); r.Status != http.StatusConflict || r.Code() != "last_member" {
		t.Fatalf("last member leaves: %d %s", r.Status, r.Body)
	}
	teamSite(t, a, team, "shop")
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team, nil, teamKey(ann)); r.Status != http.StatusConflict || r.Code() != "team_has_sites" {
		t.Fatalf("delete with sites: %d %s", r.Status, r.Body)
	}
	// A site in Recently deleted does not block; it goes for good with the team.
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() WHERE user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("delete team with a deleted site: %d %s", r.Status, r.Body)
	}
	var left int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1`, team).Scan(&left); err != nil || left != 0 {
		t.Fatalf("team's deleted site left behind: %d %v", left, err)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": team}, teamKey(ann)); r.Code() != "name_taken" {
		t.Fatalf("a deleted team's name is retired: %s", r.Body)
	}
}

// A team member is the site's owner on the team's site host (storage owner),
// as the site's /me answers it; a non-member is not, and a removed member
// stops being one on the next request.
func TestTeamMemberIsOwnerOnTheSite(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-store"), a.NewPerson(t, "team-store-b"), a.NewPerson(t, "team-store-c")
	team := newTeam(t, a, ann, "store")
	addMember(t, a, ann, team, ben)
	sid := teamSite(t, a, team, "shop")
	host := teamPerson(t, a, team).SiteHost("shop")

	// By key with owner=<team>.
	r := a.At(t, "GET", rt.SiteDomain, "/v1/sites/shop/storage/probe?owner="+team, nil, ben.Header())
	if r.Status != http.StatusOK || r.JSON(t)["Owner"] != true {
		t.Fatalf("member's key with owner=: %d %s", r.Status, r.Body)
	}
	// Signed in on the site.
	benSite, calSite := rt.Browser(host, a.Session(t, ben, sid, host)), rt.Browser(host, a.Session(t, cal, sid, host))
	siteOwner := func(headers map[string]string) bool {
		return a.At(t, "GET", host, "/v1/sites/shop/me", nil, headers).JSON(t)["site_owner"] == true
	}
	if !siteOwner(benSite) {
		t.Fatal("member signed in on the site is not its owner there")
	}
	if siteOwner(calSite) {
		t.Fatal("non-member is the site's owner there")
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ben.Handle, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if siteOwner(benSite) {
		t.Fatal("removed member's next on-site request is still the owner")
	}
}

func TestTeamMemberSeesPrivateTeamSite(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-view"), a.NewPerson(t, "team-view-b"), a.NewPerson(t, "team-view-c")
	team := newTeam(t, a, ann, "private")
	addMember(t, a, ann, team, ben)
	sid := teamSite(t, a, team, "shop")
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/access?owner="+team, map[string]any{"access": "specific"}, teamKey(ann)); r.Status >= 300 {
		t.Fatalf("named viewers: %d %s", r.Status, r.Body)
	}
	host := teamPerson(t, a, team).SiteHost("shop")
	if r := a.At(t, "GET", host, "/", nil, rt.Browser(host, a.Session(t, ben, sid, host))); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Fatalf("member opens the private team site: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/", nil, rt.Browser(host, a.Session(t, cal, sid, host))); strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Fatalf("non-member opened the private team site: %d", r.Status)
	}
}

// list_teams and who_am_i are Access's connector tools.
func TestTeamToolsThroughConnector(t *testing.T) {
	a := NewApp(t)
	ann, ben := a.NewPerson(t, "team-ai"), a.NewPerson(t, "team-ai-b")
	team := newTeam(t, a, ann, "ai")
	addMember(t, a, ann, team, ben)
	token := a.Connect(t, ben, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	call := func(name string) (string, map[string]any, bool) {
		return ToolResultOf(t, a.Rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": map[string]any{}}))
	}
	for _, name := range []string{"list_teams", "who_am_i"} {
		if text, out, failed := call(name); failed || !strings.Contains(fmt.Sprint(out["teams"]), team) {
			t.Fatalf("%s: %s", name, text)
		}
	}
}

func TestTeamDatabaseGuards(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-db")
	team := newTeam(t, a, ann, "db")
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
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/teams", nil, teamKey(ann)); r.Status != http.StatusNotFound {
		t.Fatalf("team routes on the product program: %d %s", r.Status, r.Body)
	}
	// owner is resolved on every install.
	if r := teamActs(t, a, ann, "someone-else", "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("owner on the product program: %d %s", r.Status, r.Body)
	}
}

func TestTeamInviteAnswersAlikeForAnyone(t *testing.T) {
	a := NewApp(t)
	ann, cal := a.NewPerson(t, "team-inviter"), a.NewPerson(t, "team-invitee")
	team := newTeam(t, a, ann, "invites")
	var first Resp
	for i, whom := range []string{cal.Email, "nobody-x9@example.com"} {
		r := invite(t, a, ann, team, whom)
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
	if r := invite(t, a, ann, team, cal.Handle); r.Status != http.StatusBadRequest || r.Code() != "invalid_email" {
		t.Fatalf("invitation to a handle: %d %s", r.Status, r.Body)
	}
	// The members see what was typed; the invitee sees the team.
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/teams/"+team+"/members", nil, teamKey(ann)); len(r.JSON(t)["invitations"].([]any)) != 2 {
		t.Fatalf("team invitations: %s", r.Body)
	}
	if invitationTo(t, a, cal, team) == "" {
		t.Fatal("the invitee does not see the invitation")
	}
}

func TestTeamInvitationAcceptDeclineCancelExpire(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal, dee := a.NewPerson(t, "team-ans"), a.NewPerson(t, "team-ans-b"), a.NewPerson(t, "team-ans-c"), a.NewPerson(t, "team-ans-d")
	team := newTeam(t, a, ann, "answers")
	teamSite(t, a, team, "shop")
	for _, p := range []Person{ben, cal, dee} {
		invite(t, a, ann, team, p.Email)
	}
	benInv, calInv, deeInv := invitationTo(t, a, ben, team), invitationTo(t, a, cal, team), invitationTo(t, a, dee, team)
	// Only the person addressed can answer.
	if r := answer(t, a, cal, benInv, "accept"); r.Status != http.StatusNotFound || r.Code() != "invitation_not_found" {
		t.Fatalf("someone else's invitation: %d %s", r.Status, r.Body)
	}
	if r := answer(t, a, ben, benInv, "accept"); r.Status != http.StatusOK || r.JSON(t)["team"] != team {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
	if r := teamActs(t, a, ben, team, "shop"); r.Status != http.StatusOK {
		t.Fatalf("new member acts for the team: %d %s", r.Status, r.Body)
	}
	if r := answer(t, a, cal, calInv, "decline"); r.Status != http.StatusOK {
		t.Fatalf("decline: %d %s", r.Status, r.Body)
	}
	if r := teamActs(t, a, cal, team, "shop"); r.Status != http.StatusNotFound {
		t.Fatalf("decliner acts for the team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/invitations/"+deeInv, nil, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("cancel: %d %s", r.Status, r.Body)
	}
	if invitationTo(t, a, dee, team) != "" || answer(t, a, dee, deeInv, "accept").Status != http.StatusNotFound {
		t.Fatal("a cancelled invitation is still offered")
	}
	invite(t, a, ann, team, dee.Email)
	if _, err := a.Database.Exec(`UPDATE team_invitations SET expires_at = now() - interval '1 second' WHERE addressed_to = $1`, strings.ToLower(dee.Email)); err != nil {
		t.Fatal(err)
	}
	if id := invitationTo(t, a, dee, team); id != "" {
		t.Fatal("an expired invitation is still offered")
	}
	if n := memberRows(t, a, team); n != 2 {
		t.Fatalf("members: %d", n)
	}
}

func TestTeamInvitationGoesWhenInviterLeaves(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-void"), a.NewPerson(t, "team-void-b"), a.NewPerson(t, "team-void-c")
	team := newTeam(t, a, ann, "void")
	addMember(t, a, ann, team, ben)
	invite(t, a, ben, team, cal.Email)
	id := invitationTo(t, a, cal, team)
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ben.Handle, nil, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("inviter leaves: %d %s", r.Status, r.Body)
	}
	addMember(t, a, ann, team, ben)
	if r := answer(t, a, cal, id, "accept"); r.Status != http.StatusNotFound || memberRows(t, a, team) != 2 {
		t.Fatalf("accepting an invitation whose sender left and rejoined: %d %s", r.Status, r.Body)
	}
}

func TestTeamInvitationCaps(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-caps")
	team := newTeam(t, a, ann, "caps")
	for i := 0; i <= 20; i++ {
		want, code := http.StatusAccepted, ""
		if i == 20 {
			want, code = http.StatusConflict, "invitation_limit"
		}
		if r := invite(t, a, ann, team, fmt.Sprintf("someone-%d@example.com", i)); r.Status != want || (code != "" && r.Code() != code) {
			t.Fatalf("invitation %d: %d %s", i+1, r.Status, r.Body)
		}
	}
}

func TestTeamSoleMemberAccountDeletionDeletesTheTeam(t *testing.T) {
	a := NewApp(t)
	ann, ben := a.NewPerson(t, "team-sole"), a.NewPerson(t, "team-sole-b")
	alone, shared := newTeam(t, a, ann, "alone"), newTeam(t, a, ann, "shared")
	addMember(t, a, ann, shared, ben)
	for _, team := range []string{alone, shared} {
		teamSite(t, a, team, "shop")
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": ann.Handle}, teamKey(ann)); r.Status != http.StatusNoContent {
		t.Fatalf("sole member deletes their account: %d %s", r.Status, r.Body)
	}
	var teams, sites int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE handle = $1`, alone).Scan(&teams); err != nil || teams != 0 {
		t.Fatalf("the team they alone belonged to is still there: %d %v", teams, err)
	}
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle IN ($1, $2)`, alone, shared).Scan(&sites); err != nil || sites != 1 {
		t.Fatalf("sites left: %d %v", sites, err)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": alone}, teamKey(ben)); r.Code() != "name_taken" {
		t.Fatalf("the deleted team's name is retired: %s", r.Body)
	}
	if r := teamActs(t, a, ben, shared, "shop"); r.Status != http.StatusOK {
		t.Fatalf("the shared team carries on: %d %s", r.Status, r.Body)
	}
}

// The operator's holds stop a person deleting their account through a team
// they alone belong to, as they stop it for the person's own account, and
// stop the team being deleted.
func TestTeamSoleMemberHoldsBlockDeletion(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-hold")
	team := newTeam(t, a, ann, "hold")
	teamSite(t, a, team, "shop")
	del := func() Resp {
		return a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": ann.Handle}, teamKey(ann))
	}
	if _, err := a.Database.Exec(`UPDATE sites SET suspended_at = now() WHERE name = 'shop' AND user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if r := del(); r.Status != http.StatusForbidden || r.Code() != "site_suspended" {
		t.Fatalf("delete with a taken-down team site: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`DELETE FROM sites WHERE user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at = now() WHERE handle = $1`, team); err != nil {
		t.Fatal(err)
	}
	if r := del(); r.Status != http.StatusForbidden || r.Code() != "account_suspended" {
		t.Fatalf("delete with a suspended team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team, nil, teamKey(ann)); r.Status != http.StatusForbidden || r.Code() != "account_suspended" {
		t.Fatalf("delete a suspended team: %d %s", r.Status, r.Body)
	}
	if memberRows(t, a, team) != 1 {
		t.Fatal("a refused deletion changed the team")
	}
}

// An invitation reaches only the account whose verified sign-in email it
// is, exactly: no +tag folding, and never an unverified self-signup address.
func TestTeamInvitationOnlyForVerifiedSignInEmail(t *testing.T) {
	a := NewApp(t)
	ann, bob := a.NewPerson(t, "team-email"), a.NewPerson(t, "team-email-b")
	team := newTeam(t, a, ann, "email")
	invite(t, a, ann, team, strings.Replace(bob.Email, "@", "+x@", 1))
	if invitationTo(t, a, bob, team) != "" {
		t.Fatal("an invitation to a +tag address reached the plain one")
	}
	invite(t, a, ann, team, strings.ToUpper(bob.Email))
	if invitationTo(t, a, bob, team) == "" {
		t.Fatal("an invitation to the sign-in email, in capitals, did not reach it")
	}
	eve := a.NewPerson(t, "team-email-e")
	if _, err := a.Database.Exec(`UPDATE users SET signup_method = 'password', signup_source = 'website' WHERE username = $1`, eve.Email); err != nil {
		t.Fatal(err)
	}
	invite(t, a, ann, team, eve.Email)
	if invitationTo(t, a, eve, team) != "" {
		t.Fatal("an unverified self-signup email sees an invitation")
	}
}

func TestTeamOwnerThatIsNoHandle(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-query")
	team := newTeam(t, a, ann, "query")
	teamSite(t, a, team, "shop")
	if r := teamActs(t, a, ann, team+"%00", "shop"); r.Status != http.StatusNotFound || r.Code() != "owner_not_found" {
		t.Fatalf("owner with a NUL byte: %d %s", r.Status, r.Body)
	}
}

func TestTeamHasNoKeyToReissue(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-reissue")
	team := newTeam(t, a, ann, "reissue")
	id := teamPerson(t, a, team).ID
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/admin/users/"+id+"/key", nil, map[string]string{"X-API-Key": a.Admin}); r.Status != http.StatusConflict || r.Code() != "team_account" {
		t.Fatalf("reissue a team's key: %d %s", r.Status, r.Body)
	}
}

// An admin's own account is a person: it creates, invites, is invited and
// acts for its teams with owner=<team>, and has no power over other teams.
func TestTeamAdminAccountIsAPerson(t *testing.T) {
	a := NewApp(t)
	boss, ann, ben := a.NewPerson(t, "team-admin"), a.NewPerson(t, "team-admin-ann"), a.NewPerson(t, "team-admin-ben")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, boss.ID); err != nil {
		t.Fatal(err)
	}
	own := newTeam(t, a, boss, "admin-own")
	addMember(t, a, boss, own, ben)
	teamSite(t, a, own, "shop")
	joined := newTeam(t, a, ann, "admin-joined")
	addMember(t, a, ann, joined, boss)
	teamSite(t, a, joined, "shop")
	for _, team := range []string{own, joined} {
		if r := teamActs(t, a, boss, team, "shop"); r.Status != http.StatusOK {
			t.Fatalf("admin acts for %s: %d %s", team, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/me", nil, teamKey(boss)); !strings.Contains(string(r.Body), own) || !strings.Contains(string(r.Body), joined) {
		t.Fatalf("admin's teams on /v1/me: %s", r.Body)
	}
	// Not a member: the same refusal as anyone.
	other := newTeam(t, a, ann, "admin-other")
	teamSite(t, a, other, "shop")
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/sites/shop/access?owner=" + other},
		{"GET", "/v1/sites/shop/editors?owner=" + other},
		{"GET", "/v1/teams/" + other + "/members"},
	} {
		if r := a.At(t, c.method, rt.SiteDomain, c.path, nil, teamKey(boss)); r.Status != http.StatusNotFound {
			t.Fatalf("admin outsider %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
}
