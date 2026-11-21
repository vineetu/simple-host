package hackmcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// judgeUpstream's about/rules/prizes carry marker text so a test can tell
// "never present" from "present but empty".
func judgeUpstream() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"judging","scoring_open":true,"judging_locked_at":null,"results_published":false,"submission_deadline":null,"time_zone":"UTC","about":"ORGANISER-PROSE-ABOUT","rules":"ORGANISER-PROSE-RULES","prizes":"ORGANISER-PROSE-PRIZES","join_code":"ORGANISER-JOIN-CODE"},"role":"judge","can":{"judge":true}}`))
	})
	return mux
}

func judgeCaller(handle, appName string) mcp.Caller {
	return mcp.Caller{APIKey: "judge-key", Mode: mcp.CallerModeEvents, Handle: handle, AppName: appName}
}

func runShowTool(t *testing.T, s *mcp.Server, caller mcp.Caller, name string, args map[string]any) (mcp.Output, error) {
	t.Helper()
	call := mcp.NewCall(s, httptest.NewRequest("POST", "/mcp", nil), caller).WithUICapable(true)
	return mcp.CallTool(serverTool(t, s, name), call, args)
}

// hack_show_judge opens a view with a whitelisted slice of the event; the
// organiser's prose and the credential never reach the model-visible
// fields, only the view-only _meta.
func TestHackShowJudge(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	s := newTestServer(judgeUpstream())
	c := judgeCaller("alice", "")
	allowedKey := map[string]bool{"slug": true, "title": true, "stage": true, "scoring_open": true, "judging_locked_at": true, "results_published": true, "submission_deadline": true, "time_zone": true}
	markers := []string{"ORGANISER-PROSE-ABOUT", "ORGANISER-PROSE-RULES", "ORGANISER-PROSE-PRIZES"}
	for _, view := range []string{"queue", "score", "rubric"} {
		out, err := runShowTool(t, s, c, "hack_show_judge", map[string]any{"view": view, "slug": "mockjam"})
		if err != nil {
			t.Fatalf("%s: %v", view, err)
		}
		event, _ := out.Structured["event"].(map[string]any)
		if out.Structured["view"] != view || event["scoring_open"] != true {
			t.Fatalf("%s structured content: %#v", view, out.Structured)
		}
		for key := range event {
			if !allowedKey[key] {
				t.Fatalf("%s: event field %q is outside the whitelist", view, key)
			}
		}
		visible, _ := json.Marshal([]any{out.Text, out.Structured})
		for _, secret := range append([]string{"judge-key", "ORGANISER-JOIN-CODE"}, markers...) {
			if strings.Contains(string(visible), secret) {
				t.Fatalf("%s: %q reached the model: %s", view, secret, visible)
			}
		}
		meta, _ := json.Marshal(out.Meta["simple-hack/event-text"])
		for _, marker := range markers {
			if !strings.Contains(string(meta), marker) {
				t.Fatalf("%s: %q missing from the view-only meta: %s", view, marker, meta)
			}
		}
	}
	for _, args := range []map[string]any{{"view": "bogus", "slug": "mockjam"}, {"view": "queue"}} {
		if _, err := runShowTool(t, s, c, "hack_show_judge", args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

// A participant cannot open a judging view, and the refusal carries no
// structured content.
func TestHackShowJudgeRequiresJudgeOrOrganiserRole(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"closed"},"role":"participant"}`))
	})
	s := newTestServer(mux)
	out, err := runShowTool(t, s, judgeCaller("alice", ""), "hack_show_judge", map[string]any{"view": "queue", "slug": "mockjam"})
	if err == nil || out.Structured != nil {
		t.Fatalf("participant opened a judging view: %v %#v", err, out.Structured)
	}
}
