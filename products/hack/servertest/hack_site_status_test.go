package servertest

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/teams"
)

func selectTeamByID(t *testing.T, a *teamSiteApp, access, teamID string) {
	t.Helper()
	text, _, isErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_select_team", "arguments": map[string]any{"team_id": teamID}}))
	if isErr {
		t.Fatalf("select team: %s", text)
	}
}

func teamIDOf(t *testing.T, a *teamSiteApp, eventSlug, teamSlug string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, eventSlug, teamSlug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestHackSiteStatusConnection covers GET .../site's connection block: none,
// connected, a teammate connected (by name, never their app), a left member
// not counted, another team selected, and a recently-used team key.
func TestHackSiteStatusConnection(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "org")
	p1, p2, p3 := a.newPerson(t, "p1"), a.newPerson(t, "p2"), a.newPerson(t, "p3")
	ev := a.makeEvent(t, org)
	a.join(t, ev, p1, org)
	a.join(t, ev, p2, org)
	a.join(t, ev, p3, org)
	teamSlug, code := a.startTeam(t, ev, "Alpha", p1)
	wantTS(t, "p2 joins team", a.api(t, "POST", "/v1/hack/events/"+ev+"/teams/join", map[string]any{"code": code}, p2.key), 200, "")
	wantTS(t, "p3 joins team", a.api(t, "POST", "/v1/hack/events/"+ev+"/teams/join", map[string]any{"code": code}, p3.key), 200, "")
	teamID := teamIDOf(t, a, ev, teamSlug)

	// Nothing connected yet: none.
	none := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key)
	if none.status != http.StatusOK {
		t.Fatalf("site status: %d %s", none.status, none.body)
	}
	conn := none.json(t)["connection"].(map[string]any)
	if conn["state"] != "none" || conn["app"] != nil || len(conn["teammates_connected"].([]any)) != 0 {
		t.Fatalf("none state: %v", conn)
	}

	// p1 connects a personal connector and selects this team: connected.
	clientID := a.registerClient(t, testRedirect)
	tok := a.connect(t, p1, clientID, testRedirect)
	access := tok["access_token"].(string)
	selectTeamByID(t, a, access, teamID)
	connected := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key).json(t)["connection"].(map[string]any)
	if connected["state"] != "connected" || connected["app"] != "Test Chat" || connected["last_used_at"] == nil {
		t.Fatalf("connected state: %v", connected)
	}
	if connected["other_team"] != nil {
		t.Fatalf("connected should have no other_team: %v", connected)
	}

	// p2 connects and selects the SAME team: p1 should see p2 as a connected
	// teammate, by name only -- never the app or client name.
	p2Access := a.connect(t, p2, clientID, testRedirect)["access_token"].(string)
	selectTeamByID(t, a, p2Access, teamID)
	withMate := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key).json(t)["connection"].(map[string]any)
	mates := withMate["teammates_connected"].([]any)
	if len(mates) != 1 || mates[0] != "P" {
		t.Fatalf("teammates_connected: %v", withMate)
	}

	// p2 leaves the team: their stale grant must not count any more, even
	// though the grant's selected_team_id still points at this team.
	wantTS(t, "p2 leaves", a.api(t, "POST", "/v1/hack/events/"+ev+"/teams/leave", nil, p2.key), 200, "")
	afterLeave := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key).json(t)["connection"].(map[string]any)
	if len(afterLeave["teammates_connected"].([]any)) != 0 {
		t.Fatalf("left member still counted: %v", afterLeave)
	}

	// p1 selects a team in ANOTHER event: other_team.
	otherEv := a.makeEvent(t, org)
	a.join(t, otherEv, p1, org)
	otherSlug, _ := a.startTeam(t, otherEv, "Beta", p1)
	otherTeamID := teamIDOf(t, a, otherEv, otherSlug)
	selectTeamByID(t, a, access, otherTeamID)
	other := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p1.key).json(t)["connection"].(map[string]any)
	if other["state"] != "other_team" {
		t.Fatalf("other_team state: %v", other)
	}
	ot, ok := other["other_team"].(map[string]any)
	if !ok || ot["team"] != "Beta" {
		t.Fatalf("other_team detail: %v", other)
	}
	if strings.Contains(other["app"].(string), clientID) {
		t.Fatalf("connection leaked a client id: %v", other)
	}

	// p3: never connected a connector, only a team key, recently used: key.
	a.teamKey(t, ev, p3)
	if _, err := a.database.Exec(`
		UPDATE api_keys SET last_used_at = now()
		 WHERE id = (SELECT key_id FROM event_team_keys WHERE user_id = $1 AND event_id = (SELECT id FROM events WHERE slug = $2))`,
		a.uid(t, p3), ev); err != nil {
		t.Fatal(err)
	}
	keyState := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p3.key).json(t)["connection"].(map[string]any)
	if keyState["state"] != "key" || len(keyState["apps"].([]any)) != 0 {
		t.Fatalf("key state: %v", keyState)
	}

	// A key used over 30 days ago does not count.
	if _, err := a.database.Exec(`
		UPDATE api_keys SET last_used_at = now() - interval '31 days'
		 WHERE id = (SELECT key_id FROM event_team_keys WHERE user_id = $1 AND event_id = (SELECT id FROM events WHERE slug = $2))`,
		a.uid(t, p3), ev); err != nil {
		t.Fatal(err)
	}
	staleKey := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p3.key).json(t)["connection"].(map[string]any)
	if staleKey["state"] != "none" {
		t.Fatalf("stale key should not count: %v", staleKey)
	}

	// A person with no team at all gets 404 no_team, same as /key.
	outsider := a.newPerson(t, "outsider")
	a.join(t, ev, outsider, org)
	wantTS(t, "no team", a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, outsider.key), 409, "no_team")

	// MCP: hack_get_team_status carries site.connection, with no client or
	// grant ids anywhere in the result.
	statusText, statusOut, statusErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_get_team_status", "arguments": map[string]any{"slug": otherEv}}))
	if statusErr {
		t.Fatalf("hack_get_team_status: %s", statusText)
	}
	meTeam, ok := statusOut["me"].(map[string]any)["team"].(map[string]any)
	if !ok {
		t.Fatalf("hack_get_team_status missing team: %v", statusOut)
	}
	site, ok := meTeam["site"].(map[string]any)
	if !ok {
		t.Fatalf("hack_get_team_status missing site: %v", meTeam)
	}
	mcpConn, ok := site["connection"].(map[string]any)
	if !ok || mcpConn["state"] != "connected" {
		t.Fatalf("hack_get_team_status site.connection: %v", site)
	}
	if strings.Contains(statusText, clientID) {
		t.Fatalf("hack_get_team_status leaked a client id: %s", statusText)
	}
}

// TestHackConnectStepsJSON moved to hack_assets_test.go (cleanup 3.2 slice
// 2a): it exercises loadConnectSteps/connectAppByID/expandConnectSteps,
// which moved from hack_connect_steps.go (moving) to hack_assets.go
// (staying), and needs no teamSiteApp/hackApp fixture.

func TestEventsGrantsByUserEmpty(t *testing.T) {
	a := newTeamSiteApp(t)
	out, err := teams.EventsGrantsByUser(t.Context(), a.database, nil, []string{"https://example.test/mcp", "https://example.test"})
	if err != nil || len(out) != 0 {
		t.Fatalf("empty input: %v %v", out, err)
	}
}

func TestHackSiteStatusAllEventsGrants(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p, mate := a.newPerson(t, "org"), a.newPerson(t, "p2"), a.newPerson(t, "mate")
	ev := a.makeEvent(t, org)
	a.join(t, ev, p, org)
	a.join(t, ev, mate, org)
	slug, code := a.startTeam(t, ev, "P2", p)
	wantTS(t, "mate joins", a.api(t, "POST", "/v1/hack/events/"+ev+"/teams/join", map[string]any{"code": code}, mate.key), 200, "")
	teamID := teamIDOf(t, a, ev, slug)
	otherEv := a.makeEvent(t, org)
	a.join(t, otherEv, p, org)
	otherSlug, _ := a.startTeam(t, otherEv, "Other", p)
	otherID := teamIDOf(t, a, otherEv, otherSlug)
	uid, mateID := a.uid(t, p), a.uid(t, mate)
	bolt, claude, claude2 := a.registerClient(t, testRedirect), a.registerClient(t, testRedirect), a.registerClient(t, testRedirect)
	for id, name := range map[string]string{bolt: "Bolt", claude: "Claude", claude2: "Claude"} {
		if _, err := a.database.Exec(`UPDATE oauth_clients SET client_name=$2 WHERE client_id=$1`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2026, 11, 9, 19, 56, 0, 0, time.UTC)
	newer := old.Add(time.Minute)
	type grant struct {
		user, client, team, scope string
		resource                  string
		at                        time.Time
	}
	for _, tc := range []struct {
		name, state, app string
		apps             []any
		grants           []grant
		at               time.Time
		mates            int
	}{
		{name: "older this team beats latest no team", state: "connected", app: "Bolt", apps: []any{"Bolt"}, at: old,
			grants: []grant{{uid, bolt, teamID, "sites events", a.srv.URL + "/mcp", old}, {uid, claude, "", "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "bare origin this team beats latest no team", state: "connected", app: "Bolt", apps: []any{"Bolt"}, at: old,
			grants: []grant{{uid, bolt, teamID, "sites events", a.srv.URL, old}, {uid, claude, "", "sites events", a.srv.URL + "/mcp", newer}, {uid, claude2, teamID, "sites events", "https://other.example/mcp", newer}}},
		{name: "unrelated resource ignored", state: "none", apps: []any{},
			grants: []grant{{uid, bolt, teamID, "sites events", "https://other.example/mcp", newer}}},
		{name: "only no team", state: "no_team", app: "Claude", apps: []any{"Claude"}, at: newer,
			grants: []grant{{uid, claude, "", "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "no team beats other team", state: "no_team", app: "Bolt", apps: []any{"Bolt"}, at: old,
			grants: []grant{{uid, bolt, "", "sites events", a.srv.URL + "/mcp", old}, {uid, claude, otherID, "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "deleted team beats older real team", state: "no_team", app: "Claude", apps: []any{"Claude"}, at: newer,
			grants: []grant{{uid, bolt, otherID, "sites events", a.srv.URL + "/mcp", old}, {uid, claude, "00000000-0000-0000-0000-000000000414", "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "both without a team", state: "no_team", app: "Claude", apps: []any{"Claude", "Bolt"}, at: newer,
			grants: []grant{{uid, bolt, "", "sites events", a.srv.URL + "/mcp", old}, {uid, claude, "", "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "this team beats other team", state: "connected", app: "Bolt", apps: []any{"Bolt"}, at: old,
			grants: []grant{{uid, bolt, teamID, "sites events", a.srv.URL + "/mcp", old}, {uid, claude, otherID, "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "only other team", state: "other_team", app: "Claude", apps: []any{"Claude"}, at: newer,
			grants: []grant{{uid, claude, otherID, "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "both on this team", state: "connected", app: "Claude", apps: []any{"Claude", "Bolt"}, at: newer,
			grants: []grant{{uid, bolt, teamID, "sites events", a.srv.URL + "/mcp", old}, {uid, claude, teamID, "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "teammate older this team", state: "none", apps: []any{}, mates: 1,
			grants: []grant{{mateID, bolt, teamID, "sites events", a.srv.URL + "/mcp", old}, {mateID, claude, "", "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "legacy team grant ignored", state: "none", apps: []any{},
			grants: []grant{{uid, bolt, teamID, "sites team:" + teamID, a.srv.URL + "/mcp", newer}}},
		{name: "duplicate app names", state: "connected", app: "Bolt", apps: []any{"Bolt"}, at: newer,
			grants: []grant{{uid, bolt, teamID, "sites events", a.srv.URL + "/mcp", old}, {uid, bolt, teamID, "sites events", a.srv.URL + "/mcp", newer}}},
		{name: "different clients with same app name", state: "connected", app: "Claude", apps: []any{"Claude"}, at: newer,
			grants: []grant{{uid, claude, teamID, "sites events", a.srv.URL + "/mcp", old}, {uid, claude2, teamID, "sites events", a.srv.URL + "/mcp", newer}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.database.Exec(`DELETE FROM oauth_grants WHERE user_id IN ($1,$2)`, uid, mateID); err != nil {
				t.Fatal(err)
			}
			for _, g := range tc.grants {
				if _, err := a.database.Exec(`INSERT INTO oauth_grants (user_id,client_id,scope,resource,selected_team_id,last_used_at) VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid,$6)`, g.user, g.client, g.scope, g.resource, g.team, g.at); err != nil {
					t.Fatal(err)
				}
			}
			r := a.api(t, "GET", "/v1/hack/events/"+ev+"/site", nil, p.key)
			if r.status != 200 {
				t.Fatalf("site: %d %s", r.status, r.body)
			}
			c := r.json(t)["connection"].(map[string]any)
			if c["state"] != tc.state || !reflect.DeepEqual(c["apps"], tc.apps) || len(c["teammates_connected"].([]any)) != tc.mates {
				t.Fatalf("connection: %v", c)
			}
			if tc.app != "" && (c["app"] != tc.app || c["last_used_at"] != tc.at.Format(time.RFC3339)) {
				t.Fatalf("app/last use: %v", c)
			}
			if tc.state == "other_team" {
				other, ok := c["other_team"].(map[string]any)
				if !ok || other["team"] != "Other" || other["event"] == "" {
					t.Fatalf("other team: %v", c)
				}
			}
		})
	}
}
