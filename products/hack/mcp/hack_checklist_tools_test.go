package hackmcp

import (
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// The checklist tools send the right route and body; REST/MCP parity on real
// data is servertest's TestChecklistDatabaseReplayParity.
func TestChecklistMCPRoutesAndSchemas(t *testing.T) {
	data := []byte(`{"can":{"manage":true,"judge":true,"take_part":false},"checklist":{"role":"organiser","stage":"draft","map_version":1,"items":[{"id":"o.entry-rules","label":"Choose what teams hand in","state":"todo","optional":false,"can_dismiss":false,"origin_stage":"draft","group":"current","actionable":true,"target_id":"o.entry-rules","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"detail":"","count":null,"total":null,"settled":false,"blocked_reason":null}],"next":{"kind":"link","item_id":"o.entry-rules","target_id":"o.entry-rules","label":"Choose what teams hand in","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"confirmation_required":false,"blocked_reason":null}}}`)
	for _, tc := range []struct {
		name, method, path string
		args               map[string]any
		body               string
	}{
		{"hack_get_checklist", "GET", "/v1/hack/events/spring/checklist", map[string]any{"slug": "spring"}, ""},
		{"hack_get_checklist", "GET", "/v1/hack/events/spring/checklist?role=judge", map[string]any{"slug": "spring", "role": "judge"}, ""},
		{"hack_set_checklist_not_needed", "POST", "/v1/hack/events/spring/checklist/not-needed/o.faq", map[string]any{"slug": "spring", "item_id": "o.faq", "not_needed": true}, ""},
		{"hack_set_checklist_not_needed", "DELETE", "/v1/hack/events/spring/checklist/not-needed/o.faq", map[string]any{"slug": "spring", "item_id": "o.faq", "not_needed": false}, ""},
		{"hack_acknowledge_checklist_item", "POST", "/v1/hack/events/spring/checklist/acknowledgements/j.rubric", map[string]any{"slug": "spring", "item_id": "j.rubric", "version": "current"}, `{"version":"current"}`},
		{"hack_acknowledge_checklist_item", "POST", "/v1/hack/events/spring/checklist/acknowledgements/j.conflicts", map[string]any{"slug": "spring", "item_id": "j.conflicts"}, `{}`},
	} {
		t.Run(tc.name+tc.method+tc.path, func(t *testing.T) {
			up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
			// scriptUpstream keys by URL.Path; query values are asserted on the request.
			key := tc.path
			for i, c := range key {
				if c == '?' {
					key = key[:i]
					break
				}
			}
			up.ans[tc.method+" "+key] = func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RequestURI() != tc.path {
					t.Errorf("URI %s", r.URL.RequestURI())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(data)
			}
			server := newTestServer(up)
			text, out, bad := resultOf(t, sendMode(t, server, toolCall(tc.name, tc.args), mcp.CallerModeEvents))
			if bad {
				t.Fatal(text)
			}
			var tool mcp.Tool
			for _, v := range Tools() {
				if v.Name == tc.name {
					tool = v
				}
			}
			if err := mcp.ValidateOutput(tool.OutputSchema, out, nil); err != nil {
				t.Fatal(err)
			}
			recordHackOutputValidated(tc.name)
			if up.last().body != tc.body {
				t.Fatalf("body %q", up.last().body)
			}
		})
	}
}

const hackChecklistFixtureJSON = `{"role":"organiser","stage":"draft","map_version":1,"items":[{"id":"o.entry-rules","label":"Choose what teams hand in","state":"todo","optional":false,"can_dismiss":false,"origin_stage":"draft","group":"current","actionable":true,"target_id":"o.entry-rules","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"detail":"","count":null,"total":null,"settled":false,"blocked_reason":null,"version":"current"}],"next":{"kind":"link","item_id":"o.entry-rules","target_id":"o.entry-rules","label":"Choose what teams hand in","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"confirmation_required":false,"blocked_reason":null,"stage":"open","deadline_passed":false,"cue":null}}`
