package hackmcp

import (
	"flag"
	"fmt"
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
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
// Most of Tools() already gets a real-output strict check somewhere:
// the base event/entry tools in TestHackToolDispatch, every
// hackRouteTool-backed organiser tool in TestHackOrganiserToolRoutes,
// hack_set_entry_excluded in TestHackSetEntryExcludedTool, a good number of
// organiser/member tools via hack_results_test.go's assertHackResult, the
// member tools in hack_member_tools_test.go; the app-only twins and show
// tools in the screens tests (hack_app_only_tools_test.go,
// hack_apps_*_test.go). What's left -- the two
// website-mode tool families -- is covered below.
//
// That list is honest, not just asserted: every one of those call sites
// (old and new) now also calls recordHackOutputValidated(name) right after
// its ValidateOutput succeeds, into the package-level hackOutputCoverage
// set. TestMain fails a full (unfiltered) run of this package the moment
// any name in Tools() was never recorded -- enforced, not a hand-kept
// name list that could silently drift from what actually gets exercised.
// A filtered run (go test -run ...) skips the check, since it is only ever
// exercising a subset on purpose.

var (
	hackOutputCoverageMu sync.Mutex
	hackOutputCoverage   = map[string]bool{}
)

// recordHackOutputValidated marks name (a Tools() tool) as having had
// its real output checked against its own outputSchema with ValidateOutput,
// somewhere in this package's tests. See uncoveredHackTools/TestMain.
func recordHackOutputValidated(name string) {
	hackOutputCoverageMu.Lock()
	hackOutputCoverage[name] = true
	hackOutputCoverageMu.Unlock()
}

// uncoveredTools lists, sorted, every Tools() name and every app-only tool
// that was never passed to recordHackOutputValidated during this test run.
func uncoveredTools() []string {
	hackOutputCoverageMu.Lock()
	defer hackOutputCoverageMu.Unlock()
	var missing []string
	base := Tools()
	// The app-only family (hack_apps.go) is built per tool server from the
	// base tools; its screens tests record it the same way.
	appOnly := hackApps(mcp.AppContext{}).AppFamily(base).Tools
	for _, tool := range append(append([]mcp.Tool{}, base...), appOnly...) {
		if !hackOutputCoverage[tool.Name] {
			missing = append(missing, tool.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// TestMain enforces full hack output-schema coverage (SIM-384) after every
// other test in this package has run, but only for a full, unfiltered run:
// `go test ./products/hack/mcp/...` (what CI and the DB-integration script
// both do) must exercise and record every Tools() name, or it fails here,
// naming exactly which tool was missed. `go test -run ...` (chasing one
// test while debugging) only ever exercises a deliberate subset, so the
// check is skipped whenever -run is set.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if runFlag := flag.Lookup("test.run"); runFlag == nil || runFlag.Value.String() == "" {
			if missing := uncoveredTools(); len(missing) > 0 {
				fmt.Fprintf(os.Stderr, "SIM-384: these Tools() names were never validated by a strict ValidateOutput call in this package's tests: %s\n", missing)
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
	rec := sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcp.CallerModeEvents)
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
	authored := toolNamed(t, "hack_create_event").OutputSchema
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
	byName := map[string]mcp.Tool{}
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
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), mcp.CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := mcp.ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
			recordHackOutputValidated(tc.name)
		})
	}
}

// TestHackWebsiteModeToolsOutputMatchesSchema covers famSites' tool
// (hack_web_tools.go): hack_set_event_website_mode. Same reasoning as the test above: the schema
// is the open generic one, so this is mainly a tripwire for a future
// structured schema on one of these.
func TestHackWebsiteModeToolsOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"PATCH /v1/hack/events/spring/website": jsonReply(200, `{"mode":"builtin"}`),
	}}
	s := newTestServer(up)
	byName := map[string]mcp.Tool{}
	for _, tool := range hackWebTools() {
		byName[tool.Name] = tool
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"hack_set_event_website_mode", map[string]any{"slug": "spring", "mode": "builtin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatal("tool missing from hackWebTools()")
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), mcp.CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := mcp.ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
			recordHackOutputValidated(tc.name)
		})
	}
}
