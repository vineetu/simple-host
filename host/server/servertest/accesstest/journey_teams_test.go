package accesstest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// An admin's own account is a person: it creates, invites, is invited and
// acts for its teams with owner=<team>, and has no power over other teams.
func TestTeamAdminAccountIsAPerson(t *testing.T) {
	a := NewApp(t)
	boss, ann, ben := a.NewPerson(t, "team-admin"), a.NewPerson(t, "team-admin-ann"), a.NewPerson(t, "team-admin-ben")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, boss.ID); err != nil {
		t.Fatal(err)
	}
	own := NewTeam(t, a.Base, boss, "admin-own")
	AddMember(t, a.Base, boss, own, ben)
	TeamSite(t, a.Base, own, "shop")
	joined := NewTeam(t, a.Base, ann, "admin-joined")
	AddMember(t, a.Base, ann, joined, boss)
	TeamSite(t, a.Base, joined, "shop")
	for _, team := range []string{own, joined} {
		if r := TeamActs(t, a.Base, boss, team, "shop"); r.Status != http.StatusOK {
			t.Fatalf("admin acts for %s: %d %s", team, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/me", nil, TeamKey(boss)); !strings.Contains(string(r.Body), own) || !strings.Contains(string(r.Body), joined) {
		t.Fatalf("admin's teams on /v1/me: %s", r.Body)
	}
	// Not a member: the same refusal as anyone.
	other := NewTeam(t, a.Base, ann, "admin-other")
	TeamSite(t, a.Base, other, "shop")
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/sites/shop/access?owner=" + other},
		{"GET", "/v1/sites/shop/editors?owner=" + other},
		{"GET", "/v1/teams/" + other + "/members"},
	} {
		if r := a.At(t, c.method, rt.SiteDomain, c.path, nil, TeamKey(boss)); r.Status != http.StatusNotFound {
			t.Fatalf("admin outsider %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
}

func TestTeamDeployOnlyKeyCannotActForTeam(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-ci")
	team := NewTeam(t, a.Base, ann, "ci")
	TeamSite(t, a.Base, team, "shop")
	r := a.At(t, "POST", rt.SiteDomain, "/v1/me/keys", map[string]string{"name": "CI", "scope": "deploy"}, TeamKey(ann))
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy key: %d %s", r.Status, r.Body)
	}
	deploy := Person{Email: ann.Email, Key: r.JSON(t)["api_key"].(string)}
	if r := TeamActs(t, a.Base, deploy, team, "shop"); r.Status != http.StatusForbidden || r.Code() != "deploy_only_key" {
		t.Fatalf("deploy-only key for a team: %d %s", r.Status, r.Body)
	}
}

func TestTeamHasNoKeyToReissue(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-reissue")
	team := NewTeam(t, a.Base, ann, "reissue")
	id := TeamPerson(t, a.Base, team).ID
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/admin/users/"+id+"/key", nil, map[string]string{"X-API-Key": a.Admin}); r.Status != http.StatusConflict || r.Code() != "team_account" {
		t.Fatalf("reissue a team's key: %d %s", r.Status, r.Body)
	}
}

// A team member is the site's owner on the team's site host (storage owner),
// as the site's /me answers it; a non-member is not, and a removed member
// stops being one on the next request.
func TestTeamMemberIsOwnerOnTheSite(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-store"), a.NewPerson(t, "team-store-b"), a.NewPerson(t, "team-store-c")
	team := NewTeam(t, a.Base, ann, "store")
	AddMember(t, a.Base, ann, team, ben)
	sid := TeamSite(t, a.Base, team, "shop")
	host := TeamPerson(t, a.Base, team).SiteHost("shop")

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
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team+"/members/"+ben.Handle, nil, TeamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if siteOwner(benSite) {
		t.Fatal("removed member's next on-site request is still the owner")
	}
}

func TestTeamMemberSeesPrivateTeamSite(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-view"), a.NewPerson(t, "team-view-b"), a.NewPerson(t, "team-view-c")
	team := NewTeam(t, a.Base, ann, "private")
	AddMember(t, a.Base, ann, team, ben)
	sid := TeamSite(t, a.Base, team, "shop")
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/shop/access?owner="+team, map[string]any{"access": "specific"}, TeamKey(ann)); r.Status >= 300 {
		t.Fatalf("named viewers: %d %s", r.Status, r.Body)
	}
	host := TeamPerson(t, a.Base, team).SiteHost("shop")
	if r := a.At(t, "GET", host, "/", nil, rt.Browser(host, a.Session(t, ben, sid, host))); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Fatalf("member opens the private team site: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/", nil, rt.Browser(host, a.Session(t, cal, sid, host))); strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Fatalf("non-member opened the private team site: %d", r.Status)
	}
}

// A member acts for the team with their own key and owner=<team>; an
// outsider, the admin key and a team routes outsider are refused alike.
func TestTeamMembershipGatesOwner(t *testing.T) {
	a := NewApp(t)
	ann, ben, cal := a.NewPerson(t, "team-ann"), a.NewPerson(t, "team-ben"), a.NewPerson(t, "team-cal")
	team := NewTeam(t, a.Base, ann, "studio")
	AddMember(t, a.Base, ann, team, ben)
	TeamSite(t, a.Base, team, "shop")

	for _, p := range []Person{ann, ben} {
		if r := TeamActs(t, a.Base, p, team, "shop"); r.Status != http.StatusOK {
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
		r := a.At(t, c.method, rt.SiteDomain, c.path, c.body, TeamKey(cal))
		if r.Status != http.StatusNotFound {
			t.Fatalf("outsider %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if TeamActs(t, a.Base, cal, team, "shop").Code() != "owner_not_found" {
		t.Fatal("outsider's owner= was not refused as owner_not_found")
	}
}

func TestTeamSoleMemberAccountDeletionDeletesTheTeam(t *testing.T) {
	a := NewApp(t)
	ann, ben := a.NewPerson(t, "team-sole"), a.NewPerson(t, "team-sole-b")
	alone, shared := NewTeam(t, a.Base, ann, "alone"), NewTeam(t, a.Base, ann, "shared")
	AddMember(t, a.Base, ann, shared, ben)
	for _, team := range []string{alone, shared} {
		TeamSite(t, a.Base, team, "shop")
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": ann.Handle}, TeamKey(ann)); r.Status != http.StatusNoContent {
		t.Fatalf("sole member deletes their account: %d %s", r.Status, r.Body)
	}
	var teams, sites int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE handle = $1`, alone).Scan(&teams); err != nil || teams != 0 {
		t.Fatalf("the team they alone belonged to is still there: %d %v", teams, err)
	}
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle IN ($1, $2)`, alone, shared).Scan(&sites); err != nil || sites != 1 {
		t.Fatalf("sites left: %d %v", sites, err)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": alone}, TeamKey(ben)); r.Code() != "name_taken" {
		t.Fatalf("the deleted team's name is retired: %s", r.Body)
	}
	if r := TeamActs(t, a.Base, ben, shared, "shop"); r.Status != http.StatusOK {
		t.Fatalf("the shared team carries on: %d %s", r.Status, r.Body)
	}
}

// The operator's holds stop a person deleting their account through a team
// they alone belong to, as they stop it for the person's own account, and
// stop the team being deleted.
func TestTeamSoleMemberHoldsBlockDeletion(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "team-hold")
	team := NewTeam(t, a.Base, ann, "hold")
	TeamSite(t, a.Base, team, "shop")
	del := func() Resp {
		return a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": ann.Handle}, TeamKey(ann))
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
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/teams/"+team, nil, TeamKey(ann)); r.Status != http.StatusForbidden || r.Code() != "account_suspended" {
		t.Fatalf("delete a suspended team: %d %s", r.Status, r.Body)
	}
	if memberRows(t, a, team) != 1 {
		t.Fatal("a refused deletion changed the team")
	}
}

// list_teams and who_am_i are Access's connector tools.
func TestTeamToolsThroughConnector(t *testing.T) {
	a := NewApp(t)
	ann, ben := a.NewPerson(t, "team-ai"), a.NewPerson(t, "team-ai-b")
	team := NewTeam(t, a.Base, ann, "ai")
	AddMember(t, a.Base, ann, team, ben)
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

func memberRows(t *testing.T, a *App, team string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.handle = $1`, team).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
