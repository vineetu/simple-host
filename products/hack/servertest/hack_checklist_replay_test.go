package servertest

import (
	hackmcp "github.com/vsriram/simple-host/products/hack/mcp"
	"reflect"
	"testing"

	"github.com/vsriram/simple-host/internal/mcp"
)

// The replay contract is checked against a real database computation,
// including the state change after an event-wide mark: what the tool returns
// is what the REST route returns for the same organiser and event.
//
// Moved from internal/mcp (D2-S10d): it needs Hack's tables, so it runs
// against the whole program instead of a scripted upstream.
func TestChecklistDatabaseReplayParity(t *testing.T) {
	a := newTeamSiteApp(t)
	organiser := a.newPerson(t, "check-mcp")
	event := a.makeEvent(t, organiser)
	access := a.connect(t, organiser, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	path := "/v1/hack/events/" + event + "/checklist"
	var schema any
	for _, tool := range hackmcp.Tools() {
		if tool.Name == "hack_get_checklist" {
			schema = tool.OutputSchema
		}
	}
	if schema == nil {
		t.Fatal("hack_get_checklist has no output schema")
	}
	var before any
	for _, marked := range []bool{false, true} {
		if marked {
			wantTS(t, "mark not needed", a.api(t, "POST", path+"/not-needed/o.faq", nil, organiser.key), 200, "")
		}
		r := a.api(t, "GET", path, nil, organiser.key)
		if r.status != 200 {
			t.Fatalf("REST checklist: %d %s", r.status, r.body)
		}
		rest := r.json(t)
		text, out, bad := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "hack_get_checklist", "arguments": map[string]any{"slug": event}}))
		if bad {
			t.Fatal(text)
		}
		if !reflect.DeepEqual(out, rest) {
			t.Fatalf("REST/MCP checklist items or next differ (marked=%v):\nREST %v\nMCP  %v", marked, rest, out)
		}
		if err := mcp.ValidateOutput(schema, out, nil); err != nil {
			t.Fatal(err)
		}
		if marked && reflect.DeepEqual(before, rest["checklist"]) {
			t.Fatal("the event-wide mark changed nothing in the checklist")
		}
		before = rest["checklist"]
	}
}
