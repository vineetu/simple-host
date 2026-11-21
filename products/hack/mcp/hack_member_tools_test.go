package hackmcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/vsriram/simple-host/host/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackMemberToolsForwardREST(t *testing.T) {
	cases := []struct {
		name, method, path string
		args               map[string]any
		body               map[string]any
	}{
		{"hack_ask_to_join", "POST", "/v1/hack/events/spring/teams/alpha/requests", map[string]any{"slug": "spring", "team": "alpha"}, nil},
		{"hack_set_looking", "PUT", "/v1/hack/events/spring/me/looking", map[string]any{"slug": "spring", "looking": true, "intro": "I design"}, map[string]any{"looking": true, "intro": "I design"}},
		{"hack_join_event", "POST", "/v1/hack/join/JOINCODE", map[string]any{"code": "JOINCODE", "accept_coc": true, "display_name": "Ada", "answers": map[string]any{"q1": "yes"}}, map[string]any{"accept_coc": true, "display_name": "Ada", "answers": map[string]any{"q1": "yes"}}},
		{"hack_apply_to_event", "POST", "/v1/hack/events/spring/apply", map[string]any{"slug": "spring", "accept_coc": true, "display_name": "Dewi", "city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman", "answers": map[string]any{"q1": "yes"}}, map[string]any{"accept_coc": true, "display_name": "Dewi", "city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman", "answers": map[string]any{"q1": "yes"}}},
		{"hack_application_status", "GET", "/v1/hack/events/spring", map[string]any{"slug": "Spring"}, nil},
		{"hack_update_entry", "PUT", "/v1/hack/events/spring/entry", map[string]any{
			"slug": "spring", "title": "My app", "tagline": "", "problem": "Finding food trucks is hard",
			"audience": "Hungry commuters", "tools": []any{"Go", "React"}, "maturity": "working", "track": "ai",
		}, map[string]any{
			"title": "My app", "tagline": "", "problem": "Finding food trucks is hard",
			"audience": "Hungry commuters", "tools": []any{"Go", "React"}, "maturity": "working", "track": "ai",
		}},
		{"hack_submit_entry", "POST", "/v1/hack/events/spring/entry/submit", map[string]any{"slug": "spring", "updated_at": "2026-10-01T00:00:00Z"}, map[string]any{"updated_at": "2026-10-01T00:00:00Z"}},
		{"hack_get_my_teams", "GET", "/v1/hack/my-teams", map[string]any{}, nil},
		{"hack_vote", "PUT", "/v1/hack/events/spring/vote", map[string]any{"slug": "spring", "team": "build"}, map[string]any{"team": "build"}},
		{"hack_score_team", "PUT", "/v1/hack/events/spring/judge/scores/team-id", map[string]any{"slug": "spring", "team_id": "team-id", "scores": []any{map[string]any{"criterion_id": "idea", "points": 4}}, "comment": "Nice"}, map[string]any{"scores": []any{map[string]any{"criterion_id": "idea", "points": float64(4)}}, "comment": "Nice"}},
		{"hack_declare_my_conflict", "POST", "/v1/hack/events/spring/conflicts", map[string]any{"slug": "spring", "team_id": "team-id"}, map[string]any{"team_id": "team-id"}},
		{"hack_remove_my_conflict", "DELETE", "/v1/hack/events/spring/conflicts/team-id", map[string]any{"slug": "spring", "team_id": "team-id"}, nil},
		{"hack_leave_event", "POST", "/v1/hack/events/spring/leave", map[string]any{"slug": "spring", "confirm": "spring", "successor_user_id": "person", "close": "end"}, map[string]any{"confirm": "spring", "successor_user_id": "person", "close": "end"}},
	}
	tools := map[string]mcp.Tool{}
	for _, tool := range HackMemberTools() {
		if _, ok := tools[tool.Name]; ok {
			t.Fatalf("duplicate %s", tool.Name)
		}
		tools[tool.Name] = tool
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotKey string
			var gotBody []byte
			up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotKey = r.Method, r.URL.Path, r.Header.Get("X-API-Key")
				gotBody, _ = ioRead(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"rest":"same","role":"participant","judge_id":"caller","submissions":[]}`))
			})
			tool, ok := tools[tc.name]
			if !ok {
				t.Fatal("missing tool")
			}
			c := mcp.NewCall(newTestServer(up), httptest.NewRequest("POST", "/mcp", nil), mcp.Caller{APIKey: "member-key", Mode: mcp.CallerModeEvents, UserID: "caller"})
			out, err := mcp.CallTool(tool, c, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if gotMethod != tc.method || gotPath != tc.path || gotKey != "member-key" {
				t.Fatalf("forwarded %s %s key=%s", gotMethod, gotPath, gotKey)
			}
			if out.Structured["rest"] != "same" || out.Structured["role"] != "participant" {
				t.Fatalf("REST response changed: %v", out.Structured)
			}
			if tc.body == nil {
				if len(gotBody) != 0 {
					t.Fatalf("unexpected body %s", gotBody)
				}
			} else {
				var body map[string]any
				if err := json.Unmarshal(gotBody, &body); err != nil {
					t.Fatal(err)
				}
				want, _ := json.Marshal(tc.body)
				actual, _ := json.Marshal(body)
				if !bytes.Equal(actual, want) {
					t.Fatalf("body %s want %s", actual, want)
				}
			}
		})
	}
}

func ioRead(r *http.Request) ([]byte, error) { return io.ReadAll(r.Body) }

func TestHackMemberRESTErrorsAndScreenshot(t *testing.T) {
	byName := map[string]mcp.Tool{}
	for _, tool := range HackMemberTools() {
		byName[tool.Name] = tool
	}
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/hack/events/spring/entry/screenshot" {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write([]byte("png"))
				return
			}
			raw, _ := ioRead(r)
			if !bytes.Equal(raw, []byte("png")) {
				t.Errorf("screenshot bytes %q", raw)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"saved":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"your application is pending","code":"approval_pending"}`))
	})
	c := mcp.NewCall(newTestServer(up), httptest.NewRequest("POST", "/mcp", nil), mcp.Caller{APIKey: "member-key", Mode: mcp.CallerModeEvents, UserID: "caller"})
	_, err := mcp.CallTool(byName["hack_create_team"], c, map[string]any{"slug": "spring", "name": "No"})
	if err == nil || !strings.Contains(err.Error(), "approval_pending") {
		t.Fatalf("REST permission error changed: %v", err)
	}
	got, err := mcp.CallTool(byName["hack_get_entry_screenshot"], c, map[string]any{"slug": "spring"})
	if err != nil || got.Structured["base64"] != base64.StdEncoding.EncodeToString([]byte("png")) || got.Structured["content_type"] != "image/png" {
		t.Fatalf("screenshot read: %v %v", got, err)
	}
	got, err = mcp.CallTool(byName["hack_set_entry_screenshot"], c, map[string]any{"slug": "spring", "base64": base64.StdEncoding.EncodeToString([]byte("png"))})
	if err != nil || got.Structured["saved"] != true {
		t.Fatalf("screenshot upload: %v %v", got, err)
	}
	_, err = mcp.CallTool(byName["hack_set_entry_screenshot"], c, map[string]any{"slug": "spring", "base64": "bad!"})
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("invalid screenshot: %v", err)
	}
}
