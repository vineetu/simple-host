package hackmcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// organiserUpstream plants markers on every field that must never reach the
// model: the join and judge links carry the event's codes, display_name is
// the organiser's own name.
func organiserUpstream() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"open","scoring_open":false,"judging_locked_at":null,"results_published":false,"submission_deadline":null,"time_zone":"UTC"},"organiser":{"join_url":"https://simple-hack.app/join/ORGANISER-SECRET-JOIN","judge_url":"https://simple-hack.app/judge/ORGANISER-SECRET-JUDGE","counts":{"participants":3,"teams":1,"judges":1,"on_no_team":0}},"can":{"manage":true,"judge":true,"take_part":false},"role":"organiser","admin_view":false,"me":{"display_name":"ORGANISER-OWN-NAME"}}`))
	})
	return mux
}

func organiserCaller(handle, appName string) mcp.Caller {
	return mcp.Caller{APIKey: "organiser-key", Mode: mcp.CallerModeEvents, Handle: handle, AppName: appName}
}

// hack_show_organiser opens a view with the event and its counts; the join
// and judge links and the organiser's name reach only the view-only _meta.
func TestHackShowOrganiser(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	c := organiserCaller("alice", "")
	allowedKey := map[string]bool{"slug": true, "title": true, "stage": true, "scoring_open": true, "judging_locked_at": true, "results_published": true, "submission_deadline": true, "time_zone": true, "url": true}
	markers := []string{"ORGANISER-SECRET-JOIN", "ORGANISER-SECRET-JUDGE", "ORGANISER-OWN-NAME"}
	for _, view := range []string{"overview", "people", "judging"} {
		out, err := runShowTool(t, s, c, "hack_show_organiser", map[string]any{"view": view, "slug": "mockjam"})
		if err != nil {
			t.Fatalf("%s: %v", view, err)
		}
		event, _ := out.Structured["event"].(map[string]any)
		organiser, _ := out.Structured["organiser"].(map[string]any)
		counts, _ := organiser["counts"].(map[string]any)
		if out.Structured["view"] != view || event["title"] != "Mock Jam" || counts["participants"] != float64(3) {
			t.Fatalf("%s structured content: %#v", view, out.Structured)
		}
		for key := range event {
			if !allowedKey[key] {
				t.Fatalf("%s: event field %q is outside the whitelist", view, key)
			}
		}
		visible, _ := json.Marshal([]any{out.Text, out.Structured})
		for _, secret := range append([]string{"organiser-key"}, markers...) {
			if strings.Contains(string(visible), secret) {
				t.Fatalf("%s: %q reached the model: %s", view, secret, visible)
			}
		}
		meta, _ := json.Marshal(out.Meta["simple-hack/organiser-view"])
		for _, marker := range markers {
			if !strings.Contains(string(meta), marker) {
				t.Fatalf("%s: %q missing from the view-only meta: %s", view, marker, meta)
			}
		}
	}
	for _, args := range []map[string]any{{"view": "bogus", "slug": "mockjam"}, {"view": "overview"}} {
		if _, err := runShowTool(t, s, c, "hack_show_organiser", args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

// A judge or participant of the same event cannot open the organiser view.
func TestHackShowOrganiserRequiresOrganiserRole(t *testing.T) {
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	for _, role := range []string{"judge", "participant"} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"open"},"role":"` + role + `"}`))
		})
		out, err := runShowTool(t, newTestServer(mux), organiserCaller("alice", ""), "hack_show_organiser", map[string]any{"view": "overview", "slug": "mockjam"})
		if err == nil || out.Structured != nil {
			t.Fatalf("role %q opened the organiser view: %v %#v", role, err, out.Structured)
		}
	}
}
