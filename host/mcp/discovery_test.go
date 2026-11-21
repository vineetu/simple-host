package mcp

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindToolsRanking(t *testing.T) {
	s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}}))
	cases := []struct{ query, want string }{
		{"put my site on my own domain", "connect_domain"},
		{"password protect my site", "set_site_passcode"},
		{"undo my last change", "rollback_site"},
		{"how many visitors this week", "site_analytics"},
		{"webhook when someone books", "list_notifications"},
		{"bring back a deleted site", "restore_site"},
		{"who booked this appointment", "storage_visitor_emails"},
		{"add photos of the house to my listing", "create_upload_link"},
		// Operation synonyms must still beat shared site/file nouns.
		{"erase my website", "delete_site"},
		{"remove an uploaded picture", "storage_delete_file"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			if got := s.search.rank(tc.query, 5); len(got) == 0 || got[0].tool.Name != tc.want {
				t.Fatalf("want %s first", tc.want)
			}
		})
	}
	for _, tool := range s.tools {
		if got := s.search.rank(tool.Name, 1); len(got) != 1 || got[0].tool.Name != tool.Name {
			t.Errorf("exact name %s did not rank first", tool.Name)
		}
	}
}

func TestFindToolsResults(t *testing.T) {
	s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}}))
	for _, query := range []string{"", "connect_domain"} {
		text, structured, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": query}), nil, true))
		if isErr {
			t.Fatal(text)
		}
		if err := ValidateOutput(s.byName["find_tools"].OutputSchema, structured, nil); err != nil {
			t.Fatal(err)
		}
		items := structured["tools"].([]any)
		if query == "" {
			// The empty query is the catalog of every tool not already listed.
			if len(items) == 0 {
				t.Fatalf("catalog has %d tools", len(items))
			}
			continue
		}
		if item := items[0].(map[string]any); item["name"] != query || item["arguments"] == nil {
			t.Fatalf("bad match: %v", item)
		}
	}
	for _, limit := range []any{0, 11, "5"} {
		if _, _, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "site", "limit": limit}), nil, true)); !isErr {
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
		{"copy", "copied"},
		{"expire", "expiry"},
	} {
		if base, inflected := searchTerms(pair[0]), searchTerms(pair[1]); !reflect.DeepEqual(base, inflected) {
			t.Errorf("%q stems to %v; %q stems to %v", pair[0], base, pair[1], inflected)
		}
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
	before := len(up.requests)
	for _, args := range []map[string]any{
		{"name": "run_tool"},
		{"name": "find_tools"},
		{"name": "rename_sit"},
		{"name": "list_versions", "arguments": []any{}},
		{"name": "list_versions", "arguments": map[string]any{"site": "blog", "extra": true}},
	} {
		if _, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", args), nil, true)); !isErr {
			t.Errorf("%v accepted", args)
		}
	}
	if len(up.requests) != before {
		t.Error("a refused run_tool call reached REST")
	}
}

func TestDiscoveryCallerInventories(t *testing.T) {
	s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}}))
	caller := Caller{APIKey: "test", DeployOnly: true}
	if _, _, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "remove_notification"}}, caller, false)); !isErr {
		t.Fatal("deploy key reached a notification tool through run_tool")
	}
	_, structured, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "find_tools", "arguments": map[string]any{"query": "webhook when someone books"}}, caller, false))
	if isErr {
		t.Fatal("deploy search failed")
	}
	for _, raw := range structured["tools"].([]any) {
		if name := raw.(map[string]any)["name"]; name == "list_notifications" || name == "remove_notification" {
			t.Error("deploy search leaked notifications")
		}
	}
	// A Simple Hack connection lists its tools directly: no run_tool or find_tools.
	eventsCaller := Caller{Mode: CallerModeEvents, APIKey: "test"}
	_, _, byName := s.forCaller(eventsCaller)
	for _, name := range []string{"run_tool", "find_tools"} {
		if _, ok := byName[name]; ok {
			t.Errorf("Hack has %s", name)
		}
		eventErrorCode(t, sendEvent(t, s, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"name": "list_notifications", "query": "webhook"}}, eventsCaller, false), codeInvalidParams)
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

// Instructions must stay under the client limit on every instance shape,
// including the longest realistic hostname, with every placeholder filled.
func TestInstructionsLength(t *testing.T) {
	defer NewServer(testConfig(Config{}))
	// The longest legal instance host.
	longHost := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"hosted", Config{}},
		{"long hostname", Config{SelfHosted: true, Rewrite: productionHostRewrite(longHost)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := NewServer(testConfig(tc.cfg)).instructionsFor(Caller{})
			if n := len([]rune(text)); n >= 2000 {
				t.Fatalf("instructions too long: %d", n)
			}
			if strings.Contains(text, "{claude_reconnect}") || strings.Contains(text, "{deleted_retention}") {
				t.Error("unfilled placeholder")
			}
		})
	}
}

// A signed-out connection is a tool error that tells the person how to
// reconnect, and a self-hosted instance never points them at simple-host.app.
func TestSignedOutReconnect(t *testing.T) {
	defer NewServer(testConfig(Config{}))
	up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/me": func() (int, string) { return 401, `{"error":"connection expired","code":"key_expired"}` }}}
	s := NewServer(testConfig(Config{Upstream: up, SelfHosted: true, Rewrite: strings.NewReplacer("simple-host.app", "box.example").Replace}))
	text, _, isErr := resultOf(t, send(t, s, toolCall("who_am_i", nil), nil, true))
	if !isErr || strings.Contains(text, "simple-host.app") || !strings.Contains(text, "https://box.example/mcp") {
		t.Errorf("wrong self reconnect: %s", text)
	}
}
