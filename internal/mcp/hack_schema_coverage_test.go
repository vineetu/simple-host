package mcp

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
)

// This file is SIM-384's regression test: hack_create_event once returned
// "Structured content does not match the tool's output schema: data must
// NOT have additional properties" to a client whose cached copy of the
// schema predated judging_ends_at, even though the event was created. The
// actual fix is openOutputSchemaForWire (outputs.go), applied in every
// Tool's MarshalJSON: it opens additionalProperties: false to true on the
// WIRE copy only, so an added field can never again turn a successful call
// into a reported failure for a client holding an older schema. Every test
// in this package still validates against the AUTHORED (closed) schema, so
// a tool that starts returning an undeclared field is still caught here --
// strictness didn't go away, it just moved off the wire.
// TestWireOutputSchemaOpensOnlyOnTheWire is the regression check for that
// mechanism itself: it fails if openOutputSchemaForWire's call site in
// Tool.MarshalJSON is ever removed.
//
// Most of HackTools() already gets a real-output strict check somewhere:
// the base event/entry tools in TestHackToolDispatch, every
// hackRouteTool-backed organiser tool in TestHackOrganiserToolRoutes,
// hack_set_entry_excluded in TestHackSetEntryExcludedTool, a good number of
// organiser/member tools via hack_results_test.go's assertHackResult, the
// member tools in hack_member_tools_test.go, and the app-only twins and show
// tools in hack_app_only_tools_test.go/hack_apps_judge_test.go/
// hack_apps_organiser_test.go. What's left -- the connection tool, the two
// website-mode tool families, the storage/recipe family and
// hack_create_team_key (skipped by its own REST call in
// TestHackOrganiserToolRoutes) -- is covered below.
//
// That list is honest, not just asserted: every one of those call sites
// (old and new) now also calls recordHackOutputValidated(name) right after
// its ValidateOutput succeeds, into the package-level hackOutputCoverage
// set. TestMain fails a full (unfiltered) run of this package the moment
// any name in HackTools() was never recorded -- enforced, not a hand-kept
// name list that could silently drift from what actually gets exercised.
// A filtered run (go test -run ...) skips the check, since it is only ever
// exercising a subset on purpose.

var (
	hackOutputCoverageMu sync.Mutex
	hackOutputCoverage   = map[string]bool{}
)

// recordHackOutputValidated marks name (a HackTools() tool) as having had
// its real output checked against its own outputSchema with ValidateOutput,
// somewhere in this package's tests. See uncoveredHackTools/TestMain.
func recordHackOutputValidated(name string) {
	hackOutputCoverageMu.Lock()
	hackOutputCoverage[name] = true
	hackOutputCoverageMu.Unlock()
}

// uncoveredHackTools lists, sorted, every HackTools() name that was never
// passed to recordHackOutputValidated during this test run.
func uncoveredHackTools() []string {
	hackOutputCoverageMu.Lock()
	defer hackOutputCoverageMu.Unlock()
	var missing []string
	for _, tool := range HackTools() {
		if !hackOutputCoverage[tool.Name] {
			missing = append(missing, tool.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// TestMain enforces full hack output-schema coverage (SIM-384) after every
// other test in this package has run, but only for a full, unfiltered run:
// `go test ./internal/mcp/...` (what CI and the DB-integration script both
// do) must exercise and record every HackTools() name, or it fails here,
// naming exactly which tool was missed. `go test -run ...` (chasing one
// test while debugging) only ever exercises a deliberate subset, so the
// check is skipped whenever -run is set.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if runFlag := flag.Lookup("test.run"); runFlag == nil || runFlag.Value.String() == "" {
			if missing := uncoveredHackTools(); len(missing) > 0 {
				fmt.Fprintf(os.Stderr, "SIM-384: these HackTools() names were never validated by a strict ValidateOutput call in this package's tests: %s\n", missing)
				code = 1
			}
		}
	}
	os.Exit(code)
}

// TestWireOutputSchemaOpensOnlyOnTheWire pins openOutputSchemaForWire's own
// call site (Tool.MarshalJSON, tools.go) directly: without it, every other
// test in this package still passes, because they all validate against
// tool.OutputSchema (the authored, closed map) rather than the marshaled
// tools/list JSON a real client receives. hack_create_event is the tool
// SIM-384 actually broke on, so it is the one checked here, at both the
// root and a nested object (next_step, organiser) -- additionalProperties
// must be false->true at every level on the wire, while the authored
// schema and inputSchema (untouched by this fix; see outputs.go's package
// comment) stay exactly as written.
func TestWireOutputSchemaOpensOnlyOnTheWire(t *testing.T) {
	s := newTestServer(&scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}})
	rec := sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, CallerModeEvents)
	var wire map[string]any
	for _, tool := range listedTools(t, rec) {
		if tool["name"] == "hack_create_event" {
			wire, _ = tool["outputSchema"].(map[string]any)
		}
	}
	if wire == nil {
		t.Fatal("hack_create_event not listed with an outputSchema")
	}
	if wire["additionalProperties"] != true {
		t.Fatalf("wire outputSchema root additionalProperties = %v, want true", wire["additionalProperties"])
	}
	props, _ := wire["properties"].(map[string]any)
	for _, nested := range []string{"next_step", "organiser"} {
		sub, _ := props[nested].(map[string]any)
		if sub == nil {
			t.Fatalf("wire outputSchema missing nested %q", nested)
		}
		if sub["additionalProperties"] != true {
			t.Fatalf("wire outputSchema properties.%s.additionalProperties = %v, want true", nested, sub["additionalProperties"])
		}
	}
	wireInput, _ := func() (map[string]any, bool) {
		for _, tool := range listedTools(t, rec) {
			if tool["name"] == "hack_create_event" {
				in, ok := tool["inputSchema"].(map[string]any)
				return in, ok
			}
		}
		return nil, false
	}()
	if wireInput == nil || wireInput["additionalProperties"] != false {
		t.Fatalf("wire inputSchema additionalProperties = %v, want false (unchanged by this fix)", wireInput["additionalProperties"])
	}

	// The authored copy -- what every ValidateOutput call in this package's
	// tests actually checks against -- must stay closed: the fix only opens
	// what goes out over the wire, never the map tests validate with.
	authored := s.hackByName["hack_create_event"].OutputSchema
	if authored["additionalProperties"] != false {
		t.Fatalf("authored outputSchema root additionalProperties = %v, want false", authored["additionalProperties"])
	}
	authoredProps, _ := authored["properties"].(map[string]any)
	for _, nested := range []string{"next_step", "organiser"} {
		sub, _ := authoredProps[nested].(map[string]any)
		if sub == nil || sub["additionalProperties"] != false {
			t.Fatalf("authored outputSchema properties.%s.additionalProperties = %v, want false", nested, sub["additionalProperties"])
		}
	}
}

func TestHackSelectTeamOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	s := NewServer(Config{
		Upstream: up, APIHost: "simple-host.app", ContentOrigin: "https://sites.simple-host.app",
		SiteBases: []string{"simple-host.app"}, SkillVersion: "9.9.9", ServerName: "simple-host", Version: "9.9.9",
		SelectHackTeam: func(_ context.Context, _ Caller, _ string) error { return nil },
	})
	tool := s.hackByName["hack_select_team"]
	call := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil),
		caller: Caller{APIKey: "personal-key", Mode: CallerModeEvents, GrantID: "grant-1", UserID: "user-1"}}
	out, err := tool.run(call, map[string]any{"team_id": "11111111-1111-4111-8111-111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Structured["team_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("team_id: %v", out.Structured)
	}
	if err := ValidateOutput(tool.OutputSchema, out.Structured, nil); err != nil {
		t.Fatalf("structuredContent does not match outputSchema: %v", err)
	}
	recordHackOutputValidated("hack_select_team")
}

// TestHackWebEventToolsOutputMatchesSchema covers hackWebEventTools' five
// tools (hack_event_tools.go): hack_set_event_icon, hack_clear_event_icon,
// hack_get_public_event, hack_get_preferences, hack_set_preferences. Every
// one of these declares the deliberately open generic schema
// ({"type":"object","additionalProperties":true}), so ValidateOutput can
// never fail here on an added field -- the point of exercising them for
// real anyway is that a future change swapping one to a closed, structured
// schema (the thing that makes this bug class possible at all) trips this
// test the moment its real output stops being a bare JSON object.
func TestHackWebEventToolsOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"PUT /v1/hack/events/spring/icon":    jsonReply(200, `{"icon_url":"https://x/icon.png"}`),
		"DELETE /v1/hack/events/spring/icon": jsonReply(200, `{"icon_url":null}`),
		"GET /v1/hack/events/spring/public":  jsonReply(200, `{"slug":"spring","title":"Spring"}`),
		"GET /v1/hack/preferences":           jsonReply(200, `{"theme":"system"}`),
		"PATCH /v1/hack/preferences":         jsonReply(200, `{"theme":"dark"}`),
	}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range hackWebEventTools() {
		byName[tool.Name] = tool
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"hack_set_event_icon", map[string]any{"slug": "spring", "content_type": "image/png", "image_base64": "AAAA"}},
		{"hack_clear_event_icon", map[string]any{"slug": "spring"}},
		{"hack_get_public_event", map[string]any{"slug": "spring"}},
		{"hack_get_preferences", map[string]any{}},
		{"hack_set_preferences", map[string]any{"theme": "dark"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatal("tool missing from hackWebEventTools()")
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
			recordHackOutputValidated(tc.name)
		})
	}
}

// TestHackWebsiteModeToolsOutputMatchesSchema covers famSites' three tools
// (hack_web_tools.go): hack_get_event_website, hack_publish_event_website,
// hack_set_event_website_mode. Same reasoning as the test above: the schema
// is the open generic one, so this is mainly a tripwire for a future
// structured schema on one of these.
func TestHackWebsiteModeToolsOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"GET /v1/hack/events/spring/website":       jsonReply(200, `{"has_custom_page":true,"mode":"custom"}`),
		"PATCH /v1/hack/events/spring/website":     jsonReply(200, `{"mode":"builtin"}`),
		"PUT /v1/hack/events/spring/website/files": jsonReply(200, `{"active_version":2}`),
	}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range hackWebTools() {
		byName[tool.Name] = tool
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"hack_get_event_website", map[string]any{"slug": "spring"}},
		{"hack_publish_event_website", map[string]any{"slug": "spring", "files": map[string]any{"index.html": "<h1>x</h1>"}}},
		{"hack_set_event_website_mode", map[string]any{"slug": "spring", "mode": "builtin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatal("tool missing from hackWebTools()")
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
			recordHackOutputValidated(tc.name)
		})
	}
}

// TestHackStorageAndRecipeToolsOutputMatchesSchema covers every one of
// hackStorageTools()'s 16 tools: all 15 hack_event_storage_* tools plus
// hack_get_page_recipe. hack_event_storage_*'s outputSchema is the same
// low-risk {"response": <any>, "status": <int>} wrapper the organiser route
// tools use (storage_tools.go's storageToolsFor), and hack_get_page_recipe
// shares its OutputSchema object directly with Host's own get_page_recipe
// (already exhaustively checked by TestEveryToolResultMatchesItsOutputSchema
// and TestOutputSchemasMatchRealResults in internal/handler) -- but SIM-384's
// review round asked for every one exercised and recorded, not a sample, so
// every REST shape (resource, kv, sql, file, link) gets its own call here.
func TestHackStorageAndRecipeToolsOutputMatchesSchema(t *testing.T) {
	const base = "/v1/hack/events/spring/website/storage"
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"GET " + base + "/resources":                    jsonReply(200, `{"resources":[]}`),
		"GET " + base + "/usage":                        jsonReply(200, `{"used_bytes":0,"limit_bytes":1000000,"remaining_bytes":1000000,"breakdown":{"kv_bytes":0,"sqlite_bytes":0,"files_bytes":0}}`),
		"PUT " + base + "/resources/store":              jsonReply(200, `{"name":"store","kind":"kv"}`),
		"DELETE " + base + "/resources/store":           jsonReply(200, `{"deleted":true}`),
		"GET " + base + "/kv/store/keys":                jsonReply(200, `{"keys":["count"]}`),
		"GET " + base + "/kv/store/keys/count":          jsonReply(200, `{"key":"count","value":3}`),
		"PUT " + base + "/kv/store/keys/count":          jsonReply(200, `{"key":"count","value":3}`),
		"DELETE " + base + "/kv/store/keys/count":       jsonReply(200, `{"deleted":true}`),
		"POST " + base + "/sqlite/store/query":          jsonReply(200, `{"columns":["n"],"rows":[[1]]}`),
		"POST " + base + "/sqlite/store/execute":        jsonReply(200, `{"changes":1,"last_insert_id":2}`),
		"POST " + base + "/sqlite/store/schema":         jsonReply(200, `{"changes":0}`),
		"GET " + base + "/files/store/objects":          jsonReply(200, `{"objects":[{"path":"a.txt"}]}`),
		"PUT " + base + "/files/store/objects/a.txt":    jsonReply(200, `{"path":"a.txt"}`),
		"DELETE " + base + "/files/store/objects/a.txt": jsonReply(200, `{"deleted":true}`),
		"POST " + base + "/files/store/download-link":   jsonReply(200, `{"url":"https://simple-host.app/v1/storage-download?token=opaque","expires_in":600}`),
	}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range hackStorageTools() {
		byName[tool.Name] = tool
	}
	if _, ok := byName["hack_get_page_recipe"]; !ok {
		t.Fatal("hack_get_page_recipe missing from hackStorageTools()")
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"hack_event_storage_list_resources", map[string]any{"event": "spring"}},
		{"hack_event_storage_get_usage", map[string]any{"event": "spring"}},
		{"hack_event_storage_set_resource", map[string]any{"event": "spring", "name": "store", "body": map[string]any{"kind": "kv"}}},
		{"hack_event_storage_list_kv_keys", map[string]any{"event": "spring", "name": "store"}},
		{"hack_event_storage_get_kv", map[string]any{"event": "spring", "name": "store", "key": "count"}},
		{"hack_event_storage_put_kv", map[string]any{"event": "spring", "name": "store", "key": "count", "value": 3}},
		{"hack_event_storage_sql_query", map[string]any{"event": "spring", "name": "store", "sql": "SELECT n FROM t"}},
		{"hack_event_storage_sql_execute", map[string]any{"event": "spring", "name": "store", "sql": "UPDATE t SET n=1"}},
		{"hack_event_storage_sql_schema", map[string]any{"event": "spring", "name": "store", "sql": "CREATE TABLE t(n)"}},
		{"hack_event_storage_list_file_objects", map[string]any{"event": "spring", "name": "store"}},
		{"hack_event_storage_put_file", map[string]any{"event": "spring", "name": "store", "path": "a.txt", "content_base64": "aGk=", "content_type": "text/plain"}},
		{"hack_event_storage_file_download_link", map[string]any{"event": "spring", "name": "store", "path": "a.txt"}},
		// Delete calls last: they remove the key/file/resource the reads above depend on.
		{"hack_event_storage_delete_file", map[string]any{"event": "spring", "name": "store", "path": "a.txt"}},
		{"hack_event_storage_delete_kv", map[string]any{"event": "spring", "name": "store", "key": "count"}},
		{"hack_event_storage_delete_resource", map[string]any{"event": "spring", "name": "store"}},
	}
	if len(cases) != 15 {
		t.Fatalf("%d cases, want all 15 hack_event_storage_* tools", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatalf("%s missing from hackStorageTools()", tc.name)
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
			recordHackOutputValidated(tc.name)
		})
	}

	recipeTool := byName["hack_get_page_recipe"]
	recipeCall := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "k", Mode: CallerModeEvents}}
	out, err := recipeTool.run(recipeCall, map[string]any{"topic": "wall"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutput(recipeTool.OutputSchema, out.Structured, nil); err != nil {
		t.Fatalf("hack_get_page_recipe: structuredContent does not match outputSchema: %v", err)
	}
	recordHackOutputValidated("hack_get_page_recipe")
}

// TestHackCreateTeamKeyOutputMatchesSchema covers hack_create_team_key.
// policy.connectorHidden excludes it from s.hackByName entirely (registry.go's
// view()), not just from tools/list, so it is unreachable through a real
// connector call -- the key-creation flow is REST/UI only, by design ("the
// connector never returns one into chat"). It is still a real, registered
// Tool with a run function, so this gets it the same way
// TestHackSetEntryExcludedTool and other hidden-tool tests do: from the raw
// family function, run directly rather than through tools/call dispatch.
// Its outputSchema is the same generic {"response","status"} wrapper every
// other organiser route tool uses.
func TestHackCreateTeamKeyOutputMatchesSchema(t *testing.T) {
	var tool Tool
	for _, candidate := range HackOrganiserTools() {
		if candidate.Name == "hack_create_team_key" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("hack_create_team_key missing from HackOrganiserTools()")
	}
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"POST /v1/hack/events/spring/key": jsonReply(200, `{"key":"shk_test_0123456789abcdef"}`),
	}}
	s := newTestServer(up)
	c := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "user-key", Mode: CallerModeEvents}}
	out, err := tool.run(c, map[string]any{"slug": "spring"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutput(tool.OutputSchema, out.Structured, nil); err != nil {
		t.Fatalf("structuredContent does not match outputSchema: %v", err)
	}
	recordHackOutputValidated("hack_create_team_key")
}
