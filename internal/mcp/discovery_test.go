package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFindToolsRanking(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	cases := []struct{ query, want string }{
		{"put my site on my own domain", "connect_domain"},
		{"shorter link for my site", "connect_domain"},
		{"password protect my site", "set_site_passcode"},
		{"undo my last change", "rollback_site"},
		{"how many visitors this week", "site_analytics"},
		{"webhook when someone books", "list_notifications"},
		{"move my wordpress site", "create_import_link"},
		{"download a backup of my site", "export_site"},
		{"hide my site from my public page", "set_visibility"},
		{"take my site offline", "set_site_offline"},
		{"bring back a deleted site", "restore_site"},
		{"who can read my saved data", "storage_list_resources"},
		{"how much storage space is left", "storage_get_usage"},
		{"photos visitors uploaded", "storage_list_file_objects"},
		{"rename my site", "rename_site"},
		{"remove the passcode", "remove_site_passcode"},
		{"remove a viewer", "revoke_site_viewer"},
		{"set my home page", "set_home_page"},
		{"preview an older version", "preview_version"},
		{"check my domain dns", "domain_status"},
		{"change my website address", "connect_domain"},
		{"disconnect my custom domain", "remove_domain"},
		{"let everyone in without a code", "remove_site_passcode"},
		{"see an old version without publishing it", "preview_version"},
		{"download my entire website", "export_site"},
		{"recover a website I deleted", "restore_site"},
		{"show me the trash", "list_deleted_sites"},
		{"temporarily close my website", "set_site_offline"},
		{"stop my site from expiring", "keep_site"},
		{"did my import finish", "get_import_report"},
		{"show the uploaded pictures", "storage_list_file_objects"},
		{"remove a saved setting", "storage_delete_kv"},
		{"take my site down for maintenance", "set_site_offline"},
		{"who visited my site", "site_analytics"},
		{"make my site private with a password", "set_site_passcode"},
		{"put my site back how it was yesterday", "rollback_site"},
		{"list my webhooks", "list_notifications"},
		// Additional phrasings from the 45-case discovery review.
		{"use my own URL", "connect_domain"},
		{"give me a short link", "connect_domain"},
		{"is my domain working yet", "domain_status"},
		{"check my SSL certificate", "domain_status"},
		{"lock my website with a PIN", "set_site_passcode"},
		{"what is my website password", "get_site_passcode"},
		{"I forgot my password", "get_site_passcode"},
		{"turn off password protection", "remove_site_passcode"},
		{"log everyone out of my website", "sign_out_site_visitors"},
		{"who is allowed to open my site", "list_site_viewers"},
		{"remove someone's access", "revoke_site_viewer"},
		{"go back to how my site looked yesterday", "rollback_site"},
		{"undo the update", "rollback_site"},
		{"show me my site's edit history", "list_versions"},
		{"keep only two versions", "set_keep_versions"},
		{"how many people came to my website", "site_analytics"},
		{"show my website traffic", "site_analytics"},
		{"stop sending notifications", "remove_notification"},
		{"show my webhook subscriptions", "list_notifications"},
		{"make a backup", "export_site"},
		{"delete my website", "delete_site"},
		{"hide my website from search engines", "set_visibility"},
		{"change the name of my site", "rename_site"},
		{"make this my homepage", "set_home_page"},
		{"change the description on my profile", "set_bio"},
		{"pin this website on my profile", "set_showcase_site"},
		{"move my Squarespace website here", "create_import_link"},
		{"what can visitors read in my database", "storage_list_resources"},
		{"how much disk space have I used", "storage_get_usage"},
		{"give me a link to download a photo", "storage_file_download_link"},
		{"list all saved settings", "storage_list_kv_keys"},
		{"delete my database", "storage_delete_resource"},
		{"delete an uploaded picture", "storage_delete_file"},
		// Operation synonyms must still beat shared site/file nouns.
		{"erase my website", "delete_site"},
		{"trash my website", "delete_site"},
		{"remove my website", "delete_site"},
		{"undelete my website", "restore_site"},
		{"restore a deleted site", "restore_site"},
		{"list deleted sites", "list_deleted_sites"},
		{"erase an uploaded picture", "storage_delete_file"},
		{"remove an uploaded picture", "storage_delete_file"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := s.search.rank(tc.query, 5)
			names := []string{}
			for _, match := range got {
				names = append(names, match.tool.Name)
			}
			if len(got) == 0 || got[0].tool.Name != tc.want {
				t.Fatalf("got %v, want %s first", names, tc.want)
			}
		})
	}
	for _, tool := range s.tools {
		if got := s.search.rank(tool.Name, 1); len(got) != 1 || got[0].tool.Name != tool.Name {
			t.Errorf("exact name %s did not rank first", tool.Name)
		}
		if !slices.Contains(coreToolNames, tool.Name) && len(toolAliases[tool.Name]) < 2 {
			t.Errorf("%s needs aliases", tool.Name)
		}
	}
}

func TestFindToolsTiesKeepInventoryOrder(t *testing.T) {
	search := newToolSearch([]Tool{{Name: "first", Description: "example"}, {Name: "second", Description: "example"}})
	matches := search.rank("example", 2)
	if len(matches) != 2 || matches[0].tool.Name != "first" || matches[1].tool.Name != "second" {
		t.Fatal("equal scores did not preserve inventory order")
	}
}

func TestCoreListingByRevision(t *testing.T) {
	for _, events := range []bool{false, true} {
		cfg := Config{}
		if events {
			cfg.Events = &fakeEventsBackend{}
		}
		s := NewServer(cfg)
		for _, deploy := range []bool{false, true} {
			for _, version := range append([]string{""}, supportedVersions...) {
				t.Run(fmt.Sprintf("events=%v/deploy=%v/%s", events, deploy, version), func(t *testing.T) {
					caller := Caller{APIKey: "test", DeployOnly: deploy}
					req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}}}`))
					req = req.WithContext(WithCaller(req.Context(), caller))
					req.Header.Set("MCP-Protocol-Version", version)
					if version == protocolVersion {
						req.Header.Set("Mcp-Method", "tools/list")
					}
					rec := httptest.NewRecorder()
					s.ServeHTTP(rec, req)
					res, ok := decode(t, rec)["result"].(map[string]any)
					if !ok {
						t.Fatalf("listing failed: %s", rec.Body.String())
					}
					names := []string{}
					for _, raw := range res["tools"].([]any) {
						tool := raw.(map[string]any)
						name := tool["name"].(string)
						names = append(names, name)
						_, schema := tool["outputSchema"]
						if schema != (version != "2025-03-26" && name != "run_tool") {
							t.Errorf("%s outputSchema = %v", name, schema)
						}
						ann := tool["annotations"].(map[string]any)
						if ann["title"] != tool["title"] {
							t.Errorf("%s annotation title differs", name)
						}
						if name == "run_tool" && (ann["readOnlyHint"] != false || ann["destructiveHint"] != true) {
							t.Error("unsafe run_tool hints")
						}
					}
					if !reflect.DeepEqual(names, coreToolNames) || len(names) != 20 {
						t.Fatalf("listed %v", names)
					}
					if version == protocolVersion && res["resultType"] != "complete" {
						t.Error("missing modern resultType")
					}
					_, _, byName := s.forCaller(caller)
					want := len(s.tools) + 2
					if deploy && events {
						want -= 2
					}
					if len(byName) != want {
						t.Fatalf("inventory has %d, want %d", len(byName), want)
					}
				})
			}
		}
	}
}

func TestFindToolsResults(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	for _, query := range []string{"", "zzzzzz", "connect_domain", "create_site", "storage_set_resource", "update_site", "set_keep_versions"} {
		text, structured, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": query}), nil, true))
		if isErr {
			t.Fatal(text)
		}
		if err := ValidateOutput(s.byName["find_tools"].OutputSchema, structured, nil); err != nil {
			t.Fatal(err)
		}
		items := structured["tools"].([]any)
		if query == "" || query == "zzzzzz" {
			expected := []string{}
			for _, tool := range s.tools {
				if !slices.Contains(coreToolNames, tool.Name) {
					expected = append(expected, tool.Name)
				}
			}
			names := []string{}
			for _, raw := range items {
				item := raw.(map[string]any)
				if len(item) != 2 {
					t.Fatalf("catalog has more than names and titles: %v", item)
				}
				names = append(names, item["name"].(string))
			}
			if !reflect.DeepEqual(names, expected) {
				t.Fatalf("catalog order: %v", names)
			}
			if query != "" && !strings.Contains(text, "Nothing matched") {
				t.Error("no-match explanation missing")
			}
		} else {
			item := items[0].(map[string]any)
			if item["name"] != query || item["listed"] != slices.Contains(coreToolNames, query) || item["arguments"] == nil || item["description"] == nil || item["outputSchema"] != nil {
				t.Fatalf("bad match: %v", item)
			}
			for _, raw := range items {
				match := raw.(map[string]any)
				tool := s.byName[match["name"].(string)]
				schema, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(text+"\n", "Arguments: "+string(schema)+"\n") || !strings.Contains(text, tool.Description+"\nArguments: ") {
					t.Errorf("%s text lacks description or full compact schema: %s", tool.Name, text)
				}
			}
			if len(items) > 3 {
				t.Error("default limit ignored")
			}
		}
	}
	text, _, _ := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "password protect my site"}), nil, true))
	schema, _ := json.Marshal(s.byName["set_site_passcode"].InputSchema)
	if !strings.Contains(text, "set_site_passcode (run_tool)") || !strings.Contains(text, "Arguments: "+string(schema)) {
		t.Errorf("passcode tool text lacks name or input schema: %s", text)
	}
	for _, limit := range []any{0, 11, 1.5, "5", nil} {
		_, _, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "site", "limit": limit}), nil, true))
		if !isErr {
			t.Errorf("accepted limit %v", limit)
		}
	}
	_, out, _ := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "site", "limit": 1}), nil, true))
	if len(out["tools"].([]any)) != 1 {
		t.Error("limit ignored")
	}
}

func TestSearchTermsStemming(t *testing.T) {
	for _, pair := range [][2]string{
		{"upload publish change setting", "uploads publishing changed settings"},
		{"save", "saved"},
		{"copy", "copied"},
		{"pin", "pinned"},
		{"expire", "expiring"},
		{"expire", "expiry"},
	} {
		t.Run(pair[1], func(t *testing.T) {
			base, inflected := searchTerms(pair[0]), searchTerms(pair[1])
			if !reflect.DeepEqual(base, inflected) {
				t.Errorf("%q stems to %v; %q stems to %v", pair[0], base, pair[1], inflected)
			}
		})
	}
}

func TestRunToolDispatch(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/versions": func() (int, string) { return 200, `[{"version":1}]` },
	}}
	s := newTestServer(up)
	for _, modern := range []bool{false, true} {
		caller := Caller{APIKey: "test-key"}
		direct := sendEvent(t, s, "tools/call", map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}, caller, modern)
		wrapped := sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}}, caller, modern)
		text, _, isErr := resultOf(t, wrapped)
		if isErr || !reflect.DeepEqual(decode(t, direct), decode(t, wrapped)) {
			t.Fatalf("wrapped differs: %s / %s", text, direct.Body.String())
		}
	}
	for _, tc := range []struct {
		args   map[string]any
		want   string
		schema bool
	}{
		{map[string]any{"name": "run_tool"}, "cannot call", false},
		{map[string]any{"name": "find_tools"}, "cannot call", false},
		{map[string]any{"name": "rename_sit"}, "rename_site", false},
		{map[string]any{"name": "list_versions", "arguments": []any{}}, "arguments must be an object", true},
		{map[string]any{"name": "list_versions", "arguments": nil}, "arguments must be an object", true},
		{map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog", "extra": true}}, "unexpected argument", true},
		{map[string]any{"name": "list_versions"}, "site is required", true},
		{map[string]any{"name": "list_versions", "arguments": map[string]any{"site": 12}}, "must be a string", true},
		{map[string]any{"name": "get_state"}, "no longer offered", false},
	} {
		text, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", tc.args), nil, true))
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%v: %s", tc.args, text)
		}
		if tc.schema {
			schema, _ := json.Marshal(s.byName["list_versions"].InputSchema)
			if !strings.Contains(text, string(schema)) {
				t.Errorf("missing compact schema: %s", text)
			}
		}
		if tc.args["name"] == "rename_sit" && !strings.Contains(text, "find_tools") {
			t.Error("missing find_tools advice")
		}
	}
	s.byName["no_args"] = Tool{Name: "no_args", InputSchema: noArgs(), run: func(_ *call, args map[string]any) (output, error) {
		if len(args) != 0 {
			t.Error("omitted arguments not empty")
		}
		return output{Text: "ok"}, nil
	}}
	text, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", map[string]any{"name": "no_args"}), nil, true))
	if isErr || text != "ok" {
		t.Fatalf("omitted arguments: %s", text)
	}
}

func TestDiscoveryCallerInventories(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	caller := Caller{APIKey: "test", DeployOnly: true}
	for _, name := range []string{"list_notifications", "remove_notification"} {
		text, _, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": name}}, caller, false))
		if !isErr || !strings.Contains(text, "Unknown tool") {
			t.Fatalf("deploy call: %s", text)
		}
	}
	for _, query := range []string{"", "webhook when someone books", "remove_notification"} {
		_, structured, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "find_tools", "arguments": map[string]any{"query": query}}, caller, false))
		if isErr {
			t.Fatal("deploy search failed")
		}
		for _, raw := range structured["tools"].([]any) {
			name := raw.(map[string]any)["name"]
			if name == "list_notifications" || name == "remove_notification" {
				t.Error("deploy search leaked notifications")
			}
		}
	}
	for _, caller := range []Caller{{Mode: CallerModeEvents}, {Mode: CallerModeEvents, GrantID: "grant"}, {Mode: CallerModeHackSite}} {
		full, bare, byName := s.forCaller(caller)
		expected := append([]Tool{}, s.hackTools...)
		if caller.Mode == CallerModeHackSite {
			expected = s.hackSiteTools
		} else if caller.GrantID != "" {
			for _, tool := range s.hackSiteTools {
				if tool.Name != "get_page_recipe" {
					expected = append(expected, tool)
				}
			}
		}
		if len(full) != len(expected) || len(bare) != len(expected) {
			t.Fatal("Hack inventory count changed")
		}
		for i, tool := range expected {
			if full[i].Name != tool.Name || bare[i].Name != tool.Name {
				t.Fatal("Hack order changed")
			}
		}
		for _, name := range []string{"run_tool", "find_tools"} {
			if _, ok := byName[name]; ok {
				t.Errorf("Hack has %s", name)
			}
			caller.APIKey = "test"
			eventErrorCode(t, sendEvent(t, s, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"name": "list_notifications", "query": "webhook"}}, caller, false), codeInvalidParams)
		}
	}
	brain := NewBrainServer(BrainConfig{Query: func(string, map[string]any) (string, map[string]any, error) { return "", nil, nil }})
	full, _, byName := brain.forCaller(Caller{})
	if len(full) != 7 || byName["run_tool"].Name != "" || byName["find_tools"].Name != "" {
		t.Error("brain inventory changed")
	}
}

// productionHostRewrite mirrors handler.newHostRewriter with default content and
// CNAME hosts. handler imports mcp, and its rewriter is unexported.
func productionHostRewrite(siteDomain string) func(string) string {
	const canonicalSiteDomain = "simple-host.app"
	contentHost, cnameTarget := "sites."+siteDomain, "cname."+siteDomain
	return strings.NewReplacer(
		// The hosted instance hands out per-person addresses; an instance on
		// another domain keeps the path model (PERSON_HOSTS defaults to off),
		// so its docs describe the path address instead.
		// Per-site addresses (<site>.<handle>.simple-host.app, SITE_HOSTS)
		// become the path address too.
		"<site>.<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/<site>/",
		"<site>.<handle>."+canonicalSiteDomain, contentHost+"/<handle>/<site>",
		"<sitename>.<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/<sitename>/",
		"<sitename>.<handle>."+canonicalSiteDomain, contentHost+"/<handle>/<sitename>",
		"<name>.<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/<name>/",
		"<name>.<handle>."+canonicalSiteDomain, contentHost+"/<handle>/<name>",
		"&lt;site&gt;.&lt;handle&gt;."+canonicalSiteDomain+"/", contentHost+"/&lt;handle&gt;/&lt;site&gt;/",
		"&lt;site&gt;.&lt;handle&gt;."+canonicalSiteDomain, contentHost+"/&lt;handle&gt;/&lt;site&gt;",
		"<handle>."+canonicalSiteDomain+"/v1", contentHost+"/v1",
		"<handle>."+canonicalSiteDomain+"/<sitename>", contentHost+"/<handle>/<sitename>",
		"<handle>."+canonicalSiteDomain+"/<site>", contentHost+"/<handle>/<site>",
		"<handle>."+canonicalSiteDomain+"/<name>", contentHost+"/<handle>/<name>",
		"<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/",
		"<handle>."+canonicalSiteDomain, contentHost+"/<handle>",
		"&lt;handle&gt;."+canonicalSiteDomain+"/&lt;site&gt;", contentHost+"/&lt;handle&gt;/&lt;site&gt;",
		"&lt;handle&gt;."+canonicalSiteDomain+"/", contentHost+"/&lt;handle&gt;/",
		"&lt;handle&gt;."+canonicalSiteDomain, contentHost+"/&lt;handle&gt;",
		// Every site shares the content host's origin in the path model.
		"(its own browser origin)", "(one browser origin, shared by every site on this server)",
		"sites."+canonicalSiteDomain, contentHost,
		"cname."+canonicalSiteDomain, cnameTarget,
		canonicalSiteDomain, siteDomain,
	).Replace
}

func TestInstructionsLength(t *testing.T) {
	defer NewServer(Config{})
	// A long but realistic instance host (one full 63-character label).
	longHost := strings.Repeat("a", 63) + ".sites.example-company.org"
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"hosted", Config{}}, {"NoSpamCheck", Config{NoSpamCheck: true}}, {"SelfHosted", Config{SelfHosted: true}},
		{"SelfHosted/NoSpamCheck", Config{SelfHosted: true, NoSpamCheck: true}},
		{"long hostname", Config{SelfHosted: true, Rewrite: productionHostRewrite(longHost)}},
		{"long hostname/NoSpamCheck", Config{SelfHosted: true, NoSpamCheck: true, Rewrite: productionHostRewrite(longHost)}},
		{"other instance", Config{SelfHosted: true, NoSpamCheck: true, Rewrite: productionHostRewrite("websites.example.org"), SupportContact: "support@example.org"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := NewServer(tc.cfg).instructionsFor(Caller{})
			t.Logf("instructions: %d characters (%d bytes)", len([]rune(text)), len(text))
			if n := len([]rune(text)); n >= 2000 {
				t.Fatalf("instructions too long: %d", n)
			}
			if tc.cfg.Rewrite != nil && !strings.Contains(text, "/<handle>/<site>/") {
				t.Error("missing production path address")
			}
			if strings.Contains(text, "{claude_reconnect}") || strings.Contains(text, "{deleted_retention}") {
				t.Error("unfilled placeholder")
			}
			if strings.Contains(text, "comment") == tc.cfg.NoSpamCheck {
				t.Error("comments topic does not match configuration")
			}
			if !strings.Contains(text, "Give the person each url exactly as returned.") {
				t.Error("URL instructions changed")
			}
			if !strings.HasSuffix(text, "If a tool says the connection is signed out, ask the person to reconnect Simple Host in their app.") {
				t.Error("missing signed-out advice")
			}
		})
	}
}

func TestSignedOutReconnect(t *testing.T) {
	defer NewServer(Config{})
	for _, self := range []bool{false, true} {
		for _, code := range []string{"key_expired", "key_expired_idle", "invalid_api_key", ""} {
			up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/me": func() (int, string) { return 401, `{"error":"connection expired","code":"` + code + `"}` }}}
			cfg := Config{Upstream: up, SelfHosted: self}
			if self {
				cfg.Rewrite = strings.NewReplacer("simple-host.app", "box.example").Replace
			}
			s := NewServer(cfg)
			text, _, isErr := resultOf(t, send(t, s, toolCall("who_am_i", nil), nil, true))
			for _, want := range []string{"connection expired", "https://chatgpt.com/plugins", "Add custom MCP server", "never share an API key", "Sign in in the window that opens, then choose Allow."} {
				if !isErr || !strings.Contains(text, want) {
					t.Errorf("%v/%s lacks %q: %s", self, code, want, text)
				}
			}
			if strings.Contains(text, "Sign in with Google or an email code") {
				t.Error("reconnect assumes a sign-in provider")
			}
			if self {
				if strings.Contains(text, "simple-host.app") || !strings.Contains(text, "Add custom connector") || !strings.Contains(text, "https://box.example/mcp") {
					t.Errorf("wrong self reconnect: %s", text)
				}
			} else if !strings.Contains(text, claudeReconnectHosted) {
				t.Error("missing hosted Claude reconnect")
			}
		}
	}
}
