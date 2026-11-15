package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

// Captured from the pre-extraction implementation. Hash the wire bytes, including
// listing order, schemas and instructions, across connection and protocol kinds.
func contributionSnapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	callers := map[string]Caller{
		"host": {}, "deploy": {DeployOnly: true}, "flat": {AppName: "ChatGPT"},
		"flat-deploy": {AppName: "ChatGPT", DeployOnly: true},
		"personal":    {Mode: CallerModeEvents}, "selected": {Mode: CallerModeEvents, GrantID: "grant", TeamAPIKey: "team"},
		"unselected": {Mode: CallerModeEvents, GrantID: "grant"}, "team": {Mode: CallerModeHackSite},
	}
	for _, screens := range []string{"off", "on"} {
		for _, flag := range []string{"MCP_APP_HACK_JUDGE", "MCP_APP_HACK_ORGANISER", "MCP_APP_MY_SITES", "MCP_APP_DASHBOARD", "MCP_APP_PASSCODE"} {
			t.Setenv(flag, screens)
		}
		for _, rules := range []string{"off", "on"} {
			t.Setenv("STORAGE_RULES", rules)
			for _, events := range []bool{false, true} {
				cfg := Config{APIHost: "connector.example.test", ServerName: "fixture", Version: "fixture"}
				if events {
					cfg.Events = inventoryEvents{}
				}
				s := NewServer(cfg)
				for name, c := range callers {
					c.Handle = "fixture"
					c.APIKey = "fixture"
					for _, version := range supportedVersions {
						for _, ui := range []bool{false, true} {
							s.clientUI.remember(c, ui, time.Now())
							params := map[string]any{}
							if version == protocolVersion {
								extensions := map[string]any{}
								if ui {
									extensions["io.modelcontextprotocol/ui"] = map[string]any{}
								}
								params["_meta"] = map[string]any{"io.modelcontextprotocol/clientCapabilities": map[string]any{"extensions": extensions}}
							}
							raw, _ := json.Marshal(params)
							r := httptest.NewRequest("POST", "/mcp", nil)
							r.Header.Set("MCP-Protocol-Version", version)
							reply := s.dispatch(r, request{ID: json.RawMessage("1"), Method: "tools/list", Params: raw}, c)
							data, err := json.Marshal(struct {
								List         response
								Instructions string
							}{reply, s.instructionsFor(c)})
							if err != nil {
								t.Fatal(err)
							}
							key := fmt.Sprintf("%s/screens=%s/rules=%s/events=%t/%s/ui=%t", name, screens, rules, events, version, ui)
							out[key] = fmt.Sprintf("%x", sha256.Sum256(data))
						}
					}
				}
			}
		}
	}
	return out
}

func TestContributionWireUnchanged(t *testing.T) {
	got := contributionSnapshot(t)
	data, err := os.ReadFile("testdata/contribution-wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("snapshots: got %d want %d", len(got), len(want))
	}
	for key, hash := range got {
		if hash != want[key] {
			t.Errorf("wire changed for %s: got %s want %s", key, hash, want[key])
		}
	}
}

func TestZeroContributionHostInventory(t *testing.T) {
	for _, events := range []bool{false, true} {
		cfg := Config{APIHost: "connector.example.test"}
		if events {
			cfg.Events = inventoryEvents{}
		}
		composed := NewServer(cfg)
		cfg.Contribution = &Contribution{}
		host := NewServer(cfg)
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
			if entry.edition == editionHack {
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
			contribution := hackContribution()
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
			s := NewServer(Config{Contribution: &contribution, Upstream: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("no-target call reached REST") })})
			params, _ := json.Marshal(map[string]any{"name": tc.tool, "arguments": tc.args})
			reply := s.dispatch(httptest.NewRequest("POST", "/mcp", nil), request{ID: json.RawMessage("1"), Method: "tools/call", Params: params}, Caller{Mode: CallerModeEvents, GrantID: "grant", APIKey: "person", Handle: "fixture"})
			text := "Choose a team with hack_select_team before publishing its site."
			var structured map[string]any
			if tc.listable {
				text = "No team selected. Call hack_get_my_teams to choose a team, or hack_list_events for your event websites."
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
		if ctx.APIHost != "connector.example.test" || ctx.Fixed || ctx.Meta == nil {
			t.Fatalf("context = %+v", ctx)
		}
		if ctx.Meta(AppScreen{URI: "ui://example/screen", ID: "screen"})["ui"] == nil {
			t.Fatal("missing platform app metadata")
		}
		return AppContribution{}
	}}
	s := NewServer(Config{APIHost: "connector.example.test", Contribution: &contribution})
	for range 2 {
		s.forCaller(Caller{})
		s.productAppResources(Caller{}, true)
	}
	if calls != 1 {
		t.Fatalf("Apps called %d times for one Server", calls)
	}
	NewServer(Config{APIHost: "connector.example.test", Contribution: &contribution})
	if calls != 2 {
		t.Fatalf("Apps called %d times for two Servers", calls)
	}
}

func TestContributionAppMetadataParity(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	contribution := hackContribution()
	factory := contribution.Apps
	var metadata func(AppScreen) map[string]any
	var seen []AppScreen
	contribution.Apps = func(ctx AppContext) AppContribution {
		metadata = ctx.Meta
		ctx.Meta = func(screen AppScreen) map[string]any {
			seen = append(seen, screen)
			return metadata(screen)
		}
		return factory(ctx)
	}
	s := NewServer(Config{APIHost: "screens.example.test", Contribution: &contribution})
	caller := Caller{Mode: CallerModeEvents, Handle: "fixture"}
	resources := s.productAppResources(caller, true)
	if len(resources) != len(hackAppScreens) || !reflect.DeepEqual(seen, hackAppScreens) {
		t.Fatalf("resource metadata callbacks = %#v", seen)
	}
	seen = nil
	for i, screen := range hackAppScreens {
		t.Run(screen.ID, func(t *testing.T) {
			want := s.appMeta(screen)
			if got := metadata(screen); !reflect.DeepEqual(got, want) {
				t.Fatalf("exported metadata = %#v; want %#v", got, want)
			}
			if got := resources[i].(map[string]any)["_meta"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("listed metadata = %#v; want %#v", got, want)
			}
			params, _ := json.Marshal(map[string]string{"uri": screen.URI})
			reply, handled := s.apps.Read(Request{ID: json.RawMessage("1"), Params: params}, caller, true)
			if !handled || reply.Error != nil {
				t.Fatalf("read: handled=%t error=%v", handled, reply.Error)
			}
			content := reply.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
			if got := content["_meta"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("read metadata = %#v; want %#v", got, want)
			}
			ui := want["ui"].(map[string]any)
			permissions, hasPermissions := ui["permissions"]
			if screen.ID == "hack-organiser" {
				if !reflect.DeepEqual(permissions, map[string]any{"clipboardWrite": map[string]any{}}) {
					t.Fatalf("organiser permissions = %#v", permissions)
				}
			} else if hasPermissions {
				t.Fatalf("judge has permissions: %#v", permissions)
			}
		})
	}
	if !reflect.DeepEqual(seen, hackAppScreens) {
		t.Fatalf("read metadata callbacks = %#v", seen)
	}
}
