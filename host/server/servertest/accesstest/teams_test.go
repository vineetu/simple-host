package accesstest

import (
	"fmt"
	"github.com/vsriram/simple-host/host/server/servertest"
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

func teamKey(p servertest.Person) map[string]string { return map[string]string{"X-API-Key": p.Key} }

// newTeam has p create a team named after label, returning its handle.
func newTeam(t *testing.T, a *servertest.PrivateApp, p servertest.Person, label string) string {
	t.Helper()
	name := fmt.Sprintf("%s-%s-%d", label, strconv.FormatInt(time.Now().UnixNano()%1e11, 36), teamSeq.Add(1))
	r := a.At(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": name}, teamKey(p))
	if r.Status != http.StatusCreated || r.JSON(t)["handle"] != name {
		t.Fatalf("create team: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE handle = $1 AND is_team`, name) })
	return name
}

// invite has by invite email to team.
func invite(t *testing.T, a *servertest.PrivateApp, by servertest.Person, team, email string) servertest.Resp {
	t.Helper()
	return a.At(t, "POST", teamAPIHost, "/v1/teams/"+team+"/members", map[string]string{"email": email}, teamKey(by))
}

// invitationTo is the id of the invitation to team that p sees when signed
// in (GET /v1/me), or "".
func invitationTo(t *testing.T, a *servertest.PrivateApp, p servertest.Person, team string) string {
	t.Helper()
	r := a.At(t, "GET", teamAPIHost, "/v1/me", nil, teamKey(p))
	list, _ := r.JSON(t)["team_invitations"].([]any)
	for _, v := range list {
		if v := v.(map[string]any); v["team"] == team {
			return v["id"].(string)
		}
	}
	return ""
}

func answer(t *testing.T, a *servertest.PrivateApp, p servertest.Person, id, what string) servertest.Resp {
	t.Helper()
	return a.At(t, "POST", teamAPIHost, "/v1/me/team-invitations/"+id, map[string]string{"answer": what}, teamKey(p))
}

// addMember has by invite who by email, and who accept.
func addMember(t *testing.T, a *servertest.PrivateApp, by servertest.Person, team string, who servertest.Person) {
	t.Helper()
	if r := invite(t, a, by, team, who.Email); r.Status != http.StatusAccepted {
		t.Fatalf("invite: %d %s", r.Status, r.Body)
	}
	if r := answer(t, a, who, invitationTo(t, a, who, team), "accept"); r.Status != http.StatusOK {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
}

func memberRows(t *testing.T, a *servertest.PrivateApp, team string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.handle = $1`, team).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func teamDeploy(t *testing.T, a *servertest.PrivateApp, p servertest.Person, team, site string) servertest.Resp {
	t.Helper()
	return a.At(t, "PUT", teamAPIHost, "/v1/sites/"+site+"/files?create=1&owner="+team,
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>"}}, teamKey(p))
}

func TestTeamNamesFollowAddressRules(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-names")
	_, annHandle := a.UserID(t, ann)
	for name, code := range map[string]string{
		"design.team": "invalid_name",
		"-design":     "invalid_name",
		annHandle:     "name_taken",
		"www":         "name_conflict",
	} {
		r := a.At(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": name}, teamKey(ann))
		if r.Status < 400 || r.JSON(t)["code"] != code {
			t.Fatalf("%q: want %s, got %d %s", name, code, r.Status, r.Body)
		}
	}
	taken := newTeam(t, a, ann, "names")
	if r := a.At(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": taken}, teamKey(ann)); r.JSON(t)["code"] != "name_taken" {
		t.Fatalf("second team of one name: %s", r.Body)
	}
}

func TestTeamLimitIsTenPerPerson(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, bea := a.NewPerson(t, "team-limit"), a.NewPerson(t, "team-limit-b")
	var last string
	for i := 0; i < 10; i++ {
		last = newTeam(t, a, ann, "limit")
	}
	if r := a.At(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": "limit-eleven-x"}, teamKey(ann)); r.Status != http.StatusConflict || r.JSON(t)["code"] != "team_limit" {
		t.Fatalf("eleventh team: %d %s", r.Status, r.Body)
	}
	other := newTeam(t, a, bea, "limit-other")
	invite(t, a, bea, other, ann.Email)
	if r := answer(t, a, ann, invitationTo(t, a, ann, other), "accept"); r.JSON(t)["code"] != "team_limit" {
		t.Fatalf("accepting at the limit: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", teamAPIHost, "/v1/teams", nil, teamKey(ann)); len(r.JSON(t)["teams"].([]any)) != 10 || !strings.Contains(string(r.Body), last) {
		t.Fatalf("list teams: %s", r.Body)
	}
}

func TestTeamMemberManagesTeamSitesWithOwnKey(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben := a.NewPerson(t, "team-ann"), a.NewPerson(t, "team-ben")
	team := newTeam(t, a, ann, "studio")
	addMember(t, a, ann, team, ben)
	_, benHandle := a.UserID(t, ben)

	if r := teamDeploy(t, a, ben, team, "shop"); r.Status >= 300 || !strings.Contains(r.JSON(t)["site_url"].(string), team) {
		t.Fatalf("member publish: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status != http.StatusOK {
		t.Fatalf("other member update: %d %s", r.Status, r.Body)
	}
	list := a.At(t, "GET", teamAPIHost, "/v1/sites?owner="+team, nil, teamKey(ben))
	if !strings.Contains(string(list.Body), `"access_role":"member"`) || !strings.Contains(string(list.Body), `"owner":"`+team+`"`) {
		t.Fatalf("team list: %s", list.Body)
	}
	if own := a.At(t, "GET", teamAPIHost, "/v1/sites", nil, teamKey(ben)); strings.Contains(string(own.Body), `"shop"`) {
		t.Fatalf("a team site listed as the member's own: %s", own.Body)
	}
	versions := a.At(t, "GET", teamAPIHost, "/v1/sites/shop/versions?owner="+team, nil, teamKey(ben))
	if !strings.Contains(string(versions.Body), `"author":"`+benHandle+`"`) {
		t.Fatalf("the member is not credited: %s", versions.Body)
	}
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/active-version?owner="+team, map[string]int{"version_number": 1}, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("member rollback: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", teamAPIHost, "/v1/sites/shop?owner="+team, map[string]any{"offline": true}, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("member offline: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", teamAPIHost, "/v1/sites/shop?owner="+team, map[string]any{"name": "store"}, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("member rename: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/sites/store?owner="+team, nil, teamKey(ben)); r.Status >= 300 {
		t.Fatalf("member delete: %d %s", r.Status, r.Body)
	}
}

func TestTeamNonMemberIsRefused(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, cal := a.NewPerson(t, "team-owner"), a.NewPerson(t, "team-outsider")
	team := newTeam(t, a, ann, "closed")
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
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
		{"POST", "/v1/teams/" + team + "/members", map[string]string{"email": cal.Email}},
		{"DELETE", "/v1/teams/" + team, nil},
	} {
		r := a.At(t, c.method, teamAPIHost, c.path, c.body, teamKey(cal))
		if r.Status != http.StatusNotFound {
			t.Fatalf("outsider %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	// The admin key keeps its own meaning of owner: it does not act as the team.
	r := a.At(t, "GET", teamAPIHost, "/v1/sites?owner="+team, nil, map[string]string{"X-API-Key": a.Admin})
	if r.Status != http.StatusOK || strings.Contains(string(r.Body), `"shop"`) {
		t.Fatalf("admin with owner: %d %s", r.Status, r.Body)
	}
}

func TestTeamRemovedMemberIsRefusedOnNextRequest(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben := a.NewPerson(t, "team-remover"), a.NewPerson(t, "team-removed")
	team := newTeam(t, a, ann, "removal")
	addMember(t, a, ann, team, ben)
	if r := teamDeploy(t, a, ben, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	_, benHandle := a.UserID(t, ben)
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+benHandle, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, ben, team, "shop"); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "owner_not_found" {
		t.Fatalf("removed member's next request: %d %s", r.Status, r.Body)
	}
	var actions string
	if err := a.Database.QueryRow(`SELECT string_agg(action, ',' ORDER BY id) FROM team_audit WHERE team_handle = $1`, team).Scan(&actions); err != nil || actions != "create,invite,accept,remove_member" {
		t.Fatalf("audit: %q %v", actions, err)
	}
}

func TestTeamDeployOnlyKeyCannotActForTeam(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-ci")
	team := newTeam(t, a, ann, "ci")
	r := a.At(t, "POST", teamAPIHost, "/v1/me/keys", map[string]string{"name": "CI", "scope": "deploy"}, teamKey(ann))
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy key: %d %s", r.Status, r.Body)
	}
	deploy := servertest.Person{Email: ann.Email, Key: r.JSON(t)["api_key"].(string)}
	if r := teamDeploy(t, a, deploy, team, "shop"); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "deploy_only_key" {
		t.Fatalf("deploy-only key for a team: %d %s", r.Status, r.Body)
	}
}

func TestTeamKeepsAMemberAndDeletesOnlyWhenEmpty(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-last")
	_, annHandle := a.UserID(t, ann)
	team := newTeam(t, a, ann, "guards")
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+annHandle, nil, teamKey(ann)); r.Status != http.StatusConflict || r.JSON(t)["code"] != "last_member" || !strings.Contains(r.JSON(t)["error"].(string), "delete its sites, then delete the team") {
		t.Fatalf("last member leaves: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team, nil, teamKey(ann)); r.Status != http.StatusConflict || r.JSON(t)["code"] != "team_has_sites" {
		t.Fatalf("delete with sites: %d %s", r.Status, r.Body)
	}
	// A site in Recently deleted does not block; it goes for good with the team.
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/sites/shop?owner="+team, nil, teamKey(ann)); r.Status >= 300 {
		t.Fatalf("delete site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("delete team with a deleted site: %d %s", r.Status, r.Body)
	}
	var left int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1`, team).Scan(&left); err != nil || left != 0 {
		t.Fatalf("team's deleted site left behind: %d %v", left, err)
	}
	empty := newTeam(t, a, ann, "empty")
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+empty, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("delete empty team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": empty}, teamKey(ann)); r.JSON(t)["code"] != "name_taken" {
		t.Fatalf("a deleted team's name is retired: %s", r.Body)
	}
}

func TestTeamMemberIsStorageOwnerOnTheSite(t *testing.T) {
	a, cert := servertest.NewSiteApp(t, "canonical")
	ann, ben, cal := a.NewPerson(t, "team-store"), a.NewPerson(t, "team-store-b"), a.NewPerson(t, "team-store-c")
	team := newTeam(t, a, ann, "store")
	addMember(t, a, ann, team, ben)
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	servertest.MarkReady(t, cert, team)
	host := "shop." + team + "." + servertest.PcSiteDomain
	var sid string
	if err := a.Database.QueryRow(`SELECT s.id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1 AND s.name = 'shop'`, team).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	// Rules-less resource: owner-only. The member's key with owner= writes it.
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/resources/notes?owner="+team, map[string]string{"kind": "kv"}, teamKey(ben)); r.Status != http.StatusCreated {
		t.Fatalf("member creates resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/storage/kv/notes/keys/k?owner="+team, `{"value":"members only"}`, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("member writes with key: %d %s", r.Status, r.Body)
	}
	benSite, calSite := servertest.Browser(host, a.Session(t, ben, sid, host)), servertest.Browser(host, a.Session(t, cal, sid, host))
	if r := a.At(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, benSite); r.Status != http.StatusOK {
		t.Fatalf("member signed in on the site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/me", nil, benSite); r.JSON(t)["site_owner"] != true {
		t.Fatalf("member is not the site's owner there: %s", r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, calSite); r.Status != http.StatusForbidden {
		t.Fatalf("non-member signed in on the site: %d %s", r.Status, r.Body)
	}
	_, benHandle := a.UserID(t, ben)
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+benHandle, nil, teamKey(ann)); r.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/v1/sites/shop/storage/kv/notes/keys/k", nil, benSite); r.Status != http.StatusForbidden {
		t.Fatalf("removed member's next on-site request: %d %s", r.Status, r.Body)
	}
}

func TestTeamMemberSeesPrivateTeamSite(t *testing.T) {
	a, cert := servertest.NewSiteApp(t, "canonical")
	ann, ben, cal := a.NewPerson(t, "team-view"), a.NewPerson(t, "team-view-b"), a.NewPerson(t, "team-view-c")
	team := newTeam(t, a, ann, "private")
	addMember(t, a, ann, team, ben)
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/access?owner="+team, map[string]any{"access": "specific"}, teamKey(ann)); r.Status >= 300 {
		t.Fatalf("named viewers: %d %s", r.Status, r.Body)
	}
	servertest.MarkReady(t, cert, team)
	host := "shop." + team + "." + servertest.PcSiteDomain
	var sid string
	if err := a.Database.QueryRow(`SELECT s.id FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle = $1 AND s.name = 'shop'`, team).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", host, "/", nil, servertest.Browser(host, a.Session(t, ben, sid, host))); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Fatalf("member opens the private team site: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/", nil, servertest.Browser(host, a.Session(t, cal, sid, host))); strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Fatalf("non-member opened the private team site: %d", r.Status)
	}
}

func TestTeamIfMatchRefusesAStaleUpdate(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-ifmatch")
	team := newTeam(t, a, ann, "ifmatch")
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	update := func(ifMatch string) servertest.Resp {
		h := teamKey(ann)
		h["If-Match"] = ifMatch
		return a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/files?owner="+team, map[string]any{"files": map[string]string{"index.html": "v"}}, h)
	}
	if r := update(`"1"`); r.Status != http.StatusOK {
		t.Fatalf("matching If-Match: %d %s", r.Status, r.Body)
	}
	if r := update(`"1"`); r.Status != http.StatusPreconditionFailed || r.JSON(t)["code"] != "version_changed" {
		t.Fatalf("stale If-Match: %d %s", r.Status, r.Body)
	}
	if r := update(`"two"`); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_if_match" {
		t.Fatalf("malformed If-Match: %d %s", r.Status, r.Body)
	}
}

func TestTeamToolsThroughConnector(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben := a.NewPerson(t, "team-ai"), a.NewPerson(t, "team-ai-b")
	team := newTeam(t, a, ann, "ai")
	token := a.Connect(t, ben, a.RegisterClient(t, servertest.TestRedirect), servertest.TestRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (string, map[string]any, bool) {
		return servertest.ToolResultOf(t, a.Rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args}))
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
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-db")
	team := newTeam(t, a, ann, "db")
	annID, _ := a.UserID(t, ann)
	var teamID string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE handle = $1`, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	for what, stmt := range map[string]string{
		"a team holds a key":            `INSERT INTO api_keys (key_hash, user_id) VALUES ('tms1-` + teamID + `', '` + teamID + `')`,
		"a person used as a team":       `INSERT INTO team_members (team_id, user_id) VALUES ('` + annID + `', '` + annID + `')`,
		"a team used as a member":       `INSERT INTO team_members (team_id, user_id) VALUES ('` + teamID + `', '` + teamID + `')`,
		"a member stops being a person": `UPDATE users SET is_team = TRUE WHERE id = '` + annID + `'`,
		"a team stops being a team":     `UPDATE users SET is_team = FALSE WHERE id = '` + teamID + `'`,
	} {
		if _, err := a.Database.Exec(stmt); err == nil {
			t.Fatalf("%s: accepted", what)
		}
	}
}

func TestTeamsAbsentOnProductEdition(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical", servertest.GroupTestEdition())
	ann := a.NewPerson(t, "team-product")
	if r := a.At(t, "GET", teamAPIHost, "/v1/teams", nil, teamKey(ann)); r.Status != http.StatusNotFound {
		t.Fatalf("team routes on the product program: %d %s", r.Status, r.Body)
	}
	// owner is resolved on every install.
	if r := a.At(t, "GET", teamAPIHost, "/v1/sites?owner=someone-else", nil, teamKey(ann)); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "owner_not_found" {
		t.Fatalf("owner on the product program: %d %s", r.Status, r.Body)
	}
	// If-Match is not read there either: even a malformed one goes on to the
	// program's own answer.
	files := map[string]any{"files": map[string]string{"index.html": "x"}}
	if r := a.At(t, "PUT", teamAPIHost, "/v1/sites/shop/files?create=1", files, map[string]string{"X-API-Key": ann.Key, "If-Match": `"two"`}); r.JSON(t)["code"] == "invalid_if_match" {
		t.Fatalf("If-Match on the product program: %d %s", r.Status, r.Body)
	}
}

func TestTeamInviteAnswersAlikeForAnyone(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, cal := a.NewPerson(t, "team-inviter"), a.NewPerson(t, "team-invitee")
	team := newTeam(t, a, ann, "invites")
	var first servertest.Resp
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
	_, calHandle := a.UserID(t, cal)
	if r := invite(t, a, ann, team, calHandle); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_email" {
		t.Fatalf("invitation to a handle: %d %s", r.Status, r.Body)
	}
	// The members see what was typed; the invitee sees the team, in /v1/me
	// and in GET /v1/teams.
	if r := a.At(t, "GET", teamAPIHost, "/v1/teams/"+team+"/members", nil, teamKey(ann)); len(r.JSON(t)["invitations"].([]any)) != 2 {
		t.Fatalf("team invitations: %s", r.Body)
	}
	if invitationTo(t, a, cal, team) == "" {
		t.Fatal("the invitee does not see the invitation")
	}
	if r := a.At(t, "GET", teamAPIHost, "/v1/teams", nil, teamKey(cal)); len(r.JSON(t)["invitations"].([]any)) != 1 {
		t.Fatalf("GET /v1/teams invitations: %s", r.Body)
	}
}

func TestTeamInvitationAcceptDeclineCancelExpire(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben, cal, dee := a.NewPerson(t, "team-ans"), a.NewPerson(t, "team-ans-b"), a.NewPerson(t, "team-ans-c"), a.NewPerson(t, "team-ans-d")
	team := newTeam(t, a, ann, "answers")
	for _, p := range []servertest.Person{ben, cal, dee} {
		invite(t, a, ann, team, p.Email)
	}
	benInv, calInv, deeInv := invitationTo(t, a, ben, team), invitationTo(t, a, cal, team), invitationTo(t, a, dee, team)
	// Only the person addressed can answer.
	if r := answer(t, a, cal, benInv, "accept"); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "invitation_not_found" {
		t.Fatalf("someone else's invitation: %d %s", r.Status, r.Body)
	}
	if r := answer(t, a, ben, benInv, "accept"); r.Status != http.StatusOK || r.JSON(t)["team"] != team {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, ben, team, "shop"); r.Status >= 300 {
		t.Fatalf("new member publishes: %d %s", r.Status, r.Body)
	}
	if r := answer(t, a, cal, calInv, "decline"); r.Status != http.StatusOK {
		t.Fatalf("decline: %d %s", r.Status, r.Body)
	}
	if r := teamDeploy(t, a, cal, team, "shop"); r.Status != http.StatusNotFound {
		t.Fatalf("decliner acts for the team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/invitations/"+deeInv, nil, teamKey(ben)); r.Status != http.StatusOK {
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
	var actions string
	if err := a.Database.QueryRow(`SELECT string_agg(action, ',' ORDER BY id) FROM team_audit WHERE team_handle = $1`, team).Scan(&actions); err != nil || actions != "create,invite,invite,invite,accept,decline,cancel,invite" {
		t.Fatalf("audit: %q %v", actions, err)
	}
}

func TestTeamInvitationGoesWhenInviterLeaves(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben, cal := a.NewPerson(t, "team-void"), a.NewPerson(t, "team-void-b"), a.NewPerson(t, "team-void-c")
	team := newTeam(t, a, ann, "void")
	addMember(t, a, ann, team, ben)
	invite(t, a, ben, team, cal.Email)
	id := invitationTo(t, a, cal, team)
	_, benHandle := a.UserID(t, ben)
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team+"/members/"+benHandle, nil, teamKey(ben)); r.Status != http.StatusOK {
		t.Fatalf("inviter leaves: %d %s", r.Status, r.Body)
	}
	addMember(t, a, ann, team, ben)
	if r := answer(t, a, cal, id, "accept"); r.Status != http.StatusNotFound || memberRows(t, a, team) != 2 {
		t.Fatalf("accepting an invitation whose sender left and rejoined: %d %s", r.Status, r.Body)
	}
}

func TestTeamInvitationCaps(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-caps")
	team := newTeam(t, a, ann, "caps")
	for i := 0; i <= 20; i++ {
		want, code := http.StatusAccepted, ""
		if i == 20 {
			want, code = http.StatusConflict, "invitation_limit"
		}
		if r := invite(t, a, ann, team, fmt.Sprintf("someone-%d@example.com", i)); r.Status != want || (code != "" && r.JSON(t)["code"] != code) {
			t.Fatalf("invitation %d: %d %s", i+1, r.Status, r.Body)
		}
	}
}

func TestTeamSoleMemberAccountDeletionDeletesTheTeam(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann, ben := a.NewPerson(t, "team-sole"), a.NewPerson(t, "team-sole-b")
	_, annHandle := a.UserID(t, ann)
	alone, shared := newTeam(t, a, ann, "alone"), newTeam(t, a, ann, "shared")
	addMember(t, a, ann, shared, ben)
	for _, team := range []string{alone, shared} {
		if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
			t.Fatalf("publish: %d %s", r.Status, r.Body)
		}
	}
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/me", map[string]string{"confirm": annHandle}, teamKey(ann)); r.Status != http.StatusNoContent {
		t.Fatalf("sole member deletes their account: %d %s", r.Status, r.Body)
	}
	var teams, sites int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE handle = $1`, alone).Scan(&teams); err != nil || teams != 0 {
		t.Fatalf("the team they alone belonged to is still there: %d %v", teams, err)
	}
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.handle IN ($1, $2)`, alone, shared).Scan(&sites); err != nil || sites != 1 {
		t.Fatalf("sites left: %d %v", sites, err)
	}
	if r := a.At(t, "POST", teamAPIHost, "/v1/teams", map[string]string{"name": alone}, teamKey(ben)); r.JSON(t)["code"] != "name_taken" {
		t.Fatalf("the deleted team's name is retired: %s", r.Body)
	}
	if r := teamDeploy(t, a, ben, shared, "shop"); r.Status != http.StatusOK {
		t.Fatalf("the shared team carries on: %d %s", r.Status, r.Body)
	}
}

// The operator's holds stop a person deleting their account through a team
// they alone belong to, as they stop it for the person's own account, and
// stop the team being deleted.
func TestTeamSoleMemberHoldsBlockDeletion(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-hold")
	_, annHandle := a.UserID(t, ann)
	team := newTeam(t, a, ann, "hold")
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	del := func() servertest.Resp {
		return a.At(t, "DELETE", teamAPIHost, "/v1/me", map[string]string{"confirm": annHandle}, teamKey(ann))
	}
	if _, err := a.Database.Exec(`UPDATE sites SET suspended_at = now() WHERE name = 'shop' AND user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if r := del(); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
		t.Fatalf("delete with a taken-down team site: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`DELETE FROM sites WHERE user_id = (SELECT id FROM users WHERE handle = $1)`, team); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at = now() WHERE handle = $1`, team); err != nil {
		t.Fatal(err)
	}
	if r := del(); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "account_suspended" {
		t.Fatalf("delete with a suspended team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", teamAPIHost, "/v1/teams/"+team, nil, teamKey(ann)); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "account_suspended" {
		t.Fatalf("delete a suspended team: %d %s", r.Status, r.Body)
	}
	if memberRows(t, a, team) != 1 {
		t.Fatal("a refused deletion changed the team")
	}
}

// An invitation reaches only the account whose verified sign-in email it
// is, exactly: no +tag folding, and never an unverified self-signup address.
func TestTeamInvitationOnlyForVerifiedSignInEmail(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
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
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-query")
	team := newTeam(t, a, ann, "query")
	if r := a.At(t, "GET", teamAPIHost, "/v1/sites?owner="+team+"%00", nil, teamKey(ann)); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "owner_not_found" {
		t.Fatalf("owner with a NUL byte: %d %s", r.Status, r.Body)
	}
}

func TestTeamHasNoKeyToReissue(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	ann := a.NewPerson(t, "team-reissue")
	team := newTeam(t, a, ann, "reissue")
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE handle = $1`, team).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", teamAPIHost, "/v1/admin/users/"+id+"/key", nil, map[string]string{"X-API-Key": a.Admin}); r.Status != http.StatusConflict || r.JSON(t)["code"] != "team_account" {
		t.Fatalf("reissue a team's key: %d %s", r.Status, r.Body)
	}
}

func TestTeamSiteOffersNoPush(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	a.Data.SetWebPushAccounts(nil, true)
	ann := a.NewPerson(t, "team-push")
	team := newTeam(t, a, ann, "push")
	if r := teamDeploy(t, a, ann, team, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "POST", teamAPIHost, "/v1/sites/shop/push/subscribe?owner="+team, servertest.PushSubscribeBody(t, "https://push.example/x", ""), teamKey(ann))
	if r.Status != http.StatusNotFound || r.JSON(t)["code"] != "not_offered" {
		t.Fatalf("push on a team site: %d %s", r.Status, r.Body)
	}
}

// An admin's own account is a person: it creates, invites, is invited and
// acts for its teams with owner=<team>, and has no power over other teams.
func TestTeamAdminAccountIsAPerson(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	boss, ann, ben := a.NewPerson(t, "team-admin"), a.NewPerson(t, "team-admin-ann"), a.NewPerson(t, "team-admin-ben")
	bossID, _ := a.UserID(t, boss)
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, bossID); err != nil {
		t.Fatal(err)
	}
	own := newTeam(t, a, boss, "admin-own")
	addMember(t, a, boss, own, ben)
	if r := teamDeploy(t, a, boss, own, "shop"); r.Status >= 300 || !strings.Contains(r.JSON(t)["site_url"].(string), own) {
		t.Fatalf("admin publish for own team: %d %s", r.Status, r.Body)
	}
	joined := newTeam(t, a, ann, "admin-joined")
	addMember(t, a, ann, joined, boss)
	if r := teamDeploy(t, a, boss, joined, "shop"); r.Status >= 300 || !strings.Contains(r.JSON(t)["site_url"].(string), joined) {
		t.Fatalf("admin publish for joined team: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", teamAPIHost, "/v1/me", nil, teamKey(boss)); !strings.Contains(string(r.Body), own) || !strings.Contains(string(r.Body), joined) {
		t.Fatalf("admin's teams on /v1/me: %s", r.Body)
	}

	// Not a member: the same refusal as anyone on the /v1/sites routes.
	other := newTeam(t, a, ann, "admin-other")
	if r := teamDeploy(t, a, ann, other, "shop"); r.Status >= 300 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/sites?owner=" + other},
		{"PUT", "/v1/sites/shop/files?owner=" + other},
		{"DELETE", "/v1/sites/shop?owner=" + other},
		{"GET", "/v1/teams/" + other + "/members"},
	} {
		r := a.At(t, c.method, teamAPIHost, c.path, map[string]any{"files": map[string]string{"index.html": "x"}}, teamKey(boss))
		if r.Status != http.StatusNotFound {
			t.Fatalf("admin outsider %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}

	// owner on the analytics routes keeps meaning "an admin reads another
	// account's analytics" for an account that is not one of the admin's teams.
	a.Deploy(t, ben, "bens")
	_, benHandle := a.UserID(t, ben)
	if r := a.At(t, "GET", teamAPIHost, "/v1/sites/bens/analytics?days=7&owner="+benHandle, nil, teamKey(boss)); r.Status != http.StatusOK {
		t.Fatalf("admin analytics for another account: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", teamAPIHost, "/v1/sites/shop/analytics?days=7&owner="+own, nil, teamKey(boss)); r.Status != http.StatusOK {
		t.Fatalf("admin analytics for own team: %d %s", r.Status, r.Body)
	}
}
