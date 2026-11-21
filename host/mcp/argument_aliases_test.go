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
	s := NewServer(testConfig(Config{VisitorTool: func(_ *http.Request, _ Caller, _ string, args map[string]any) (map[string]any, error) {
		gotResource, _ = args["resource"].(string)
		if _, present := args["name"]; present {
			t.Error("alias not removed before reaching the tool")
		}
		return map[string]any{"visitor_data": map[string]any{}, "note": ""}, nil
	}}))

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

// Both the canonical field and the alias, with different values, is refused
// by name rather than silently preferring one of them.
func TestArgumentAliasConflictNamesCanonicalField(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_put_kv", map[string]any{"site": "blog", "name": "settings", "resource": "other", "key": "feature", "value": true}), nil, true))
	if !isErr {
		t.Fatalf("conflict does not name the canonical field: %s", text)
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
	for _, tool := range Tools() {
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
