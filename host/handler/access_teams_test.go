package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var teamSeq atomic.Int64

// Teams (owner decisions 2026-11-19): an account that owns sites and that
// people join; members act with their own credentials and owner=<team>.

const teamAPIHost = "simple-host.test"

func teamKey(p person) map[string]string { return map[string]string{"X-API-Key": p.key} }

// newTeam has p create a team named after label, returning its handle.
func newTeam(t *testing.T, a *privateApp, p person, label string) string {
	t.Helper()
	name := fmt.Sprintf("%s-%s-%d", label, strconv.FormatInt(time.Now().UnixNano()%1e11, 36), teamSeq.Add(1))
	r := a.at(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": name}, teamKey(p))
	if r.status != http.StatusCreated || r.json(t)["handle"] != name {
		t.Fatalf("create team: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE handle = $1 AND is_team`, name) })
	return name
}

// invite has by invite email to team.
func invite(t *testing.T, a *privateApp, by person, team, email string) resp {
	t.Helper()
	return a.at(t, "POST", teamAPIHost, "/v1/teams/"+team+"/members", map[string]string{"email": email}, teamKey(by))
}

// invitationTo is the id of the invitation to team that p sees when signed
// in (GET /v1/me), or "".
func invitationTo(t *testing.T, a *privateApp, p person, team string) string {
	t.Helper()
	r := a.at(t, "GET", teamAPIHost, "/v1/me", nil, teamKey(p))
	list, _ := r.json(t)["team_invitations"].([]any)
	for _, v := range list {
		if v := v.(map[string]any); v["team"] == team {
			return v["id"].(string)
		}
	}
	return ""
}

func answer(t *testing.T, a *privateApp, p person, id, what string) resp {
	t.Helper()
	return a.at(t, "POST", teamAPIHost, "/v1/me/team-invitations/"+id, map[string]string{"answer": what}, teamKey(p))
}

// addMember has by invite who by email, and who accept.
func addMember(t *testing.T, a *privateApp, by person, team string, who person) {
	t.Helper()
	if r := invite(t, a, by, team, who.email); r.status != http.StatusAccepted {
		t.Fatalf("invite: %d %s", r.status, r.body)
	}
	if r := answer(t, a, who, invitationTo(t, a, who, team), "accept"); r.status != http.StatusOK {
		t.Fatalf("accept: %d %s", r.status, r.body)
	}
}

func memberRows(t *testing.T, a *privateApp, team string) int {
	t.Helper()
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.handle = $1`, team).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func teamDeploy(t *testing.T, a *privateApp, p person, team, site string) resp {
	t.Helper()
	return a.at(t, "PUT", teamAPIHost, "/v1/sites/"+site+"/files?create=1&owner="+team,
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>"}}, teamKey(p))
}

func TestTeamNamesFollowAddressRules(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-names")
	_, annHandle := a.userID(t, ann)
	for name, code := range map[string]string{
		"design.team": "invalid_name",
		"-design":     "invalid_name",
		annHandle:     "name_taken",
		"www":         "name_conflict",
	} {
		r := a.at(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": name}, teamKey(ann))
		if r.status < 400 || r.json(t)["code"] != code {
			t.Fatalf("%q: want %s, got %d %s", name, code, r.status, r.body)
		}
	}
	taken := newTeam(t, a, ann, "names")
	if r := a.at(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": taken}, teamKey(ann)); r.json(t)["code"] != "name_taken" {
		t.Fatalf("second team of one name: %s", r.body)
	}
}

func TestTeamLimitIsTenPerPerson(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, bea := a.newPerson(t, "team-limit"), a.newPerson(t, "team-limit-b")
	var last string
	for i := 0; i < 10; i++ {
		last = newTeam(t, a, ann, "limit")
	}
	if r := a.at(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": "limit-eleven-x"}, teamKey(ann)); r.status != http.StatusConflict || r.json(t)["code"] != "team_limit" {
		t.Fatalf("eleventh team: %d %s", r.status, r.body)
	}
	other := newTeam(t, a, bea, "limit-other")
	invite(t, a, bea, other, ann.email)
	if r := answer(t, a, ann, invitationTo(t, a, ann, other), "accept"); r.json(t)["code"] != "team_limit" {
		t.Fatalf("accepting at the limit: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", teamAPIHost, "/v1/teams", nil, teamKey(ann)); len(r.json(t)["teams"].([]any)) != 10 || !strings.Contains(string(r.body), last) {
		t.Fatalf("list teams: %s", r.body)
	}
}

func TestTeamMemberManagesTeamSitesWithOwnKey(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben := a.newPerson(t, "team-ann"), a.newPerson(t, "team-ben")
	team := newTeam(t, a, ann, "studio")
	addMember(t, a, ann, team, ben)
	_, benHandle := a.userID(t, ben)

	if r := teamDeploy(t, a, ben, team, "shop"); r.status >= 300 || !strings.Contains(r.json(t)["site_url"].(string), team) {
		t.Fatalf("member publish: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, ann, team, "shop"); r.status != http.StatusOK {
		t.Fatalf("other member update: %d %s", r.status, r.body)
	}
	list := a.at(t, "GET", teamAPIHost, "/v1/sites?owner="+team, nil, teamKey(ben))
	if !strings.Contains(string(list.body), `"access_role":"member"`) || !strings.Contains(string(list.body), `"owner":"`+team+`"`) {
		t.Fatalf("team list: %s", list.body)
	}
	if own := a.at(t, "GET", teamAPIHost, "/v1/sites", nil, teamKey(ben)); strings.Contains(string(own.body), `"shop"`) {
		t.Fatalf("a team site listed as the member's own: %s", own.body)
	}
	versions := a.at(t, "GET", teamAPIHost, "/v1/sites/shop/versions?owner="+team, nil, teamKey(ben))
	if !strings.Contains(string(versions.body), `"author":"`+benHandle+`"`) {
		t.Fatalf("the member is not credited: %s", versions.body)
	}
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/active-version?owner="+team, map[string]int{"version_number": 1}, teamKey(ben)); r.status != http.StatusOK {
		t.Fatalf("member rollback: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", teamAPIHost, "/v1/sites/shop?owner="+team, map[string]any{"offline": true}, teamKey(ben)); r.status != http.StatusOK {
		t.Fatalf("member offline: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", teamAPIHost, "/v1/sites/shop?owner="+team, map[string]any{"name": "store"}, teamKey(ben)); r.status != http.StatusOK {
		t.Fatalf("member rename: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/sites/store?owner="+team, nil, teamKey(ben)); r.status >= 300 {
		t.Fatalf("member delete: %d %s", r.status, r.body)
	}
}

func TestTeamNonMemberIsRefused(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, cal := a.newPerson(t, "team-owner"), a.newPerson(t, "team-outsider")
	team := newTeam(t, a, ann, "closed")
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	files := map[string]any{"files": map[string]string{"index.html": "x"}}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/sites?owner=" + team, nil},
		{"PUT", "/v1/sites/shop/files?owner=" + team, files},
		{"PUT", "/v1/sites/shop/active-version?owner=" + team, map[string]int{"version_number": 1}},
		{"DELETE", "/v1/sites/shop?owner=" + team, nil},
		{"GET", "/v1/sites/shop/storage/resources?owner=" + team, nil},
		{"GET", "/v1/teams/" + team + "/members", nil},
		{"POST", "/v1/teams/" + team + "/members", map[string]string{"email": cal.email}},
		{"DELETE", "/v1/teams/" + team, nil},
	} {
		r := a.at(t, c.method, teamAPIHost, c.path, c.body, teamKey(cal))
		if r.status != http.StatusNotFound {
			t.Fatalf("outsider %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	// The admin key keeps its own meaning of owner: it does not act as the team.
	r := a.at(t, "GET", teamAPIHost, "/v1/sites?owner="+team, nil, map[string]string{"X-API-Key": a.admin})
	if r.status != http.StatusOK || strings.Contains(string(r.body), `"shop"`) {
		t.Fatalf("admin with owner: %d %s", r.status, r.body)
	}
}

func TestTeamRemovedMemberIsRefusedOnNextRequest(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben := a.newPerson(t, "team-remover"), a.newPerson(t, "team-removed")
	team := newTeam(t, a, ann, "removal")
	addMember(t, a, ann, team, ben)
	if r := teamDeploy(t, a, ben, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	_, benHandle := a.userID(t, ben)
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+benHandle, nil, teamKey(ann)); r.status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, ben, team, "shop"); r.status != http.StatusNotFound || r.json(t)["code"] != "owner_not_found" {
		t.Fatalf("removed member's next request: %d %s", r.status, r.body)
	}
	var actions string
	if err := a.database.QueryRow(`SELECT string_agg(action, ',' ORDER BY id) FROM team_audit WHERE team_handle = $1`, team).Scan(&actions); err != nil || actions != "create,invite,accept,remove_member" {
		t.Fatalf("audit: %q %v", actions, err)
	}
}

func TestTeamDeployOnlyKeyCannotActForTeam(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-ci")
	team := newTeam(t, a, ann, "ci")
	r := a.at(t, "POST", teamAPIHost, "/v1/me/keys", map[string]string{"name": "CI", "scope": "deploy"}, teamKey(ann))
	if r.status != http.StatusCreated {
		t.Fatalf("deploy key: %d %s", r.status, r.body)
	}
	deploy := person{email: ann.email, key: r.json(t)["api_key"].(string)}
	if r := teamDeploy(t, a, deploy, team, "shop"); r.status != http.StatusForbidden || r.json(t)["code"] != "deploy_only_key" {
		t.Fatalf("deploy-only key for a team: %d %s", r.status, r.body)
	}
}

func TestTeamKeepsAMemberAndDeletesOnlyWhenEmpty(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-last")
	_, annHandle := a.userID(t, ann)
	team := newTeam(t, a, ann, "guards")
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+annHandle, nil, teamKey(ann)); r.status != http.StatusConflict || r.json(t)["code"] != "last_member" || !strings.Contains(r.json(t)["error"].(string), "delete its sites, then delete the team") {
		t.Fatalf("last member leaves: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team, nil, teamKey(ann)); r.status != http.StatusConflict || r.json(t)["code"] != "team_has_sites" {
		t.Fatalf("delete with sites: %d %s", r.status, r.body)
	}
	// A site in Recently deleted does not block; it goes for good with the team.
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/sites/shop?owner="+team, nil, teamKey(ann)); r.status >= 300 {
		t.Fatalf("delete site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team, nil, teamKey(ann)); r.status != http.StatusOK {
		t.Fatalf("delete team with a deleted site: %d %s", r.status, r.body)
	}
	var left int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1`, team).Scan(&left); err != nil || left != 0 {
		t.Fatalf("team's deleted site left behind: %d %v", left, err)
	}
	empty := newTeam(t, a, ann, "empty")
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+empty, nil, teamKey(ann)); r.status != http.StatusOK {
		t.Fatalf("delete empty team: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": empty}, teamKey(ann)); r.json(t)["code"] != "name_taken" {
		t.Fatalf("a deleted team's name is retired: %s", r.body)
	}
}

func TestTeamMemberIsStorageOwnerOnTheSite(t *testing.T) {
	a, cert := newSiteApp(t, "canonical")
	ann, ben, cal := a.newPerson(t, "team-store"), a.newPerson(t, "team-store-b"), a.newPerson(t, "team-store-c")
	team := newTeam(t, a, ann, "store")
	addMember(t, a, ann, team, ben)
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	markReady(t, cert, team)
	host := "shop." + team + "." + pcSiteDomain
	var sid string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1 AND s.name = 'shop'`, team).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	// Rules-less resource: owner-only. The member's key with owner= writes it.
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/resources/notes?owner="+team, map[string]string{"kind": "kv"}, teamKey(ben)); r.status != http.StatusCreated {
		t.Fatalf("member creates resource: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/kv/notes/keys/k?owner="+team, `{"value":"members only"}`, teamKey(ben)); r.status != http.StatusOK {
		t.Fatalf("member writes with key: %d %s", r.status, r.body)
	}
	benSite, calSite := browser(host, a.session(t, ben, sid, host)), browser(host, a.session(t, cal, sid, host))
	if r := a.at(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, benSite); r.status != http.StatusOK {
		t.Fatalf("member signed in on the site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/v1/sites/shop/me", nil, benSite); r.json(t)["site_owner"] != true {
		t.Fatalf("member is not the site's owner there: %s", r.body)
	}
	if r := a.at(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, calSite); r.status != http.StatusForbidden {
		t.Fatalf("non-member signed in on the site: %d %s", r.status, r.body)
	}
	_, benHandle := a.userID(t, ben)
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+benHandle, nil, teamKey(ann)); r.status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, benSite); r.status != http.StatusForbidden {
		t.Fatalf("removed member's next on-site request: %d %s", r.status, r.body)
	}
}

func TestTeamMemberSeesPrivateTeamSite(t *testing.T) {
	a, cert := newSiteApp(t, "canonical")
	ann, ben, cal := a.newPerson(t, "team-view"), a.newPerson(t, "team-view-b"), a.newPerson(t, "team-view-c")
	team := newTeam(t, a, ann, "private")
	addMember(t, a, ann, team, ben)
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/access?owner="+team, map[string]any{"access": "specific"}, teamKey(ann)); r.status >= 300 {
		t.Fatalf("named viewers: %d %s", r.status, r.body)
	}
	markReady(t, cert, team)
	host := "shop." + team + "." + pcSiteDomain
	var sid string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1 AND s.name = 'shop'`, team).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", host, "/", nil, browser(host, a.session(t, ben, sid, host))); r.status != http.StatusOK || !strings.Contains(string(r.body), "<h1>shop</h1>") {
		t.Fatalf("member opens the private team site: %d", r.status)
	}
	if r := a.at(t, "GET", host, "/", nil, browser(host, a.session(t, cal, sid, host))); strings.Contains(string(r.body), "<h1>shop</h1>") {
		t.Fatalf("non-member opened the private team site: %d", r.status)
	}
}

func TestTeamIfMatchRefusesAStaleUpdate(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-ifmatch")
	team := newTeam(t, a, ann, "ifmatch")
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	update := func(ifMatch string) resp {
		h := teamKey(ann)
		h["If-Match"] = ifMatch
		return a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/files?owner="+team, map[string]any{"files": map[string]string{"index.html": "v"}}, h)
	}
	if r := update(`"1"`); r.status != http.StatusOK {
		t.Fatalf("matching If-Match: %d %s", r.status, r.body)
	}
	if r := update(`"1"`); r.status != http.StatusPreconditionFailed || r.json(t)["code"] != "version_changed" {
		t.Fatalf("stale If-Match: %d %s", r.status, r.body)
	}
	if r := update(`"two"`); r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_if_match" {
		t.Fatalf("malformed If-Match: %d %s", r.status, r.body)
	}
}

func TestTeamToolsThroughConnector(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben := a.newPerson(t, "team-ai"), a.newPerson(t, "team-ai-b")
	team := newTeam(t, a, ann, "ai")
	token := a.connect(t, ben, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (string, map[string]any, bool) {
		return toolResultOf(t, a.rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args}))
	}
	if text, _, failed := call("create_site", map[string]any{"site": "shop", "owner": team, "files": map[string]any{"index.html": "<h1>x</h1>"}}); !failed || !strings.Contains(text, "owner_not_found") {
		t.Fatalf("non-member tool call: %s", text)
	}
	addMember(t, a, ann, team, ben)
	if text, _, failed := call("create_site", map[string]any{"site": "shop", "owner": team, "files": map[string]any{"index.html": "<h1>x</h1>"}}); failed {
		t.Fatalf("member create_site: %s", text)
	}
	if text, out, failed := call("list_sites", map[string]any{"owner": team}); failed || !strings.Contains(fmt.Sprint(out["sites"]), "access_role:member") {
		t.Fatalf("list_sites owner: %s", text)
	}
	if text, _, failed := call("update_site", map[string]any{"site": "shop", "owner": team, "expected_version": 9, "files": map[string]any{"index.html": "y"}}); !failed || !strings.Contains(text, "version_changed") {
		t.Fatalf("stale expected_version: %s", text)
	}
	if text, out, failed := call("list_teams", map[string]any{}); failed || !strings.Contains(fmt.Sprint(out["teams"]), team) {
		t.Fatalf("list_teams: %s", text)
	}
	if text, out, failed := call("who_am_i", map[string]any{}); failed || !strings.Contains(fmt.Sprint(out["teams"]), team) {
		t.Fatalf("who_am_i teams: %s", text)
	}
}

func TestTeamDatabaseGuards(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-db")
	team := newTeam(t, a, ann, "db")
	annID, _ := a.userID(t, ann)
	var teamID string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE handle = $1`, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	for what, stmt := range map[string]string{
		"a team holds a key":            `INSERT INTO api_keys (key_hash, user_id) VALUES ('tms1-` + teamID + `', '` + teamID + `')`,
		"a person used as a team":       `INSERT INTO team_members (team_id, user_id) VALUES ('` + annID + `', '` + annID + `')`,
		"a team used as a member":       `INSERT INTO team_members (team_id, user_id) VALUES ('` + teamID + `', '` + teamID + `')`,
		"a member stops being a person": `UPDATE users SET is_team = TRUE WHERE id = '` + annID + `'`,
		"a team stops being a team":     `UPDATE users SET is_team = FALSE WHERE id = '` + teamID + `'`,
	} {
		if _, err := a.database.Exec(stmt); err == nil {
			t.Fatalf("%s: accepted", what)
		}
	}
}

func TestTeamsAbsentOnProductEdition(t *testing.T) {
	a := newPersonApp(t, "canonical", groupTestEdition())
	ann := a.newPerson(t, "team-product")
	if r := a.at(t, "GET", teamAPIHost, "/v1/teams", nil, teamKey(ann)); r.status != http.StatusNotFound {
		t.Fatalf("team routes on the product program: %d %s", r.status, r.body)
	}
	// owner is resolved on every install.
	if r := a.at(t, "GET", teamAPIHost, "/v1/sites?owner=someone-else", nil, teamKey(ann)); r.status != http.StatusNotFound || r.json(t)["code"] != "owner_not_found" {
		t.Fatalf("owner on the product program: %d %s", r.status, r.body)
	}
	// If-Match is not read there either: even a malformed one goes on to the
	// program's own answer.
	files := map[string]any{"files": map[string]string{"index.html": "x"}}
	if r := a.at(t, "PUT", teamAPIHost, "/v1/sites/shop/files?create=1", files, map[string]string{"X-API-Key": ann.key, "If-Match": `"two"`}); r.json(t)["code"] == "invalid_if_match" {
		t.Fatalf("If-Match on the product program: %d %s", r.status, r.body)
	}
}

func TestTeamInviteAnswersAlikeForAnyone(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, cal := a.newPerson(t, "team-inviter"), a.newPerson(t, "team-invitee")
	team := newTeam(t, a, ann, "invites")
	var first resp
	for i, whom := range []string{cal.email, "nobody-x9@example.com"} {
		r := invite(t, a, ann, team, whom)
		if r.status != http.StatusAccepted {
			t.Fatalf("invite %q: %d %s", whom, r.status, r.body)
		}
		if i == 0 {
			first = r
		} else if string(r.body) != string(first.body) {
			t.Fatalf("invite %q answered differently:\n%s\n%s", whom, r.body, first.body)
		}
	}
	if strings.Contains(string(first.body), cal.email) {
		t.Fatalf("the answer names the invitee: %s", first.body)
	}
	if n := memberRows(t, a, team); n != 1 {
		t.Fatalf("an invitation made a member: %d members", n)
	}
	_, calHandle := a.userID(t, cal)
	if r := invite(t, a, ann, team, calHandle); r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_email" {
		t.Fatalf("invitation to a handle: %d %s", r.status, r.body)
	}
	// The members see what was typed; the invitee sees the team, in /v1/me
	// and in GET /v1/teams.
	if r := a.at(t, "GET", teamAPIHost, "/v1/teams/"+team+"/members", nil, teamKey(ann)); len(r.json(t)["invitations"].([]any)) != 2 {
		t.Fatalf("team invitations: %s", r.body)
	}
	if invitationTo(t, a, cal, team) == "" {
		t.Fatal("the invitee does not see the invitation")
	}
	if r := a.at(t, "GET", teamAPIHost, "/v1/teams", nil, teamKey(cal)); len(r.json(t)["invitations"].([]any)) != 1 {
		t.Fatalf("GET /v1/teams invitations: %s", r.body)
	}
}

func TestTeamInvitationAcceptDeclineCancelExpire(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben, cal, dee := a.newPerson(t, "team-ans"), a.newPerson(t, "team-ans-b"), a.newPerson(t, "team-ans-c"), a.newPerson(t, "team-ans-d")
	team := newTeam(t, a, ann, "answers")
	for _, p := range []person{ben, cal, dee} {
		invite(t, a, ann, team, p.email)
	}
	benInv, calInv, deeInv := invitationTo(t, a, ben, team), invitationTo(t, a, cal, team), invitationTo(t, a, dee, team)
	// Only the person addressed can answer.
	if r := answer(t, a, cal, benInv, "accept"); r.status != http.StatusNotFound || r.json(t)["code"] != "invitation_not_found" {
		t.Fatalf("someone else's invitation: %d %s", r.status, r.body)
	}
	if r := answer(t, a, ben, benInv, "accept"); r.status != http.StatusOK || r.json(t)["team"] != team {
		t.Fatalf("accept: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, ben, team, "shop"); r.status >= 300 {
		t.Fatalf("new member publishes: %d %s", r.status, r.body)
	}
	if r := answer(t, a, cal, calInv, "decline"); r.status != http.StatusOK {
		t.Fatalf("decline: %d %s", r.status, r.body)
	}
	if r := teamDeploy(t, a, cal, team, "shop"); r.status != http.StatusNotFound {
		t.Fatalf("decliner acts for the team: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/invitations/"+deeInv, nil, teamKey(ben)); r.status != http.StatusOK {
		t.Fatalf("cancel: %d %s", r.status, r.body)
	}
	if invitationTo(t, a, dee, team) != "" || answer(t, a, dee, deeInv, "accept").status != http.StatusNotFound {
		t.Fatal("a cancelled invitation is still offered")
	}
	invite(t, a, ann, team, dee.email)
	if _, err := a.database.Exec(`UPDATE team_invitations SET expires_at = now() - interval '1 second' WHERE addressed_to = $1`, strings.ToLower(dee.email)); err != nil {
		t.Fatal(err)
	}
	if id := invitationTo(t, a, dee, team); id != "" {
		t.Fatal("an expired invitation is still offered")
	}
	if n := memberRows(t, a, team); n != 2 {
		t.Fatalf("members: %d", n)
	}
	var actions string
	if err := a.database.QueryRow(`SELECT string_agg(action, ',' ORDER BY id) FROM team_audit WHERE team_handle = $1`, team).Scan(&actions); err != nil || actions != "create,invite,invite,invite,accept,decline,cancel,invite" {
		t.Fatalf("audit: %q %v", actions, err)
	}
}

func TestTeamInvitationGoesWhenInviterLeaves(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben, cal := a.newPerson(t, "team-void"), a.newPerson(t, "team-void-b"), a.newPerson(t, "team-void-c")
	team := newTeam(t, a, ann, "void")
	addMember(t, a, ann, team, ben)
	invite(t, a, ben, team, cal.email)
	id := invitationTo(t, a, cal, team)
	_, benHandle := a.userID(t, ben)
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+benHandle, nil, teamKey(ben)); r.status != http.StatusOK {
		t.Fatalf("inviter leaves: %d %s", r.status, r.body)
	}
	addMember(t, a, ann, team, ben)
	if r := answer(t, a, cal, id, "accept"); r.status != http.StatusNotFound || memberRows(t, a, team) != 2 {
		t.Fatalf("accepting an invitation whose sender left and rejoined: %d %s", r.status, r.body)
	}
}

func TestTeamInvitationCaps(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-caps")
	team := newTeam(t, a, ann, "caps")
	for i := 0; i <= 20; i++ {
		want, code := http.StatusAccepted, ""
		if i == 20 {
			want, code = http.StatusConflict, "invitation_limit"
		}
		if r := invite(t, a, ann, team, fmt.Sprintf("someone-%d@example.com", i)); r.status != want || (code != "" && r.json(t)["code"] != code) {
			t.Fatalf("invitation %d: %d %s", i+1, r.status, r.body)
		}
	}
}

func TestTeamSoleMemberAccountDeletionDeletesTheTeam(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, ben := a.newPerson(t, "team-sole"), a.newPerson(t, "team-sole-b")
	_, annHandle := a.userID(t, ann)
	alone, shared := newTeam(t, a, ann, "alone"), newTeam(t, a, ann, "shared")
	addMember(t, a, ann, shared, ben)
	for _, team := range []string{alone, shared} {
		if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
			t.Fatalf("publish: %d %s", r.status, r.body)
		}
	}
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/me", map[string]string{"confirm": annHandle}, teamKey(ann)); r.status != http.StatusNoContent {
		t.Fatalf("sole member deletes their account: %d %s", r.status, r.body)
	}
	var teams, sites int
	if err := a.database.QueryRow(`SELECT count(*) FROM users WHERE handle = $1`, alone).Scan(&teams); err != nil || teams != 0 {
		t.Fatalf("the team they alone belonged to is still there: %d %v", teams, err)
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle IN ($1, $2)`, alone, shared).Scan(&sites); err != nil || sites != 1 {
		t.Fatalf("sites left: %d %v", sites, err)
	}
	if r := a.at(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": alone}, teamKey(ben)); r.json(t)["code"] != "name_taken" {
		t.Fatalf("the deleted team's name is retired: %s", r.body)
	}
	if r := teamDeploy(t, a, ben, shared, "shop"); r.status != http.StatusOK {
		t.Fatalf("the shared team carries on: %d %s", r.status, r.body)
	}
}

// The operator's holds stop a person deleting their account through a team
// they alone belong to, as they stop it for the person's own account, and
// stop the team being deleted.
func TestTeamSoleMemberHoldsBlockDeletion(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-hold")
	_, annHandle := a.userID(t, ann)
	team := newTeam(t, a, ann, "hold")
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	del := func() resp {
		return a.at(t, "DELETE", teamAPIHost, "/v1/me", map[string]string{"confirm": annHandle}, teamKey(ann))
	}
	if _, err := a.database.Exec(`UPDATE sites SET suspended_at = now() WHERE name = 'shop' AND user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if r := del(); r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" {
		t.Fatalf("delete with a taken-down team site: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`DELETE FROM sites WHERE user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET suspended_at = now() WHERE handle = $1`, team); err != nil {
		t.Fatal(err)
	}
	if r := del(); r.status != http.StatusForbidden || r.json(t)["code"] != "account_suspended" {
		t.Fatalf("delete with a suspended team: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", teamAPIHost, "/v1/teams/"+team, nil, teamKey(ann)); r.status != http.StatusForbidden || r.json(t)["code"] != "account_suspended" {
		t.Fatalf("delete a suspended team: %d %s", r.status, r.body)
	}
	if memberRows(t, a, team) != 1 {
		t.Fatal("a refused deletion changed the team")
	}
}

// An invitation reaches only the account whose verified sign-in email it
// is, exactly: no +tag folding, and never an unverified self-signup address.
func TestTeamInvitationOnlyForVerifiedSignInEmail(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann, bob := a.newPerson(t, "team-email"), a.newPerson(t, "team-email-b")
	team := newTeam(t, a, ann, "email")
	invite(t, a, ann, team, strings.Replace(bob.email, "@", "+x@", 1))
	if invitationTo(t, a, bob, team) != "" {
		t.Fatal("an invitation to a +tag address reached the plain one")
	}
	invite(t, a, ann, team, strings.ToUpper(bob.email))
	if invitationTo(t, a, bob, team) == "" {
		t.Fatal("an invitation to the sign-in email, in capitals, did not reach it")
	}
	eve := a.newPerson(t, "team-email-e")
	if _, err := a.database.Exec(`UPDATE users SET signup_method = 'password', signup_source = 'website' WHERE username = $1`, eve.email); err != nil {
		t.Fatal(err)
	}
	invite(t, a, ann, team, eve.email)
	if invitationTo(t, a, eve, team) != "" {
		t.Fatal("an unverified self-signup email sees an invitation")
	}
}

func TestTeamOwnerThatIsNoHandle(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-query")
	team := newTeam(t, a, ann, "query")
	if r := a.at(t, "GET", teamAPIHost, "/v1/sites?owner="+team+"%00", nil, teamKey(ann)); r.status != http.StatusNotFound || r.json(t)["code"] != "owner_not_found" {
		t.Fatalf("owner with a NUL byte: %d %s", r.status, r.body)
	}
}

func TestTeamHasNoKeyToReissue(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	ann := a.newPerson(t, "team-reissue")
	team := newTeam(t, a, ann, "reissue")
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE handle = $1`, team).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", teamAPIHost, "/v1/admin/users/"+id+"/key", nil, map[string]string{"X-API-Key": a.admin}); r.status != http.StatusConflict || r.json(t)["code"] != "team_account" {
		t.Fatalf("reissue a team's key: %d %s", r.status, r.body)
	}
}

func TestTeamSiteOffersNoPush(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	a.sites.SetWebPushAccounts(nil, true)
	ann := a.newPerson(t, "team-push")
	team := newTeam(t, a, ann, "push")
	if r := teamDeploy(t, a, ann, team, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	r := a.at(t, "POST", teamAPIHost, "/v1/sites/shop/push/subscribe?owner="+team, pushSubscribeBody(t, "https://push.example/x", ""), teamKey(ann))
	if r.status != http.StatusNotFound || r.json(t)["code"] != "not_offered" {
		t.Fatalf("push on a team site: %d %s", r.status, r.body)
	}
}

// An admin's own account is a person: it creates, invites, is invited and
// acts for its teams with owner=<team>, and has no power over other teams.
func TestTeamAdminAccountIsAPerson(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	boss, ann, ben := a.newPerson(t, "team-admin"), a.newPerson(t, "team-admin-ann"), a.newPerson(t, "team-admin-ben")
	bossID, _ := a.userID(t, boss)
	if _, err := a.database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, bossID); err != nil {
		t.Fatal(err)
	}
	own := newTeam(t, a, boss, "admin-own")
	addMember(t, a, boss, own, ben)
	if r := teamDeploy(t, a, boss, own, "shop"); r.status >= 300 || !strings.Contains(r.json(t)["site_url"].(string), own) {
		t.Fatalf("admin publish for own team: %d %s", r.status, r.body)
	}
	joined := newTeam(t, a, ann, "admin-joined")
	addMember(t, a, ann, joined, boss)
	if r := teamDeploy(t, a, boss, joined, "shop"); r.status >= 300 || !strings.Contains(r.json(t)["site_url"].(string), joined) {
		t.Fatalf("admin publish for joined team: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", teamAPIHost, "/v1/me", nil, teamKey(boss)); !strings.Contains(string(r.body), own) || !strings.Contains(string(r.body), joined) {
		t.Fatalf("admin's teams on /v1/me: %s", r.body)
	}

	// Not a member: the same refusal as anyone on the /v1/sites routes.
	other := newTeam(t, a, ann, "admin-other")
	if r := teamDeploy(t, a, ann, other, "shop"); r.status >= 300 {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/sites?owner=" + other},
		{"PUT", "/v1/sites/shop/files?owner=" + other},
		{"DELETE", "/v1/sites/shop?owner=" + other},
		{"GET", "/v1/teams/" + other + "/members"},
	} {
		r := a.at(t, c.method, teamAPIHost, c.path, map[string]any{"files": map[string]string{"index.html": "x"}}, teamKey(boss))
		if r.status != http.StatusNotFound {
			t.Fatalf("admin outsider %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}

	// owner on the analytics routes keeps meaning "an admin reads another
	// account's analytics" for an account that is not one of the admin's teams.
	a.deploy(t, ben, "bens")
	_, benHandle := a.userID(t, ben)
	if r := a.at(t, "GET", teamAPIHost, "/v1/sites/bens/analytics?days=7&owner="+benHandle, nil, teamKey(boss)); r.status != http.StatusOK {
		t.Fatalf("admin analytics for another account: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", teamAPIHost, "/v1/sites/shop/analytics?days=7&owner="+own, nil, teamKey(boss)); r.status != http.StatusOK {
		t.Fatalf("admin analytics for own team: %d %s", r.status, r.body)
	}
}
