package servertest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Co-organiser invitations are stored hashed, expire, lose authority on
// rotation, removal and revocation, and cannot promote an existing participant.
func TestRhpCoOrganiserInvites(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "rhpadminorg")
	co := a.newPerson(t, "rhpcoorg")
	other := a.newPerson(t, "rhpotherorg")
	part := a.newPerson(t, "rhppart")
	third := a.newPerson(t, "rhpthirdorg")
	slug := uniqueSlug()
	foreign := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	wantTS(t, "other event", a.createEvent(t, other, foreign, nil), 201, "")
	base := "/v1/hack/events/" + slug
	invite := func(p person) string {
		t.Helper()
		r := a.at(t, "POST", base+"/organiser-invite", nil, a.key(p))
		wantTS(t, "invite", r, 201, "")
		u, _ := url.Parse(r.json(t)["url"].(string))
		return "/v1/hack" + u.Path
	}
	body := map[string]any{"accept_coc": true, "display_name": "Collaborator"}

	first := invite(org)
	second := invite(org)
	wantTS(t, "rotated link preview", a.at(t, "GET", first, nil, nil), 404, "invalid_code")
	wantTS(t, "rotated link accept", a.at(t, "POST", first, body, a.key(co)), 404, "invalid_code")
	var stored string
	if err := a.database.QueryRow(`SELECT token_hash FROM event_organiser_invites WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, slug).Scan(&stored); err != nil || stored == "" || strings.Contains(second, stored) {
		t.Fatalf("invitation must store a hash, not the token: %v", err)
	}

	a.openEvent(t, org, slug)
	code := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	wantTS(t, "participant joins", a.at(t, "POST", "/v1/hack/join/"+code, body, a.key(part)), 200, "")
	wantTS(t, "no silent promotion", a.at(t, "POST", second, body, a.key(part)), 409, "already_member")
	wantTS(t, "coc required", a.at(t, "POST", second, map[string]any{"display_name": "C"}, a.key(co)), 400, "coc_required")
	joined := a.at(t, "POST", second, body, a.key(co))
	wantTS(t, "accept", joined, 200, "")
	if joined.json(t)["role"] != "organiser" {
		t.Fatalf("role: %v", joined.json(t)["role"])
	}
	wantTS(t, "second organiser accepts", a.at(t, "POST", second, body, a.key(other)), 200, "")
	wantTS(t, "already organiser", a.at(t, "POST", second, body, a.key(co)), 409, "already_member")
	ev, err := hackevents.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []person{co, other} {
		if m, err := hackevents.GetEventMember(context.Background(), a.database, ev.ID, rhpUserID(t, a, p)); err != nil || m.Role != "organiser" {
			t.Fatalf("accepted member: %v %v", m, err)
		}
	}

	wantTS(t, "revoke", a.at(t, "DELETE", base+"/organiser-invite", nil, a.key(org)), 204, "")
	wantTS(t, "revoked link", a.at(t, "POST", second, body, a.key(third)), 404, "invalid_code")
	wantTS(t, "co-organiser manages event", a.at(t, "PATCH", base, map[string]any{"tagline": "edit"}, a.key(co)), 200, "")
	wantTS(t, "foreign event", a.at(t, "GET", "/v1/hack/events/"+foreign+"/people", nil, a.key(co)), 404, "event_not_found")
	wantTS(t, "participant cannot invite", a.at(t, "POST", base+"/organiser-invite", nil, a.key(part)), 404, "event_not_found")
	wantTS(t, "primary protected", a.at(t, "DELETE", base+"/organisers/"+rhpUserID(t, a, org), nil, a.key(co)), 409, "primary_organiser")

	issued := invite(org)
	wantTS(t, "remove co-organiser", a.at(t, "DELETE", base+"/organisers/"+rhpUserID(t, a, co), nil, a.key(org)), 204, "")
	wantTS(t, "removed has no access", a.at(t, "GET", base+"/people", nil, a.key(co)), 404, "event_not_found")
	wantTS(t, "removal revokes link", a.at(t, "POST", issued, body, a.key(third)), 404, "invalid_code")

	expired := invite(org)
	if _, err := a.database.Exec(`UPDATE event_organiser_invites SET expires_at=now()-interval '1 second' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "expired link", a.at(t, "POST", expired, body, a.key(co)), 404, "invalid_code")
}

// A team writes privately to the organisers: only team members write, every
// organiser reads the thread, other roles cannot, delivery follows each
// organiser's email opt-in, and writing stops once the event has ended.
func TestRhpTeamToOrganisers(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "rhpt2oorg")
	quiet := a.newPerson(t, "rhpt2oquiet")
	lead := a.newPerson(t, "rhpt2olead")
	other := a.newPerson(t, "rhpt2oother")
	lone := a.newPerson(t, "rhpt2olone")
	judge := a.newPerson(t, "rhpt2ojudge")
	outsider := a.newPerson(t, "rhpt2ooutsider")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug

	if _, err := a.database.Exec(`INSERT INTO event_members (event_id, user_id, role, coc_accepted_at) SELECT id, $2, 'organiser', now() FROM events WHERE slug = $1`, slug, a.uid(t, quiet)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "opt out", a.api(t, "PATCH", "/v1/hack/preferences", map[string]any{"email_updates": false}, quiet.key), 200, "")
	for _, p := range []person{lead, other, lone} {
		a.join(t, slug, p, org)
	}
	code := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)["judge_code"].(string)
	wantTS(t, "judge joins", a.api(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true, "display_name": "J"}, judge.key), 200, "")
	alpha, _ := a.startTeam(t, slug, "Alpha", lead)
	a.startTeam(t, slug, "Beta", other)

	post := func(p person, text string) resp {
		return a.api(t, "POST", base+"/team/messages", map[string]any{"text": text}, p.key)
	}
	for _, tc := range []struct {
		name   string
		p      person
		text   string
		status int
		code   string
	}{
		{"no team", lone, "hi", http.StatusConflict, "no_team"},
		{"organiser", org, "hi", http.StatusNotFound, "event_not_found"},
		{"judge", judge, "hi", http.StatusNotFound, "event_not_found"},
		{"outsider", outsider, "hi", http.StatusNotFound, "event_not_found"},
		{"empty", lead, "  ", http.StatusBadRequest, "invalid_text"},
		{"too long", lead, strings.Repeat("x", 1001), http.StatusBadRequest, "invalid_text"},
	} {
		wantTS(t, tc.name, post(tc.p, tc.text), tc.status, tc.code)
	}
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_messages WHERE from_team AND event_id = (SELECT id FROM events WHERE slug = $1)`, slug).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows after refusals: %d %v", rows, err)
	}
	wantTS(t, "team writes", post(lead, "We need a bigger table."), http.StatusCreated, "")

	// Every organiser reads the thread; other roles and teams do not.
	for _, p := range []person{org, quiet} {
		got := a.api(t, "GET", base+"/teams/"+alpha+"/messages", nil, p.key).json(t)["messages"].([]any)
		if len(got) != 1 || got[0].(map[string]any)["from_team"] != true {
			t.Fatalf("organiser thread: %v", got)
		}
	}
	for _, p := range []person{lead, other, lone, judge, outsider} {
		wantTS(t, "organiser view refused", a.api(t, "GET", base+"/teams/"+alpha+"/messages", nil, p.key), http.StatusNotFound, "event_not_found")
	}
	if got := a.api(t, "GET", base+"/team/messages", nil, other.key).json(t)["messages"].([]any); len(got) != 0 {
		t.Fatalf("another team reads Alpha's thread: %v", got)
	}

	// Email is queued only for the organiser who opted in, never for others.
	state := map[string]string{}
	rowsQ, err := a.database.Query(`SELECT u.username, n.email_state FROM event_notices n JOIN users u ON u.id = n.user_id WHERE n.kind = 'team_inbound' AND n.event_id = (SELECT id FROM events WHERE slug = $1)`, slug)
	if err != nil {
		t.Fatal(err)
	}
	for rowsQ.Next() {
		var who, st string
		if err := rowsQ.Scan(&who, &st); err != nil {
			t.Fatal(err)
		}
		state[who] = st
	}
	rowsQ.Close()
	if state[org.email] != "pending" || state[quiet.email] != "none" || len(state) != 2 {
		t.Fatalf("team_inbound notices: %v", state)
	}

	wantTS(t, "archive", a.api(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	wantTS(t, "after the end", post(other, "late"), http.StatusConflict, "event_closed")
}

// A team editor cannot call the retired saved-data and passcode routes or
// tools, while current resource storage keeps working on a team's connection.
func TestRhpStorageOnlyRefusals(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "rhpstoronlyorg")
	member := a.newPerson(t, "rhpstoronlymember")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Only", member)
	markReady(t, a.certDir, slug)
	key := member.key
	wantTS(t, "team publish", a.deployTeam(t, slug, team, key, "storage only"), 201, "")
	base := "/v1/sites/" + team
	own := "?owner=" + slug
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", base + "/state" + own, nil},
		{"PUT", base + "/state" + own, map[string]any{"old": true}},
		{"GET", base + "/collections" + own, nil},
		{"POST", base + "/collections/old" + own, map[string]any{"old": true}},
		{"GET", base + "/data" + own, nil},
		{"PUT", base + "/data/old/kind" + own, map[string]any{"kind": "entries"}},
		{"GET", base + "/savers" + own, nil},
		{"DELETE", base + "/history" + own, map[string]string{"confirm": team}},
		{"GET", "/v1/u/" + slug + "/sites/" + team + "/state", nil},
	} {
		if r := a.api(t, tc.method, tc.path, tc.body, key); r.status != http.StatusNotFound && r.status != http.StatusForbidden {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	wantTS(t, "team resource", a.api(t, "PUT", base+"/storage/resources/prefs"+own, map[string]any{"kind": "kv", "rules": json.RawMessage(publicRules)}, key), 201, "")
	wantTS(t, "team KV write", a.api(t, "PUT", base+"/storage/kv/prefs/keys/color"+own, map[string]any{"value": "blue"}, key), 200, "")

	client := a.registerClient(t, testRedirect)
	access := a.connect(t, member, client, testRedirect)["access_token"].(string)
	retired := []string{"get_state", "list_collections", "declare_data", "list_data", "data_history", "update_data", "get_site_passcode", "set_site_passcode", "remove_site_passcode", "sign_out_site_visitors"}
	for label, listing := range map[string]resp{"own key": a.mcpWithKey(t, key), "personal grant": a.rpc(t, access, "tools/list", map[string]any{})} {
		names := mcpToolNames(t, listing)
		for _, name := range retired {
			if hasTool(names, name) {
				t.Fatalf("%s exposes retired tool %s", label, name)
			}
		}
		for _, name := range []string{"storage_get_usage", "storage_set_resource"} {
			if !hasTool(names, name) {
				t.Fatalf("%s lost storage tool %s", label, name)
			}
		}
	}
	for _, name := range []string{"get_state", "list_data", "get_site_passcode", "set_site_passcode"} {
		r := a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"site": team}})
		if e, _ := r.json(t)["error"].(map[string]any); e == nil || !strings.Contains(e["message"].(string), "unknown tool") {
			t.Fatalf("direct %s was not rejected: %d %s", name, r.status, r.body)
		}
	}
}

func rhpUserID(t *testing.T, a *hackApp, p person) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
