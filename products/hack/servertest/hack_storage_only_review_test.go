package servertest

import (
	"bytes"
	"encoding/json"

	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/handler"
)

// Checks the public router and both connector grant shapes: the removed
// saved-data routes are unknown, and Hack websites keep resource storage.
func TestHackStorageOnlyReview(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "storage-only-org")
	member := a.newPerson(t, "storage-only-member")
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
		key          string
	}{
		{"GET", base + "/state" + own, nil, key},
		{"PUT", base + "/state" + own, map[string]any{"old": true}, key},
		{"GET", base + "/state/history" + own, nil, key},
		{"GET", base + "/collections" + own, nil, key},
		{"POST", base + "/collections/old" + own, map[string]any{"old": true}, key},
		{"GET", base + "/collections/old/deleted" + own, nil, key},
		{"GET", base + "/data" + own, nil, key},
		{"GET", base + "/data/old/kind" + own, nil, key},
		{"PUT", base + "/data/old/kind" + own, map[string]any{"kind": "entries"}, key},
		{"GET", base + "/savers" + own, nil, key},
		{"DELETE", base + "/history" + own, map[string]string{"confirm": team}, key},
		{"PUT", base + "/allowed-origins" + own, map[string]any{"origins": []string{}}, key},
		{"GET", "/v1/u/" + slug + "/sites/" + team + "/state", nil, key},
		{"GET", "/v1/u/" + slug + "/sites/" + team + "/data/old", nil, key},
		{"GET", "/v1/admin/data-watch", nil, a.admin},
		{"GET", "/v1/data-notify/stop", nil, ""},
	} {
		// Gone: unknown to the router, or outside what an editor may call.
		if r := a.api(t, tc.method, tc.path, tc.body, tc.key); r.status != http.StatusNotFound && r.status != http.StatusForbidden {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	teamHost := team + "." + slug + "." + tsDomain
	wantTS(t, "team-host removed read", a.on(t, "GET", teamHost, base+"/state", nil, ""), http.StatusNotFound, "")
	wantTS(t, "team resource", a.api(t, "PUT", base+"/storage/resources/prefs"+own, map[string]any{"kind": "kv", "rules": json.RawMessage(publicRules)}, key), 201, "")
	wantTS(t, "team KV write", a.api(t, "PUT", base+"/storage/kv/prefs/keys/color"+own, map[string]any{"value": "blue"}, key), 200, "")
	if r := a.on(t, "GET", teamHost, base+"/storage/kv/prefs/keys/color", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "blue") {
		t.Fatalf("team visitor KV: %d %s", r.status, r.body)
	}
	wantTS(t, "visitor sign-in status", hackBrowserRequest(t, a, "GET", teamHost, base+"/me", nil), 200, "")

	wantTS(t, "event-site publish", a.deployTeam(t, slug, eventSite(t, a, slug, org), org.key, "event"), 201, "")
	eventHost := slug + "." + tsDomain
	wantTS(t, "event-host legacy read", a.on(t, "GET", eventHost, "/v1/sites/"+slug+"/state", nil, ""), 404, "not_found")
	wantTS(t, "event-host legacy alias", a.on(t, "GET", eventHost, "/v1/u/"+slug+"/sites/"+slug+"/data/old", nil, ""), 404, "not_found")
	wantTS(t, "native event remains", a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key), 200, "")

	// The person's own key and their connection both hide retired tools.
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, member, client, testRedirect)["access_token"].(string)
	for label, listing := range map[string]resp{
		"own key":        a.mcpWithKey(t, key),
		"personal grant": a.rpc(t, access, "tools/list", map[string]any{}),
	} {
		names := mcpToolNames(t, listing)
		for _, name := range []string{"get_state", "list_collections", "declare_data", "list_data", "data_history", "update_data", "get_site_passcode", "set_site_passcode", "remove_site_passcode", "sign_out_site_visitors"} {
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
	// The removed saved-data tools and the passcode tools are not offered to
	// this caller, so a direct call is an unknown tool.
	for _, name := range []string{"get_state", "list_data", "get_site_passcode", "set_site_passcode", "remove_site_passcode", "sign_out_site_visitors"} {
		r := a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"site": team}})
		e, _ := r.json(t)["error"].(map[string]any)
		if e == nil || !strings.Contains(e["message"].(string), "unknown tool") {
			t.Fatalf("direct %s was not rejected: %d %s", name, r.status, r.body)
		}
	}
}

// Exercises the registered public routes: the Hack spec must be the same
// complete JSON document at both extensions, with no dangling component refs.
func TestHackRegisteredDocsReview(t *testing.T) {
	handler.SetInstanceHosts("simple-hack.test", "sites.simple-hack.test", "")
	t.Cleanup(func() {
		handler.SetInstanceHosts("simple-host.app", "", "")
	})
	mux := http.NewServeMux()
	handler.RegisterUIRoutes(mux, "https://simple-hack.test", chromeTestHandler(productedition.Hack))
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
