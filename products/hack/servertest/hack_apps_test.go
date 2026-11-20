package servertest

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type hackAppsFixture struct {
	app                                      *teamSiteApp
	access, outsider, team, otherTeam, event string
	teamEvent, otherEvent, eventSite         string
}

func newHackAppsFixture(t *testing.T) hackAppsFixture {
	t.Helper()
	a := newTeamSiteApp(t)
	owner, organiser, outsider := a.newPerson(t, "apps-owner"), a.newPerson(t, "apps-org"), a.newPerson(t, "apps-outsider")
	event := a.makeEvent(t, owner)
	teamEvent := a.makeEvent(t, organiser)
	a.join(t, teamEvent, owner, organiser)
	team, _ := a.startTeam(t, teamEvent, "Alpha", owner)
	otherEvent := a.makeEvent(t, organiser)
	a.join(t, otherEvent, owner, organiser)
	otherTeam, _ := a.startTeam(t, otherEvent, "Beta", owner)
	for _, pair := range [][2]string{{teamEvent, team}, {otherEvent, otherTeam}} {
		markReady(t, a.certDir, pair[0])
		wantTS(t, "deploy team", a.deployTeam(t, pair[0], pair[1], owner.key, "team"), 201, "")
	}
	var eventSite string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, event).Scan(&eventSite); err != nil {
		t.Fatal(err)
	}
	markReady(t, a.certDir, event)
	wantTS(t, "event website", a.deployTeam(t, event, eventSite, owner.key, "event"), 201, "")
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, owner, client, testRedirect)["access_token"].(string)
	stranger := a.connect(t, outsider, client, testRedirect)["access_token"].(string)
	f := hackAppsFixture{a, access, stranger, team, otherTeam, event, teamEvent, otherEvent, eventSite}
	for _, target := range f.targets() {
		args := map[string]any{"site": target[0], "owner": target[1], "name": "inbox", "body": map[string]any{"kind": "sqlite", "rules": json.RawMessage(inboxRules)}}
		f.call(t, access, "storage_set_resource", args, false)
		delete(args, "body")
		args["sql"] = "CREATE TABLE entries (id INTEGER PRIMARY KEY, name TEXT, message TEXT, created_at TEXT)"
		f.call(t, access, "storage_sql_schema", args, false)
		args["sql"] = "INSERT INTO entries (name,message) VALUES (?,?)"
		args["params"] = []any{"<img src=x onerror=alert(1)>", "<script>bad()</script>"}
		for i := 0; i < 4; i++ {
			f.call(t, access, "storage_sql_execute", args, false)
		}
	}
	return f
}

// targets are the team site and the event website, as {site, owner}.
func (f hackAppsFixture) targets() [][2]string {
	return [][2]string{{f.team, f.teamEvent}, {f.eventSite, f.event}}
}

func (f hackAppsFixture) call(t *testing.T, access, name string, args map[string]any, wantError bool) map[string]any {
	t.Helper()
	text, out, bad := toolResultOf(t, f.app.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
	if bad != wantError {
		t.Fatalf("%s error=%v: %s", name, bad, text)
	}
	return out
}

func TestHackAppsRealPermissionsAndFlags(t *testing.T) {
	f := newHackAppsFixture(t)
	a := f.app
	t.Setenv("MCP_APP_MY_SITES", "off")
	t.Setenv("MCP_APP_DASHBOARD", "off")
	methods := []struct {
		method string
		args   map[string]any
	}{
		{"tools/list", map[string]any{}},
		{"tools/call", map[string]any{"name": "list_sites", "arguments": map[string]any{}}},
		{"tools/call", map[string]any{"name": "site_analytics", "arguments": map[string]any{"site": f.team, "owner": f.teamEvent, "days": 7}}},
	}
	baseline := make([]string, len(methods))
	for i, m := range methods {
		baseline[i] = string(a.rpc(t, f.access, m.method, m.args).body)
	}
	t.Setenv("MCP_APP_MY_SITES", "on")
	t.Setenv("MCP_APP_DASHBOARD", "on")
	// A person outside the event gets no team data, but a successful App
	// entry point, and no site read naming the event.
	if outside := f.call(t, f.outsider, "list_sites", map[string]any{}, false); len(outside["sites"].([]any)) != 0 {
		t.Fatal("team data leaked")
	}
	f.call(t, f.outsider, "storage_list_resources", map[string]any{"site": f.team, "owner": f.teamEvent}, true)
	listing := a.rpc(t, f.access, "tools/list", map[string]any{}).json(t)["result"].(map[string]any)["tools"].([]any)
	for _, raw := range listing {
		tool := raw.(map[string]any)
		// Only appToolMetadata's own resourceUri decoration is what this
		// loop guards -- never attached outside list_sites here. A
		// hack_app_* twin's _meta.ui.visibility (SIM-344 phase 2, Grok
		// security review) is a different, unconditional field under the
		// same "ui" key, so it is not what this invariant is about (see
		// products/hack/mcp/apps_hack_test.go's own TestHackAppsGatesAndUnchangedInventory
		// for the same narrowing).
		meta, _ := tool["_meta"].(map[string]any)
		ui, _ := meta["ui"].(map[string]any)
		if (ui["resourceUri"] != nil) != (tool["name"] == "list_sites") {
			t.Fatalf("unexpected UI metadata on %s", tool["name"])
		}
	}
	resource := a.rpc(t, f.access, "resources/read", map[string]any{"uri": "ui://simple-host/my-sites"})
	if !strings.Contains(string(resource.body), "SHHackSites") || strings.Contains(string(resource.body), f.access) {
		t.Fatal("missing or credential-bearing resource")
	}
	analytics := a.rpc(t, f.access, "tools/call", map[string]any{"name": "site_analytics", "arguments": map[string]any{"site": f.team, "owner": f.teamEvent, "days": 7}}).json(t)["result"].(map[string]any)
	if analytics["isError"] == true {
		t.Fatal("team analytics refused")
	}
	if len(analytics["_meta"].(map[string]any)["simple-host/daily"].([]any)) != 7 {
		t.Fatal("seven-day chart missing")
	}
	for _, target := range f.targets() {
		f.call(t, f.access, "storage_list_resources", map[string]any{"site": target[0], "owner": target[1]}, false)
		args := map[string]any{"site": target[0], "owner": target[1], "name": "inbox", "sql": "SELECT * FROM entries ORDER BY created_at DESC, id DESC LIMIT 3", "params": []any{}}
		result := f.call(t, f.access, "storage_sql_query", args, false)
		if len(result["response"].(map[string]any)["rows"].([]any)) != 3 {
			t.Fatalf("latest three entries: %v", result)
		}
		f.call(t, f.outsider, "storage_sql_query", args, true)
	}
	// Another event's team site is only reachable naming that event.
	f.call(t, f.access, "storage_list_resources", map[string]any{"site": f.otherTeam, "owner": f.teamEvent}, true)
	f.call(t, f.access, "site_analytics", map[string]any{"site": f.otherTeam, "owner": f.teamEvent}, true)
	for _, uri := range []string{"photo-picker", "site-preview"} {
		t.Setenv("MCP_APP_PHOTO_PICKER", "on")
		t.Setenv("MCP_APP_SITE_PREVIEW", "on")
		if a.rpc(t, f.access, "resources/read", map[string]any{"uri": "ui://simple-host/" + uri}).json(t)["error"] == nil {
			t.Fatal("forbidden Hack app")
		}
	}
	t.Setenv("MCP_APP_MY_SITES", "off")
	t.Setenv("MCP_APP_DASHBOARD", "off")
	for i, m := range methods {
		if string(a.rpc(t, f.access, m.method, m.args).body) != baseline[i] {
			t.Fatalf("disabled %s result changed", m.method)
		}
	}
}

// Optional local fixture for the browser bridge harness. Its private file is read
// by the host process, never by the frame, and removed by the test cleanup.
func TestServeHackAppsBrowser(t *testing.T) {
	path := os.Getenv("HACK_APPS_FIXTURE")
	if path == "" {
		t.Skip("HACK_APPS_FIXTURE unset")
	}
	t.Setenv("MCP_APP_MY_SITES", "on")
	t.Setenv("MCP_APP_DASHBOARD", "on")
	f := newHackAppsFixture(t)
	stop := make(chan struct{})
	f.app.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stop:
		default:
			close(stop)
		}
		w.WriteHeader(204)
	})
	data, _ := json.Marshal(map[string]string{"url": f.app.srv.URL, "access": f.access, "team": f.team, "event": f.event})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	select {
	case <-stop:
	case <-time.After(15 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
