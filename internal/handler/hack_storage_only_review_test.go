package handler

import (
	"net/http"
	"strings"
	"testing"
)

// Checks the public router and both connector grant shapes, not only the
// legacy route-name classifier. Hack websites retain resource storage.
func TestHackStorageOnlyReview(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "storage-only-org")
	member := a.newPerson(t, "storage-only-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Only", member)
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, member)
	wantTS(t, "team publish", a.deployTeam(t, team, key, "storage only"), 201, "")
	base := "/v1/sites/" + team
	for _, tc := range []struct {
		method, path string
		body         any
		key          string
	}{
		{"GET", base + "/state", nil, key},
		{"PUT", base + "/state", map[string]any{"old": true}, key},
		{"GET", base + "/state/history", nil, key},
		{"GET", base + "/collections", nil, key},
		{"POST", base + "/collections/old", map[string]any{"old": true}, key},
		{"GET", base + "/collections/old/deleted", nil, key},
		{"GET", base + "/data", nil, key},
		{"GET", base + "/data/old/kind", nil, key},
		{"PUT", base + "/data/old/kind", map[string]any{"kind": "entries"}, key},
		{"GET", base + "/savers", nil, key},
		{"DELETE", base + "/history", map[string]string{"confirm": team}, key},
		{"PUT", base + "/allowed-origins", map[string]any{"origins": []string{}}, key},
		{"GET", "/v1/u/" + slug + "/sites/" + team + "/state", nil, key},
		{"GET", "/v1/u/" + slug + "/sites/" + team + "/data/old", nil, key},
		{"GET", "/v1/admin/data-watch", nil, a.admin},
		{"GET", "/v1/data-notify/stop", nil, ""},
	} {
		wantTS(t, tc.method+" "+tc.path, a.api(t, tc.method, tc.path, tc.body, tc.key), http.StatusGone, "legacy_storage_removed")
	}
	teamHost := team + "." + slug + "." + tsDomain
	wantTS(t, "team-host legacy read", a.on(t, "GET", teamHost, base+"/state", nil, ""), http.StatusGone, "legacy_storage_removed")
	wantTS(t, "team-host legacy preflight", a.on(t, "OPTIONS", teamHost, base+"/data/old", nil, ""), http.StatusGone, "legacy_storage_removed")
	wantTS(t, "team resource", a.api(t, "PUT", base+"/storage/resources/prefs", map[string]string{"kind": "kv", "read": "anyone"}, key), 201, "")
	wantTS(t, "team KV write", a.api(t, "PUT", base+"/storage/kv/prefs/keys/color", map[string]any{"value": "blue"}, key), 200, "")
	if r := a.on(t, "GET", teamHost, base+"/storage/kv/prefs/keys/color", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "blue") {
		t.Fatalf("team visitor KV: %d %s", r.status, r.body)
	}
	wantTS(t, "visitor sign-in status", hackBrowserRequest(t, a, "GET", teamHost, base+"/me", nil), 200, "")

	eventBase := "/v1/hack/events/" + slug + "/website"
	wantTS(t, "event-site publish", a.api(t, "PUT", eventBase+"/files?create=1", deployBody("event"), org.key), 201, "")
	eventHost := slug + "." + tsDomain
	wantTS(t, "event-host legacy read", a.on(t, "GET", eventHost, "/v1/sites/"+slug+"/state", nil, ""), 404, "not_found")
	wantTS(t, "event-host legacy alias", a.on(t, "GET", eventHost, "/v1/u/"+slug+"/sites/"+slug+"/data/old", nil, ""), 404, "not_found")
	wantTS(t, "native event remains", a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key), 200, "")

	// An older team key and a selected personal grant must hide retired tools.
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, member, client, testRedirect)["access_token"].(string)
	var teamID string
	if err := a.database.QueryRow(`SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2`, slug, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if text, _, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_select_team", "arguments": map[string]any{"team_id": teamID}})); failed {
		t.Fatalf("select team: %s", text)
	}
	for label, listing := range map[string]resp{
		"team key":       a.mcpWithKey(t, key),
		"personal grant": a.rpc(t, access, "tools/list", map[string]any{}),
	} {
		names := mcpToolNames(t, listing)
		for _, name := range []string{"get_state", "list_collections", "declare_data", "list_data", "data_history", "update_data", "set_site_passcode", "hack_create_team_key"} {
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
	for _, name := range []string{"get_state", "list_data", "set_site_passcode", "hack_create_team_key"} {
		r := a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"site": team}})
		e, _ := r.json(t)["error"].(map[string]any)
		if e == nil || !strings.Contains(e["message"].(string), "unknown tool") {
			t.Fatalf("direct %s was not rejected: %d %s", name, r.status, r.body)
		}
	}
}

func TestHostRetainsLegacyStorageReview(t *testing.T) {
	s := newKindsSite(t, false)
	wantCode(t, "Host state", s.owner(t, "GET", "/v1/sites/shop/state", nil), 200, "")
	wantCode(t, "Host declared data", s.owner(t, "GET", "/v1/sites/shop/data", nil), 200, "")
}
