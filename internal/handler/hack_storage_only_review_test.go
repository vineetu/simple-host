package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	// get_state and list_data are retired connector-wide, so a direct call
	// now answers with the retirement notice (a successful tool result,
	// isError true) rather than a protocol-level "unknown tool" error.
	for _, name := range []string{"get_state", "list_data"} {
		text, _, isErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"site": team}}))
		if !isErr || !strings.Contains(text, "no longer offered") {
			t.Fatalf("direct %s retirement notice: %v %s", name, isErr, text)
		}
	}
	// set_site_passcode and hack_create_team_key are merely not offered to
	// this caller (not retired), so a direct call is still an unknown tool.
	for _, name := range []string{"set_site_passcode", "hack_create_team_key"} {
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

// Exercises the registered public routes: the Hack spec must be the same
// complete JSON document at both extensions, with no dangling component refs.
func TestHackRegisteredDocsReview(t *testing.T) {
	oldMode, oldChrome := HackMode(), hackChrome
	SetHackMode(true)
	SetHackChrome(true)
	SetInstanceHosts("simple-hack.test", "sites.simple-hack.test", "")
	t.Cleanup(func() {
		SetHackMode(oldMode)
		SetHackChrome(oldChrome)
		SetInstanceHosts("simple-host.app", "", "")
	})
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-hack.test", &SiteHandler{})
	get := func(path string) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "https://simple-hack.test"+path, nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: HTTP %d", path, rec.Code)
		}
		return rec.Body.Bytes()
	}
	page := string(get("/docs.html"))
	for _, text := range []string{"<title>API Docs — Simple Hack</title>", "<h1>Simple Hack REST API</h1>", `href="/get-started"`} {
		if !strings.Contains(page, text) {
			t.Errorf("Hack docs missing %q", text)
		}
	}
	for _, old := range []string{"npx skills add", "/install.sh", "simple-host.app/skills.zip", "<h1>Simple Host REST API</h1>"} {
		if strings.Contains(page, old) {
			t.Errorf("Hack docs retained %q", old)
		}
	}
	llms := string(get("/llms.txt"))
	if !strings.Contains(llms, "SH.storage.kv()") || strings.Contains(llms, "SH.data(") {
		t.Fatal("Hack llms route does not describe current-only storage")
	}
	jsonSpec, yamlSpec := get("/openapi.json"), get("/openapi.yaml")
	if !bytes.Equal(jsonSpec, yamlSpec) || !json.Valid(jsonSpec) {
		t.Fatal("Hack OpenAPI JSON/YAML routes differ or are not JSON (valid YAML 1.2)")
	}
	var spec map[string]any
	if err := json.Unmarshal(jsonSpec, &spec); err != nil {
		t.Fatal(err)
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("Hack OpenAPI has no paths")
	}
	for path := range paths {
		if hackLegacyStoragePath(path) {
			t.Errorf("retired path in Hack OpenAPI: %s", path)
		}
	}
	if _, ok := paths["/v1/sites/{sitename}/storage/resources"]; !ok {
		t.Fatal("Hack OpenAPI lacks current storage routes")
	}
	refs := 0
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, "#/") {
				refs++
				var target any = spec
				for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					m, ok := target.(map[string]any)
					if !ok {
						t.Errorf("dangling OpenAPI ref: %s", ref)
						return
					}
					target, ok = m[segment]
					if !ok {
						t.Errorf("dangling OpenAPI ref: %s", ref)
						return
					}
				}
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(paths)
	if refs == 0 {
		t.Fatal("OpenAPI ref check exercised no references")
	}
}
