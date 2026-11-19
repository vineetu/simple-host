package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZeroContributionHostInventory(t *testing.T) {
	for _, events := range []bool{false, true} {
		cfg := Config{APIHost: "connector.example.test"}
		if events {
			cfg.Events = inventoryEvents{}
		}
		composed := NewServer(testConfig(cfg))
		cfg.Contribution = &Contribution{}
		host := NewServer(testConfig(cfg))
		for _, caller := range []Caller{{}, {DeployOnly: true}, {AppName: "ChatGPT"}, {AppName: "ChatGPT", DeployOnly: true}} {
			a, b, _ := composed.forCaller(caller)
			x, y, _ := host.forCaller(caller)
			want, _ := json.Marshal([]any{a, b, composed.instructionsFor(caller)})
			got, _ := json.Marshal([]any{x, y, host.instructionsFor(caller)})
			if string(got) != string(want) {
				t.Fatalf("Host wire changed with zero contribution (events=%t caller=%+v)", events, caller)
			}
		}
		for _, entry := range host.inventory() {
			if entry.edition == editionProduct {
				t.Fatalf("zero contribution registered %s", entry.tool.Name)
			}
		}
		if host.apps.Screens != nil || host.apps.Gate != nil || host.apps.Read != nil || host.apps.Resources != nil {
			t.Fatal("zero contribution installed product apps")
		}
	}
}

func TestContributionNoTarget(t *testing.T) {
	for _, tc := range []struct {
		name, flag, tool string
		args             map[string]any
		listable, zero   bool
	}{
		{name: "listable", flag: "on", tool: "list_sites", listable: true},
		{name: "screen off", flag: "off", tool: "list_sites"},
		{name: "other tool", flag: "on", tool: "get_site", args: map[string]any{"site": "fixture"}},
		{name: "nil hook", flag: "on", tool: "list_sites", zero: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_MY_SITES", tc.flag)
			contribution := *fixtureProduct()
			calls := 0
			if tc.zero {
				contribution = Contribution{}
			} else {
				original := contribution.NoTarget
				contribution.NoTarget = func(tool string, listable bool) (string, map[string]any, bool) {
					calls++
					if tool != tc.tool || listable != tc.listable {
						t.Fatalf("NoTarget(%q,%t)", tool, listable)
					}
					return original(tool, listable)
				}
			}
			s := NewServer(testConfig(Config{Contribution: &contribution, Upstream: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("no-target call reached REST") })}))
			params, _ := json.Marshal(map[string]any{"name": tc.tool, "arguments": tc.args})
			reply := s.dispatch(httptest.NewRequest("POST", "/mcp", nil), request{ID: json.RawMessage("1"), Method: "tools/call", Params: params}, Caller{Mode: CallerModeEvents, GrantID: "grant", APIKey: "person", Handle: "fixture"})
			text := "Fixture: select a target for " + tc.tool + "."
			var structured map[string]any
			if tc.listable {
				text = "Fixture: nothing selected, nothing listed."
				structured = map[string]any{"sites": []any{}, "count": 0}
			}
			if tc.zero {
				text = "No target selected."
			}
			want := toolResult(json.RawMessage("1"), text, structured, !tc.listable, false)
			gotJSON, _ := json.Marshal(reply)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("got %s want %s", gotJSON, wantJSON)
			}
			if !tc.zero && calls != 1 {
				t.Fatalf("NoTarget called %d times", calls)
			}
		})
	}
}

func TestContributionAppsPerServer(t *testing.T) {
	calls := 0
	contribution := Contribution{Apps: func(ctx AppContext) AppContribution {
		calls++
		if ctx.APIHost != "connector.example.test" || ctx.Meta == nil {
			t.Fatalf("context = %+v", ctx)
		}
		if ctx.Meta(AppScreen{URI: "ui://example/screen", ID: "screen"})["ui"] == nil {
			t.Fatal("missing platform app metadata")
		}
		return AppContribution{}
	}}
	s := NewServer(testConfig(Config{APIHost: "connector.example.test", Contribution: &contribution}))
	for range 2 {
		s.forCaller(Caller{})
		s.productAppResources(Caller{}, true)
	}
	if calls != 1 {
		t.Fatalf("Apps called %d times for one Server", calls)
	}
	NewServer(testConfig(Config{APIHost: "connector.example.test", Contribution: &contribution}))
	if calls != 2 {
		t.Fatalf("Apps called %d times for two Servers", calls)
	}
}

// AppFamily is one more family, built from the tools of the product's own
// families and registered after them, before the product's notification tools.
func TestContributionAppFamily(t *testing.T) {
	product := fixtureProduct()
	var gotBase []string
	calls := 0
	product.Apps = func(AppContext) AppContribution {
		return AppContribution{AppFamily: func(base []Tool) FamilyTools {
			calls++
			for _, tool := range base {
				gotBase = append(gotBase, tool.Name)
			}
			twin := base[0]
			twin.Name = "fixture_app_read"
			return FamilyTools{Family: "fixture-app-only", Tools: []Tool{twin}}
		}}
	}
	s := NewServer(Config{Contribution: product, Events: inventoryEvents{}})
	if calls != 1 || len(gotBase) != 1 || gotBase[0] != "fixture_read" {
		t.Fatalf("AppFamily called %d times with %v", calls, gotBase)
	}
	var names, families []string
	for _, entry := range s.inventory() {
		if entry.edition == editionProduct {
			names, families = append(names, entry.tool.Name), append(families, string(entry.family))
		}
	}
	wantNames := []string{"fixture_read", "fixture_app_read", "list_notifications", "remove_notification"}
	wantFamilies := []string{"fixture", "fixture-app-only", "notifications", "notifications"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") || strings.Join(families, ",") != strings.Join(wantFamilies, ",") {
		t.Fatalf("registered %v in %v", names, families)
	}
	listed, _, byName := s.forCaller(Caller{Mode: CallerModeEvents})
	if len(listed) != 4 || listed[1].Name != "fixture_app_read" || byName["fixture_app_read"].run == nil {
		t.Fatalf("personal connection lists %d tools", len(listed))
	}
	// The documentation inventory composes the same family.
	var inventory []string
	for _, entry := range Inventory(*product) {
		if entry.Edition == "product" {
			inventory = append(inventory, entry.Name+"/"+entry.Family)
		}
	}
	if strings.Join(inventory, ",") != "fixture_read/fixture,fixture_app_read/fixture-app-only,list_notifications/notifications,remove_notification/notifications" {
		t.Fatalf("inventory = %v", inventory)
	}
	// Nil Apps and nil AppFamily add no family.
	for _, apps := range []func(AppContext) AppContribution{nil, func(AppContext) AppContribution { return AppContribution{} }} {
		product := fixtureProduct()
		product.Apps = apps
		for _, entry := range NewServer(Config{Contribution: product}).inventory() {
			if entry.edition == editionProduct && entry.family != "fixture" {
				t.Fatalf("no AppFamily registered %s", entry.tool.Name)
			}
		}
	}
}

// SitesPage is the script and the product name the platform's own screens
// carry on one of the product's connections. Nil keeps the platform's page,
// and a Simple Host connection never gets the product's.
func TestContributionSitesPage(t *testing.T) {
	t.Setenv("MCP_APP_MY_SITES", "on")
	read := func(s *Server, caller Caller) string {
		params, _ := json.Marshal(map[string]string{"uri": "ui://simple-host/my-sites"})
		reply := s.readApp(request{ID: json.RawMessage("1"), Params: params}, caller)
		if reply.Error != nil {
			t.Fatalf("read as %+v: %v", caller, reply.Error)
		}
		return reply.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string)
	}
	product := fixtureProduct()
	product.Apps = func(AppContext) AppContribution {
		return AppContribution{SitesPage: func() (string, string) { return "window.FIXTURE_PRODUCT_SCRIPT = true", "Fixture Product" }}
	}
	with, without := NewServer(Config{Contribution: product}), NewServer(Config{Contribution: fixtureProduct()})
	host := read(without, Caller{})
	if strings.Contains(host, "__HACK__") || strings.Contains(host, "__PRODUCT__") {
		t.Fatal("platform page left a placeholder")
	}
	for _, caller := range []Caller{{Mode: CallerModeGroupSite}, {Mode: CallerModeEvents, GrantID: "grant"}} {
		page := read(with, caller)
		if !strings.Contains(page, "window.FIXTURE_PRODUCT_SCRIPT = true") || !strings.Contains(page, "Fixture Product") || strings.Contains(page, "__HACK__") {
			t.Fatalf("product page not rendered for %+v", caller)
		}
		// Nil: the platform's page, without the product's script.
		plain := read(without, caller)
		if strings.Contains(plain, "FIXTURE_PRODUCT_SCRIPT") || strings.Contains(plain, "Fixture Product") || strings.Contains(plain, "__HACK__") || !strings.Contains(plain, "Simple Host") {
			t.Fatalf("nil SitesPage changed the page for %+v", caller)
		}
	}
	if page := read(with, Caller{}); page != host {
		t.Fatal("a Simple Host connection got the product's page")
	}
}

// MutationNames answers a call to a retired tool name with the product's
// message; through run_tool the refusal is recorded under the product's code.
// With none contributed the name is an ordinary unknown tool.
func TestContributionMutationNames(t *testing.T) {
	up := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("a retired name reached REST") })
	call := func(s *Server, caller Caller, name string) response {
		params, _ := json.Marshal(map[string]any{"name": name, "arguments": map[string]any{}})
		return s.dispatch(httptest.NewRequest("POST", "/mcp", nil), request{ID: json.RawMessage("1"), Method: "tools/call", Params: params}, caller)
	}
	const message = "Fixture: this tool was retired. [fixture_retired]"
	s := NewServer(Config{Contribution: fixtureProduct(), Upstream: up})
	want, _ := json.Marshal(toolResult(json.RawMessage("1"), message, nil, true, false))
	for _, caller := range []Caller{{}, {Mode: CallerModeEvents}, {Mode: CallerModeEvents, GrantID: "grant"}, {Mode: CallerModeGroupSite}, {DeployOnly: true}} {
		got, _ := json.Marshal(call(s, caller, "fixture_retired"))
		if string(got) != string(want) {
			t.Fatalf("as %+v: %s", caller, got)
		}
	}
	// Through run_tool the same message comes back as the tool's refusal.
	params, _ := json.Marshal(map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "fixture_retired"}})
	wrapped, _ := json.Marshal(s.dispatch(httptest.NewRequest("POST", "/mcp", nil), request{ID: json.RawMessage("1"), Method: "tools/call", Params: params}, Caller{}))
	if string(wrapped) != string(want) {
		t.Fatalf("through run_tool: %s", wrapped)
	}
	// Nil map and nil contribution: an unknown tool.
	for name, server := range map[string]*Server{
		"zero": NewServer(Config{Contribution: &Contribution{}, Upstream: up}),
		"nil":  NewServer(Config{Upstream: up}),
	} {
		reply := call(server, Caller{}, "fixture_retired")
		if reply.Error == nil || reply.Error.Code != codeInvalidParams || !strings.Contains(reply.Error.Message, "unknown tool: fixture_retired") {
			t.Fatalf("%s: %+v", name, reply)
		}
	}
}
