package servertest

import (
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/internal/mcp"
)

// mcpCallWithKey calls tools/call directly with a personal API key (no OAuth
// grant needed; CallerModeEvents is reached from a key's own scope the same
// way a.mcpWithKey already reaches it for tools/list in hack_connector_test.go).
func (a *teamSiteApp) mcpCallWithKey(t *testing.T, key, name string, args map[string]any) resp {
	t.Helper()
	return a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}), map[string]string{"Content-Type": "application/json", "X-API-Key": key, "MCP-Protocol-Version": "2025-06-18"})
}

// TestHackOutputSchemasMatchRealResults is SIM-384's root-cause check,
// driven against the real app and database rather than internal/mcp's own
// fake-upstream fixtures (TestHackToolDispatch and friends): hack_create_event
// returned "Structured content does not match the tool's output schema: data
// must NOT have additional properties" on 2026-11-06 even though the event
// was created. The cause was never a mismatch in this code -- it was
// judging_ends_at landing in the event output/schema together (commit
// 9e691c58, deployed ~04:50 UTC 2026-11-05) after claude.ai had already
// cached an older copy of hack_create_event's outputSchema (its "organisation"
// input description still reads as the pre-eefc4e4b text, confirming the
// cached copy predates both changes): the client rejected its own cached
// schema's extra, unknown property, not a real tool/schema disagreement.
//
// This test proves that by exercising the real create/read/update/stage/
// rubric/export path end to end and validating every result against each
// tool's AUTHORED outputSchema (mcp.HackTools(), additionalProperties: false
// as written in outputs.go) -- not the wire copy tools/list now advertises,
// which openOutputSchemaForWire (outputs.go) deliberately opens so this
// exact class of client-side rejection can't recur. If a genuine mismatch
// existed anywhere in this path, this strict, in-process check would still
// catch it.
func TestHackOutputSchemasMatchRealResults(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "org")

	schemas := map[string]map[string]any{}
	for _, tool := range mcp.HackTools() {
		schemas[tool.Name] = tool.OutputSchema
	}

	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		text, structured, isErr := toolResultOf(t, a.mcpCallWithKey(t, org.key, name, args))
		if isErr || structured == nil {
			t.Fatalf("%s %v failed: %s", name, args, text)
		}
		schema, ok := schemas[name]
		if !ok {
			t.Fatalf("%s has no authored outputSchema in mcp.HackTools()", name)
		}
		if err := mcp.ValidateOutput(schema, structured, nil); err != nil {
			t.Fatalf("%s %v: structuredContent does not match its authored outputSchema: %v\nstructuredContent: %v", name, args, err, structured)
		}
		return structured
	}

	slug := uniqueSlug()
	t.Cleanup(func() { a.cleanupSlug(slug) })

	if name := call("hack_check_event_name", map[string]any{"slug": slug}); name["available"] != true {
		t.Fatalf("name check: %v", name)
	}
	created := call("hack_create_event", map[string]any{
		"slug": slug, "title": "Schema check", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "SIM-384 real-app schema check", "expected_participants": 5, "starts_at": "2026-12-01", "time_zone": "UTC",
	})
	// A freshly created event has no judging_ends_at yet: the exact field
	// whose addition (while null, still a declared property) is what broke
	// a client validating against a schema recorded before it existed.
	if created["judging_ends_at"] != nil {
		t.Fatalf("fresh event unexpectedly has judging_ends_at: %v", created["judging_ends_at"])
	}
	call("hack_get_event", map[string]any{"slug": slug})
	call("hack_list_events", map[string]any{})
	updated := call("hack_update_event", map[string]any{"slug": slug, "judging_ends_at": "2026-12-05T18:00:00Z"})
	if updated["judging_ends_at"] != "2026-12-05T18:00:00Z" {
		t.Fatalf("judging_ends_at did not round-trip through hack_update_event: %v", updated["judging_ends_at"])
	}
	stage := call("hack_set_event_stage", map[string]any{"slug": slug, "stage": "open"})
	if stage["stage"] != "open" {
		t.Fatalf("stage: %v", stage)
	}
	call("hack_get_rubric", map[string]any{"slug": slug})
	rubric := call("hack_set_rubric", map[string]any{"slug": slug, "criteria": []any{
		map[string]any{"name": "Idea", "weight": 100, "max_points": 10},
	}})
	if len(rubric["criteria"].([]any)) != 1 {
		t.Fatalf("rubric: %v", rubric)
	}
	scores := call("hack_export_scores", map[string]any{"slug": slug})
	if scores["filename"] == "" {
		t.Fatalf("scores export: %v", scores)
	}
	results := call("hack_export_results", map[string]any{"slug": slug})
	if results["filename"] == "" {
		t.Fatalf("results export: %v", results)
	}
}
