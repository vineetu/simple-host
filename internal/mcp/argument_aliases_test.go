package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// storage_* tools call a saved-data resource "name"; visitor_* tools call
// the identical concept "resource". A model that only used one family
// correctly reaches for the same word in the other and must not be refused
// for it, and the alias must never show up as a second property in the
// advertised schema (it would change the tool list some clients cache).
func TestStorageToolAcceptsResourceAliasForName(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"PUT /v1/sites/blog/storage/kv/settings/keys/feature": func() (int, string) { return 200, `{"key":"feature","value":true}` },
	}}
	s := newTestServer(up)
	text, structured, isErr := resultOf(t, send(t, s, toolCall("storage_put_kv", map[string]any{"site": "blog", "resource": "settings", "key": "feature", "value": true}), nil, true))
	if isErr || structured == nil {
		t.Fatalf("resource alias refused: %s", text)
	}
	if req := up.requests[0]; req.URL.Path != "/v1/sites/blog/storage/kv/settings/keys/feature" {
		t.Fatalf("alias did not resolve to name: %s", req.URL.Path)
	}
	if _, ok := s.byName["storage_put_kv"].InputSchema["properties"].(map[string]any)["resource"]; ok {
		t.Fatal("alias leaked into the advertised schema")
	}
}

// visitor_* tools' concept is advertised as "resource"; a model that knows
// the storage_* tools' "name" must be accepted too.
func TestVisitorToolAcceptsNameAliasForResource(t *testing.T) {
	var gotResource string
	s := NewServer(Config{VisitorTool: func(_ *http.Request, _ Caller, _ string, args map[string]any) (map[string]any, error) {
		gotResource, _ = args["resource"].(string)
		if _, present := args["name"]; present {
			t.Error("alias not removed before reaching the tool")
		}
		return map[string]any{"visitor_data": map[string]any{}, "note": ""}, nil
	}})
	text, _, isErr := resultOf(t, send(t, s, toolCall("visitor_read_site", map[string]any{"address": "site.example", "name": "wall"}), nil, true))
	if isErr {
		t.Fatalf("name alias refused: %s", text)
	}
	if gotResource != "wall" {
		t.Fatalf("resource = %q", gotResource)
	}
	if _, ok := s.byName["visitor_read_site"].InputSchema["properties"].(map[string]any)["name"]; ok {
		t.Fatal("alias leaked into the advertised schema")
	}
}

// find_tools' argument is "query"; a model guessing "task" (a word other
// search-style tools use) is accepted the same way.
func TestFindToolsAcceptsTaskAliasForQuery(t *testing.T) {
	s := NewServer(Config{})
	text, structured, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"task": "put my site on my own domain"}), nil, true))
	if isErr {
		t.Fatalf("task alias refused: %s", text)
	}
	tools, _ := structured["tools"].([]any)
	found := false
	for _, raw := range tools {
		if raw.(map[string]any)["name"] == "connect_domain" {
			found = true
		}
	}
	if !found {
		t.Fatalf("task alias did not reach the search: %v", structured)
	}
}

// get_page_recipe's argument is "topic"; a model guessing "kind" (the word
// the storage tools use for kv/sqlite/files) is accepted the same way, for
// both the Host and the Hack copy of the tool.
func TestGetPageRecipeAcceptsKindAliasForTopic(t *testing.T) {
	for _, tc := range []struct {
		tool   string
		caller Caller
	}{
		{"get_page_recipe", Caller{APIKey: "test"}},
		{"hack_get_page_recipe", Caller{APIKey: "test", Mode: CallerModeEvents}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			s := NewServer(Config{Events: &fakeEventsBackend{}})
			text, structured, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": tc.tool, "arguments": map[string]any{"kind": "inbox"}}, tc.caller, false))
			if isErr {
				t.Fatalf("kind alias refused: %s", text)
			}
			if structured["topic"] != "inbox" {
				t.Fatalf("topic = %v", structured["topic"])
			}
		})
	}
}

// Both the canonical field and the alias, with different values, is refused
// by name rather than silently preferring one of them.
func TestArgumentAliasConflictNamesCanonicalField(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_put_kv", map[string]any{"site": "blog", "name": "settings", "resource": "other", "key": "feature", "value": true}), nil, true))
	if !isErr || !strings.Contains(text, "both given with different values") || !strings.Contains(text, "use name") {
		t.Fatalf("conflict does not name the canonical field: %s", text)
	}
}

// Both given with the same value is a harmless duplicate, not a conflict.
func TestArgumentAliasAgreeingValuesAreAccepted(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/storage/kv/settings/keys/feature": func() (int, string) { return 200, `{"key":"feature","value":1}` },
	}}
	s := newTestServer(up)
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_get_kv", map[string]any{"site": "blog", "name": "settings", "resource": "settings", "key": "feature"}), nil, true))
	if isErr {
		t.Fatalf("agreeing alias and canonical refused: %s", text)
	}
}

// storage_get_usage has no resource/name concept at all, so the alias must
// not invent one: an unrelated "resource" argument stays an ordinary
// unexpected-argument refusal that names the tool's real fields.
func TestArgumentAliasDoesNotApplyWithoutTheCanonicalField(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_get_usage", map[string]any{"site": "blog", "resource": "settings"}), nil, true))
	if !isErr || !strings.Contains(text, `unexpected argument "resource"`) || !strings.Contains(text, "site") {
		t.Fatalf("unrelated resource argument not refused plainly: %s", text)
	}
}

// A near-miss that is not a known alias still gets refused, but the message
// now names the fields the tool actually takes instead of only pointing at
// a possibly-stale tool list.
func TestUnexpectedArgumentNamesTheExpectedFields(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_put_kv", map[string]any{"site": "blog", "key": "feature", "value": true, "title": "settings"}), nil, true))
	if !isErr || !strings.Contains(text, `unexpected argument "title"`) || !strings.Contains(text, "key") || !strings.Contains(text, "name") || !strings.Contains(text, "site") {
		t.Fatalf("expected fields not named: %s", text)
	}
}

// A tool that genuinely takes both words as distinct arguments (unlike any
// real tool today) must keep both meanings: the alias guard is driven by
// the schema, not by the tool's name family.
func TestArgumentAliasSkipsWhenAliasIsItsOwnDeclaredArgument(t *testing.T) {
	s := NewServer(Config{})
	var gotArgs map[string]any
	s.byName["storage_test_dual"] = Tool{
		Name: "storage_test_dual",
		InputSchema: object(map[string]any{
			"site":     str(""),
			"name":     str(""),
			"resource": str(""),
		}, "site"),
		run: func(_ *call, args map[string]any) (output, error) {
			gotArgs = args
			return output{Text: "ok"}, nil
		},
	}
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_test_dual", map[string]any{"site": "blog", "name": "n", "resource": "r"}), nil, true))
	if isErr {
		t.Fatalf("both-declared call refused: %s", text)
	}
	if gotArgs["name"] != "n" || gotArgs["resource"] != "r" {
		t.Fatalf("args changed when both are real, distinct arguments: %v", gotArgs)
	}
	text, _, isErr = resultOf(t, send(t, s, toolCall("storage_test_dual", map[string]any{"site": "blog", "resource": "r"}), nil, true))
	if isErr {
		t.Fatalf("resource-only call refused: %s", text)
	}
	if _, present := gotArgs["name"]; present {
		t.Fatalf("alias filled in the declared name field: %v", gotArgs)
	}
	if gotArgs["resource"] != "r" {
		t.Fatalf("resource argument lost: %v", gotArgs)
	}
}

// The alias also works dispatched indirectly through run_tool, the path an
// app with a trimmed core tool list uses for anything not in its core.
func TestStorageAliasAcceptedThroughRunTool(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"PUT /v1/sites/blog/storage/kv/settings/keys/feature": func() (int, string) { return 200, `{"key":"feature","value":true}` },
	}}
	s := newTestServer(up)
	text, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", map[string]any{"name": "storage_put_kv", "arguments": map[string]any{"site": "blog", "resource": "settings", "key": "feature", "value": true}}), nil, true))
	if isErr {
		t.Fatalf("resource alias refused through run_tool: %s", text)
	}
	if req := up.requests[0]; req.URL.Path != "/v1/sites/blog/storage/kv/settings/keys/feature" {
		t.Fatalf("alias did not resolve to name through run_tool: %s", req.URL.Path)
	}
}

// hack_event_storage_* tools share the storage_* builder (storageToolsFor),
// so the alias must work on the organiser's event-website copies too.
func TestHackEventStorageAliasAcceptsResourceForName(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"PUT /v1/hack/events/weekend/website/storage/kv/settings/keys/feature": func() (int, string) { return 200, `{"key":"feature","value":true}` },
	}}
	s := NewServer(Config{Events: &fakeEventsBackend{}, Upstream: up, APIHost: "simple-hack.app"})
	caller := Caller{APIKey: "test", Mode: CallerModeEvents}
	text, _, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "hack_event_storage_put_kv", "arguments": map[string]any{"event": "weekend", "resource": "settings", "key": "feature", "value": true}}, caller, false))
	if isErr {
		t.Fatalf("resource alias refused for hack_event_storage_put_kv: %s", text)
	}
	if req := up.requests[0]; req.URL.Path != "/v1/hack/events/weekend/website/storage/kv/settings/keys/feature" {
		t.Fatalf("alias did not resolve to name: %s", req.URL.Path)
	}
}

// Aliasing operates on the top-level arguments map only: a caller-supplied
// JSON value that happens to contain keys named "name" or "resource" is
// opaque data to the server and must reach the REST call byte-for-byte.
func TestArgumentAliasLeavesNestedValuesUntouched(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"PUT /v1/sites/blog/storage/kv/settings/keys/feature": func() (int, string) { return 200, `{"key":"feature","value":null}` },
	}}
	s := newTestServer(up)
	nested := map[string]any{"name": "inner-n", "resource": "inner-r"}
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_put_kv", map[string]any{"site": "blog", "resource": "settings", "key": "feature", "value": nested}), nil, true))
	if isErr {
		t.Fatalf("call refused: %s", text)
	}
	if len(up.bodies) == 0 || !strings.Contains(up.bodies[0], `"name":"inner-n"`) || !strings.Contains(up.bodies[0], `"resource":"inner-r"`) {
		t.Fatalf("nested object changed: %v", up.bodies)
	}
}

// Cheap, broad regression check: across the whole real tool inventory (not
// a synthetic tool), no tool's advertised schema has grown the alias
// property it is given for. If it had, the alias would have become a
// second real argument instead of a silently-accepted guess, changing the
// frozen schema some clients cache.
func TestArgumentAliasNeverAppearsInAnyAdvertisedSchema(t *testing.T) {
	for _, tool := range append(append([]Tool{}, Tools()...), HackTools()...) {
		canonical, alias := argAliasFor(tool.Name)
		if canonical == "" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		if _, ok := props[alias]; ok {
			t.Errorf("%s advertises %q, which is also this family's alias for %q", tool.Name, alias, canonical)
		}
	}
}
