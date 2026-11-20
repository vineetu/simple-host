package servertest

import (
	"context"
	"database/sql"
	"encoding/json"
	"hash/crc32"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	siterepo "github.com/vsriram/simple-host/host/repository/sites"
	"github.com/vsriram/simple-host/products/hack"
	teamsapp "github.com/vsriram/simple-host/products/hack/app/teams"
)

// M2: team sites published with the member's own key and owner=<event>, the
// deadline freeze, organiser moderation and the connector — wired the way
// cmd/server/main.go wires hosted mode (scope gate, connector, host routing).

func TestHackM2TeamSitesEndToEnd(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1, p2, p3 := a.newPerson(t, "org"), a.newPerson(t, "p1"), a.newPerson(t, "p2"), a.newPerson(t, "p3")
	slug := a.makeEvent(t, org)
	// Opening the event asked for its certificate.
	if _, err := os.Stat(filepath.Join(a.certDir, "requests", slug)); err != nil {
		t.Fatalf("no certificate request on open: %v", err)
	}
	for _, p := range []person{p1, p2, p3} {
		a.join(t, slug, p, org)
	}
	alpha, alphaCode := a.startTeam(t, slug, "Alpha", p1)
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": alphaCode}, p2.key); r.status != 200 {
		t.Fatalf("join team: %d %s", r.status, r.body)
	}
	beta, _ := a.startTeam(t, slug, "Beta", p3)
	k1, k2, k3 := p1.key, p2.key, p3.key

	// Not ready: nothing lands anywhere.
	wantTS(t, "deploy before ready", a.deployTeam(t, slug, alpha, k1, "a1"), 409, "team_sites_not_ready")
	markReady(t, a.certDir, slug)

	// A personal key owns no sites.
	wantTS(t, "personal key", a.api(t, "PUT", "/v1/sites/"+alpha+"/files?create=1", deployBody("x"), p1.key), 403, "no_personal_sites")
	// Members deploy to their team's site.
	if r := a.deployTeam(t, slug, alpha, k1, "alpha one"); r.status != 201 {
		t.Fatalf("p1 deploy: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, slug, alpha, k2, "alpha two"); r.status != 200 {
		t.Fatalf("p2 deploy: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, slug, beta, k3, "beta one"); r.status != 201 {
		t.Fatalf("p3 deploy: %d %s", r.status, r.body)
	}
	alphaHost := alpha + "." + slug + "." + tsDomain
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "alpha two") {
		t.Fatalf("alpha site: %d %s", r.status, r.body)
	}
	// Person-path serving stays off: the event host never serves a team's files.
	if r := a.on(t, "GET", slug+"."+tsDomain, "/"+alpha+"/", nil, ""); r.status == 200 {
		t.Fatalf("event host served a team path: %s", r.body)
	}
	if r := a.on(t, "GET", slug+"."+tsDomain, "/v1/sites/"+alpha+"/data/x", nil, ""); r.status != 404 {
		t.Fatalf("event host /v1/: %d", r.status)
	}

	// Cross-team: a member never reaches another team's site.
	wantTS(t, "alpha member on beta", a.deployTeam(t, slug, beta, k1, "pwn"), 404, "owner_not_found")
	// An editor changes only the site's deploys, storage and visits.
	wantTS(t, "editor deletes site", a.api(t, "DELETE", "/v1/sites/"+alpha+"?owner="+slug, nil, k1), 403, "editor_scope")
	// Declaring an owner-only KV resource on its own site is allowed.
	if r := a.api(t, "PUT", "/v1/sites/"+alpha+"/storage/resources/signups?owner="+slug, map[string]any{"kind": "kv", "rules": json.RawMessage(privateRules)}, k1); r.status/100 != 2 {
		t.Fatalf("declare kind: %d %s", r.status, r.body)
	}

	// ---- deadline freeze ---------------------------------------------------
	ev := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, org.key)
	if ev.status != 200 {
		t.Fatalf("deadline: %d %s", ev.status, ev.body)
	}
	wantTS(t, "deploy after deadline", a.deployTeam(t, slug, alpha, k1, "late"), 409, "submissions_closed")
	wantTS(t, "rollback after deadline", a.api(t, "PUT", "/v1/sites/"+alpha+"/active-version?owner="+slug, map[string]int{"version_number": 1}, k1), 409, "submissions_closed")
	wantTS(t, "data write after deadline", a.api(t, "PUT", "/v1/sites/"+alpha+"/storage/resources/signups?owner="+slug, map[string]any{"kind": "kv", "rules": json.RawMessage(privateRules)}, k1), 409, "submissions_closed")
	// Preview links still work (read-only).
	if r := a.api(t, "POST", "/v1/sites/"+alpha+"/versions/2/preview-link?owner="+slug, nil, k1); r.status != 200 {
		t.Fatalf("preview after deadline: %d %s", r.status, r.body)
	}
	teams := a.api(t, "GET", "/v1/hack/events/"+slug+"/teams", nil, org.key).json(t)["teams"].([]any)
	var alphaObj map[string]any
	for _, x := range teams {
		if m := x.(map[string]any); m["slug"] == alpha {
			alphaObj = m
		}
	}
	if alphaObj["pinned_version"] != float64(2) || alphaObj["frozen"] != true {
		t.Fatalf("pin: %v", alphaObj)
	}
	pinned, _ := alphaObj["pinned_url"].(string)
	pu, err := url.Parse(pinned)
	if err != nil || pu.Host != alphaHost || !strings.Contains(pu.Path, "/__preview/2/") {
		t.Fatalf("pinned url %q", pinned)
	}
	if r := a.on(t, "GET", pu.Host, pu.Path, nil, ""); r.status != 200 || !strings.Contains(string(r.body), "alpha two") {
		t.Fatalf("pinned page: %d %s", r.status, r.body)
	} else if csp := strings.Join(r.header.Values("Content-Security-Policy"), ";"); !strings.Contains(csp, "frame-ancestors 'self' https://"+tsDomain) {
		// The judge's scoring screen on the app origin frames the submitted version.
		t.Fatalf("pinned page frame-ancestors: %q", csp)
	}
	// A future deadline while submissions are closed is refused.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "closed"}, org.key); r.status != 200 {
		t.Fatalf("closed: %d %s", r.status, r.body)
	}
	wantTS(t, "future deadline when closed", a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, org.key), 409, "submissions_closed_stage")

	// Per-team extension: alpha again, beta still frozen.
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+alpha+"/deadline", map[string]string{"deadline": later}, org.key); r.status != 200 || r.json(t)["pinned_version"] != nil {
		t.Fatalf("extend: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, slug, alpha, k1, "alpha three"); r.status != 200 {
		t.Fatalf("deploy with extension: %d %s", r.status, r.body)
	}
	wantTS(t, "beta still frozen", a.deployTeam(t, slug, beta, k3, "late"), 409, "submissions_closed")
	wantTS(t, "extension earlier than deadline", a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+beta+"/deadline", map[string]string{"deadline": "2020-01-01T00:00:00Z"}, org.key), 400, "deadline_not_later")

	// Judging and results freeze even a team's future extension and pin its site.
	for _, stage := range []string{"judging", "results"} {
		wantTS(t, "stage "+stage, a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": stage}, org.key), 200, "")
		wantTS(t, "extended deploy frozen "+stage, a.deployTeam(t, slug, alpha, k1, "too late"), 409, "submissions_closed")
		wantTS(t, "extended entry frozen "+stage, a.api(t, "PUT", "/v1/hack/events/"+slug+"/entry", map[string]string{"title": "Too late"}, p1.key), 409, "submissions_closed")
		var pinned bool
		if err := a.database.QueryRow(`SELECT pinned_at IS NOT NULL FROM event_teams WHERE slug=$1 AND event_id=(SELECT id FROM events WHERE slug=$2)`, alpha, slug).Scan(&pinned); err != nil || !pinned {
			t.Fatalf("extended site not pinned: %v %v", pinned, err)
		}
	}
	wantTS(t, "reopen building", a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, org.key), 200, "")
	wantTS(t, "extended deploy reopened", a.deployTeam(t, slug, alpha, k1, "building again"), 200, "")

	// ---- organiser moderation ---------------------------------------------
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+alpha+"/takedown", map[string]string{"reason": "spam"}, org.key); r.status != 200 {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status == 200 && strings.Contains(string(r.body), "alpha") {
		t.Fatalf("taken-down site still served: %d", r.status)
	}
	wantTS(t, "deploy taken down", a.deployTeam(t, slug, alpha, k1, "x"), 403, "team_site_taken_down")
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+alpha+"/restore", nil, org.key); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status != 200 {
		t.Fatalf("restored site: %d", r.status)
	}
	// A platform take-down is not the organiser's to undo.
	var betaSite string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN events e ON e.account_id = s.user_id WHERE e.slug = $1 AND s.name = $2`, slug, beta).Scan(&betaSite); err != nil {
		t.Fatal(err)
	}
	if err := siterepo.SetSiteSuspended(context.Background(), a.database, betaSite, "platform says no"); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "restore platform takedown", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+beta+"/restore", nil, org.key), 409, "platform_takedown")

	// Another event's organiser cannot touch this event's teams.
	org2 := a.newPerson(t, "org2")
	_ = a.makeEvent(t, org2)
	wantTS(t, "stranger takedown", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+alpha+"/takedown", nil, org2.key), 404, "event_not_found")
	wantTS(t, "stranger extension", a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+alpha+"/deadline", map[string]string{"deadline": ""}, org2.key), 404, "event_not_found")

	// ---- access follows the person ---------------------------------------
	// Removing a person ends their access at once.
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.uid(t, p2), nil, org.key); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	wantTS(t, "removed person", a.deployTeam(t, slug, alpha, k2, "x"), 404, "owner_not_found")
	// Moving a person to another team: the old team's site stops.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+beta+"/members", map[string]string{"user_id": a.uid(t, p1)}, org.key); r.status != 200 {
		t.Fatalf("move: %d %s", r.status, r.body)
	}
	wantTS(t, "moved person's old team", a.deployTeam(t, slug, alpha, k1, "x"), 404, "owner_not_found")

	// ---- a removed team's site goes, and its name is never reused ----------
	// Alpha emptied when its last member moved: it went, and its site too.
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status == 200 {
		t.Fatalf("emptied team's site still served")
	}
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+beta, nil, org.key); r.status != 204 {
		t.Fatalf("delete team: %d %s", r.status, r.body)
	}
	var live int
	_ = a.database.QueryRow(`SELECT count(*) FROM sites s JOIN events e ON e.account_id = s.user_id WHERE e.slug = $1 AND s.deleted_at IS NULL`, slug).Scan(&live)
	if live != 0 {
		t.Fatalf("removed teams' sites still live: %d", live)
	}
	// Back to building so people join and teams form again.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, org.key); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	if ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)["event"].(map[string]any); ev["submission_deadline"] != nil {
		t.Fatalf("back to building kept the passed deadline: %v", ev["submission_deadline"])
	}
	p4 := a.newPerson(t, "p4")
	a.join(t, slug, p4, org)
	again, _ := a.startTeam(t, slug, "Alpha", p4)
	if again == alpha {
		t.Fatalf("new team inherited the removed team's site name %q", again)
	}
}

// The person's own connection publishes their team's site by naming the
// event as owner, never another team's, and stops when they leave the team.
func TestHackM2ConnectorPublishesAsOwner(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1, p2 := a.newPerson(t, "org"), a.newPerson(t, "p1"), a.newPerson(t, "p2")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	a.join(t, slug, p2, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	beta, _ := a.startTeam(t, slug, "Beta", p2)
	markReady(t, a.certDir, slug)

	client := a.registerClient(t, testRedirect)
	access := a.connect(t, p1, client, testRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (string, bool) {
		rr := a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args})
		if rr.status != 200 {
			t.Fatalf("mcp %s: %d %s", name, rr.status, rr.body)
		}
		text, _, isErr := toolResultOf(t, rr)
		return text, isErr
	}
	if text, isErr := call("create_site", map[string]any{"site": alpha, "owner": slug, "files": map[string]string{"index.html": "<h1>via connector</h1>"}}); isErr {
		t.Fatalf("connector deploy: %s", text)
	}
	if text, isErr := call("create_site", map[string]any{"site": beta, "owner": slug, "files": map[string]string{"index.html": "x"}}); !isErr || !strings.Contains(text, "owner_not_found") {
		t.Fatalf("connector other team: %v %s", isErr, text)
	}
	if text, isErr := call("delete_site", map[string]any{"site": alpha, "owner": slug, "confirm_name": alpha}); !isErr || !strings.Contains(text, "editor_scope") {
		t.Fatalf("connector delete: %v %s", isErr, text)
	}
	// Off the team, the connection stops publishing.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, p1.key); r.status != 200 {
		t.Fatalf("leave: %d %s", r.status, r.body)
	}
	if text, isErr := call("update_site", map[string]any{"site": alpha, "owner": slug, "files": map[string]string{"index.html": "x"}}); !isErr {
		t.Fatalf("connector after leaving: %s", text)
	}
}

// A deploy let in before the deadline is pinned; nothing after it lands.
func TestHackM2DeadlineRace(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	markReady(t, a.certDir, slug)
	k1 := p1.key
	if r := a.deployTeam(t, slug, alpha, k1, "v1"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	// The deadline a moment away; deploy in a loop across it.
	dl := time.Now().Add(1500 * time.Millisecond)
	if r := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": dl.UTC().Format(time.RFC3339Nano)}, org.key); r.status != 200 {
		t.Fatalf("deadline: %d %s", r.status, r.body)
	}
	lastOK := 1
	for i := 2; time.Now().Before(dl.Add(700 * time.Millisecond)); i++ {
		time.Sleep(120 * time.Millisecond)
		r := a.deployTeam(t, slug, alpha, k1, "v")
		if r.status == 200 {
			lastOK = int(r.json(t)["active_version"].(float64))
		} else if r.status != 409 {
			t.Fatalf("deploy %d: %d %s", i, r.status, r.body)
		}
	}
	if err := teamsapp.New(a.database).PinDue(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	var pinned sql.NullInt64
	if err := a.database.QueryRow(`SELECT t.pinned_version FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1`, slug).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if !pinned.Valid || int(pinned.Int64) != lastOK {
		t.Fatalf("pinned %v, last accepted deploy %d", pinned, lastOK)
	}
}

// The platform's take-down of an event takes its team sites down with it.
func TestHackM2EventTakedownTakesTeamSitesDown(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	markReady(t, a.certDir, slug)
	k1 := p1.key
	if r := a.deployTeam(t, slug, alpha, k1, "up"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	host := alpha + "." + slug + "." + tsDomain
	if r := a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "abuse"}, a.admin); r.status != 200 {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status == 200 && strings.Contains(string(r.body), "up") {
		t.Fatalf("team site served while the event is taken down")
	}
	wantTS(t, "deploy while taken down", a.deployTeam(t, slug, alpha, k1, "x"), 403, "")
	// Reads stop too: the event's account is suspended.
	wantTS(t, "data while taken down", a.api(t, "GET", "/v1/sites/"+alpha+"/storage/resources?owner="+slug, nil, k1), 403, "account_suspended")
	if r := a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/restore", nil, a.admin); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 {
		t.Fatalf("team site after restore: %d", r.status)
	}
}

// Review round 1: names are never reused, a solo team cannot throw its
// submission away, extensions only extend, bare names stay in their event.
func TestHackM2ReviewRound1(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1, p2 := a.newPerson(t, "org"), a.newPerson(t, "p1"), a.newPerson(t, "p2")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	a.join(t, slug, p2, org)
	solo, _ := a.startTeam(t, slug, "Solo", p1)
	markReady(t, a.certDir, slug)
	k1 := p1.key
	if r := a.deployTeam(t, slug, solo, k1, "mine"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	// Anonymous bare names on the apex resolve nothing on this platform.
	if r := a.api(t, "GET", "/v1/sites/"+solo+"/data", nil, ""); r.status/100 == 2 {
		t.Fatalf("bare anonymous read: %d %s", r.status, r.body)
	}
	if r := a.api(t, "GET", "/v1/sites/"+solo+"/storage/resources?owner="+slug, nil, k1); r.status != 200 {
		t.Fatalf("member's read of the team site: %d %s", r.status, r.body)
	}
	// Extensions only extend: a later event deadline wins over an older one.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+solo+"/deadline", map[string]string{"deadline": past}, org.key); r.status != 409 {
		t.Fatalf("extension without an event deadline: %d %s", r.status, r.body)
	}
	soon := time.Now().Add(time.Hour).UTC()
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": soon.Format(time.RFC3339)}, org.key)
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+solo+"/deadline", map[string]string{"deadline": soon.Add(time.Hour).Format(time.RFC3339)}, org.key); r.status != 200 {
		t.Fatalf("extension: %d %s", r.status, r.body)
	}
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": soon.Add(24 * time.Hour).Format(time.RFC3339)}, org.key)
	if r := a.deployTeam(t, slug, solo, k1, "still open"); r.status != 200 {
		t.Fatalf("older extension froze the team early: %d %s", r.status, r.body)
	}
	// After the deadline a solo team's only member cannot leave (and take
	// the pinned submission with the team).
	a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+solo+"/deadline", map[string]string{"deadline": ""}, org.key)
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, org.key)
	wantTS(t, "leave after deadline", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, p1.key), 409, "submissions_closed")
	// Pinned: even emptied by the organiser, the team (and its site) stays.
	a.api(t, "GET", "/v1/hack/events/"+slug+"/teams", nil, org.key)
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+solo+"/members/"+a.uid(t, p1), nil, org.key); r.status != 204 {
		t.Fatalf("take off: %d %s", r.status, r.body)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1 AND t.slug = $2`, slug, solo).Scan(&n)
	if n != 1 {
		t.Fatalf("pinned team removed when emptied")
	}
	// Explicit removal still works, and the name is never reused, even
	// after the site is purged.
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+solo, nil, org.key); r.status != 204 {
		t.Fatalf("delete team: %d", r.status)
	}
	if _, err := a.database.Exec(`DELETE FROM sites s USING events e WHERE e.account_id = s.user_id AND e.slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, org.key)
	// A new team cannot start after the deadline; clear it first.
	wantTS(t, "team after deadline", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Late"}, p2.key), 409, "submissions_closed")
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": ""}, org.key)
	if again, _ := a.startTeam(t, slug, "Solo", p2); again == solo {
		t.Fatalf("team name reused after purge: %s", again)
	}
	// The event name is kept for good once it had a team.
	var accountID string
	_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	if r := a.api(t, "DELETE", "/v1/admin/hack/events/"+slug, nil, a.admin); r.status != 204 {
		t.Fatalf("admin delete: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug = $1`, slug) })
	r := a.api(t, "GET", "/v1/hack/names/"+slug, nil, org.key)
	if r.json(t)["available"] != false {
		t.Fatalf("deleted event's name available again: %s", r.body)
	}
}

// Review round 2 (correctness and security).
func TestHackM2ReviewRound2(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	// A draft event asks for no certificate, whatever is read.
	draft := uniqueSlug()
	if r := a.api(t, "POST", "/v1/hack/events", map[string]any{"slug": draft, "title": "D", "organiser_name": "O", "contact_email": "o@example.com", "purpose": "p", "expected_participants": 1, "starts_at": "2026-10-01"}, org.key); r.status != 201 {
		t.Fatalf("draft: %d %s", r.status, r.body)
	}
	t.Cleanup(func() {
		var acc string
		_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, draft).Scan(&acc)
		_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, draft)
		_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, acc)
	})
	a.api(t, "GET", "/v1/hack/events/"+draft, nil, org.key)
	if _, err := os.Stat(filepath.Join(a.certDir, "requests", draft)); err == nil {
		t.Fatalf("a draft event asked for a certificate")
	}

	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	markReady(t, a.certDir, slug)
	k1 := p1.key
	if r := a.deployTeam(t, slug, alpha, k1, "v1"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, org.key)
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)}, org.key)
	// A time-zone change never reopens a passed deadline.
	if r := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"time_zone": "America/Los_Angeles"}, org.key); r.status != 200 {
		t.Fatalf("zone: %d %s", r.status, r.body)
	}
	wantTS(t, "deploy after zone change", a.deployTeam(t, slug, alpha, k1, "late"), 409, "submissions_closed")
	// Emptied after its deadline but before any pin: the team stays.
	if _, err := a.database.Exec(`UPDATE event_teams t SET pinned_at = NULL, pinned_version = NULL FROM events e WHERE e.id = t.event_id AND e.slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+alpha+"/members/"+a.uid(t, p1), nil, org.key); r.status != 204 {
		t.Fatalf("take off: %d %s", r.status, r.body)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1`, slug).Scan(&n)
	if n != 1 {
		t.Fatalf("team past its deadline removed when emptied")
	}
	// Closed → draft → building reopens submissions.
	for _, st := range []string{"closed", "draft", "building"} {
		if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": st}, org.key); r.status != 200 {
			t.Fatalf("stage %s: %d %s", st, r.status, r.body)
		}
	}
	if ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)["event"].(map[string]any); ev["submission_deadline"] != nil {
		t.Fatalf("draft then building kept the passed deadline")
	}
	// The emptied team, reopened and unpinned, goes with the sweep.
	hack.NewSiteSweep(a.database, a.sites, nil).Sweep(context.Background())
	_ = a.database.QueryRow(`SELECT count(*) FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1`, slug).Scan(&n)
	if n != 0 {
		t.Fatalf("empty reopened team kept: %d", n)
	}
	// Images: huge declared dimensions refused; gallery screenshots revalidate.
	big := pngOfSize(9000, 10)
	a.startTeam(t, slug, "Beta", p1)
	r := a.on(t, "PUT", "", "/v1/hack/events/"+slug+"/entry/screenshot", string(big), p1.key)
	wantTS(t, "huge image", r, 415, "unsupported_image")
	hugeWebP := []byte("RIFF\x16\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00\xff\x3f\x00\xff\x3f\x00")
	wantTS(t, "huge webp", a.on(t, "PUT", "", "/v1/hack/events/"+slug+"/entry/screenshot", string(hugeWebP), p1.key), 415, "unsupported_image")

}

// pngOfSize is a PNG header declaring w x h (enough for DecodeConfig).
func pngOfSize(w, h int) []byte {
	ihdr := []byte{'I', 'H', 'D', 'R', byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w), byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h), 8, 2, 0, 0, 0}
	c := crc32.ChecksumIEEE(ihdr)
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	b = append(b, ihdr...)
	return append(b, byte(c>>24), byte(c>>16), byte(c>>8), byte(c))
}

// Review round 3.
func TestHackM2ReviewRound3(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	markReady(t, a.certDir, slug)
	k1 := p1.key
	a.deployTeam(t, slug, alpha, k1, "v1")
	// A wall-clock time the clocks skip is refused.
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"time_zone": "Europe/London"}, org.key)
	wantTS(t, "dst gap", a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": "2027-03-28T01:30"}, org.key), 400, "invalid_submission_deadline")
	// my-teams says how many of my teams cannot publish.
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, org.key)
	if m := a.api(t, "GET", "/v1/hack/my-teams", nil, p1.key).json(t); m["blocked"] != float64(1) || len(m["teams"].([]any)) != 0 {
		t.Fatalf("my-teams: %v", m)
	}
	// Ending an event before its deadline ends submissions too.
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": ""}, org.key)
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}, org.key)
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, org.key); r.status != 200 {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)["event"].(map[string]any)
	if d, _ := time.Parse(time.RFC3339, ev["submission_deadline"].(string)); d.After(time.Now()) {
		t.Fatalf("ended event kept a future deadline: %v", ev["submission_deadline"])
	}
}
