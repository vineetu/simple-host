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
		for _, caller := range []Caller{{DeployOnly: true}, {AppName: "ChatGPT"}} {
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
	if len(listed) <= 4 || listed[1].Name != "fixture_app_read" || byName["fixture_app_read"].run == nil || byName["update_site"].run == nil {
		t.Fatalf("personal connection lists %d tools", len(listed))
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
	caller := Caller{Mode: CallerModeEvents, GrantID: "grant"}
	if page := read(with, caller); !strings.Contains(page, "window.FIXTURE_PRODUCT_SCRIPT = true") {
		t.Fatal("product page not rendered on the product connection")
	}
	if plain := read(without, caller); strings.Contains(plain, "FIXTURE_PRODUCT_SCRIPT") {
		t.Fatal("nil SitesPage changed the page")
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
	for _, caller := range []Caller{{}, {Mode: CallerModeEvents, GrantID: "grant"}} {
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

// Discovery gives a product connection Host's short list: the core tools,
// find_tools and run_tool, every other tool reached by run_tool under the
// same call. The flat list (ChatGPT) and a nil Discovery list every tool.
func TestContributionDiscovery(t *testing.T) {
	product := fixtureProduct()
	product.Discovery = &ProductDiscovery{Core: []string{"fixture_read"}, Instructions: "More: find_tools.",
		Aliases: map[string][]string{"storage_sql_schema": {"make a table"}},
		Tool:    func(tool Tool) Tool { tool.Title = "Fixture " + tool.Name; return tool }}
	s := NewServer(Config{Contribution: product, Events: &fakeEventsBackend{}})
	caller := Caller{Mode: CallerModeEvents, GrantID: "grant"}
	listed, _, byName := s.forCaller(caller)
	var names []string
	for _, tool := range listed {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "fixture_read,find_tools,run_tool" || byName["update_site"].run == nil || byName["run_tool"].Title != "Fixture run_tool" {
		t.Fatalf("short list %v", names)
	}
	if text := s.instructionsFor(caller); !strings.HasSuffix(text, "\n\nMore: find_tools.") {
		t.Fatalf("instructions %q", text)
	}
	if got := s.searchFor(caller).rank("make a table", 1); len(got) != 1 || got[0].tool.Name != "storage_sql_schema" {
		t.Fatalf("product search %v", got)
	}
	flat := Caller{Mode: CallerModeEvents, GrantID: "grant", AppName: "ChatGPT"}
	full, _, _ := s.forCaller(flat)
	plain, _, _ := NewServer(Config{Contribution: fixtureProduct(), Events: &fakeEventsBackend{}}).forCaller(caller)
	if len(full) != len(plain) || len(full) <= 3 || strings.Contains(s.instructionsFor(flat), "More: find_tools.") {
		t.Fatalf("flat list %d tools, plain %d", len(full), len(plain))
	}
	for _, tool := range full {
		if tool.Name == "find_tools" || tool.Name == "run_tool" {
			t.Fatalf("flat list names %s", tool.Name)
		}
	}
}
