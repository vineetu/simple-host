package hackmcp

import (
	"encoding/json"
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// appOnlyMarkers is every secret/personal-data marker the fixture upstream
// below plants in a REST response that an app-only twin wraps: an email,
// a free-text answer, a city, an organiser-written question, a rubric
// criterion name, a judge/team display name, a former judge's name, an
// event join/judge URL and code, a regenerated link, a co-organiser
// invite link, a judge's own entry title and scoring comment. Every twin
// test below asserts none of these ever appear in out.Text or the JSON
// encoding of out.Structured (the two model-visible fields), and that the
// ones relevant to each specific twin DO appear in out.Meta (proving the
// data reached the view, just not the model).
var appOnlyMarkers = []string{
	"MARKER-EMAIL-1", "MARKER-EMAIL-2", "MARKER-ANSWER", "MARKER-CITY",
	"MARKER-QUESTION", "MARKER-CRITERION", "MARKER-JUDGE-NAME", "MARKER-TEAM-NAME",
	"MARKER-FORMER-JUDGE", "MARKER-JOIN-URL", "MARKER-JUDGE-URL", "MARKER-REGEN-URL",
	"MARKER-INVITE-URL", "MARKER-ENTRY-TITLE", "MARKER-SCORE-COMMENT",
}

func appOnlyUpstream() *http.ServeMux {
	mux := http.NewServeMux()
	const resultRow = `{"team_id":"t1","team_name":"MARKER-TEAM-NAME","total":85,"rank":1,"tied":false,"judges_scored":2,"raw_total":85,"normalised_total":85,"deciding_mode":"raw","tie_decider":"score","track":null,"track_rank":null,"track_winner":false}`
	mux.HandleFunc("GET /v1/hack/events/mockjam/judging/preview", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[` + resultRow + `],"deciding_mode":"raw"}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/results", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[` + resultRow + `],"full_ranking":true}`))
	})
	mux.HandleFunc("POST /v1/hack/events/mockjam/results/publish", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[` + resultRow + `],"full_ranking":true}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/people", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"user_id":"u1","email":"MARKER-EMAIL-1","display_name":"MARKER-TEAM-NAME","role":"participant"}]`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/applications", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"user_id":"u2","email":"MARKER-EMAIL-2","display_name":"MARKER-JUDGE-NAME","status":"pending","city":"MARKER-CITY","answers":[{"id":"q1","prompt":"MARKER-QUESTION","answer":"MARKER-ANSWER"}]}]`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/registration", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"questions":[{"id":"q1","prompt":"MARKER-QUESTION","required":false}],"approval_required":true,"applications_open":true}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/rubric", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"criteria":[{"id":"c1","name":"MARKER-CRITERION","description":"d","weight":100,"max_points":5}]}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/judging/settings", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"assignment_mode":"open","judges_per_team":2,"score_mode":"raw","tie_criterion_id":"c1","public_scores":false,"public_ranks":false}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/conflicts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"conflicts":[{"judge_id":"j1","judge_name":"MARKER-JUDGE-NAME","team_id":"t1","team_name":"MARKER-TEAM-NAME"}]}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/teams", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"teams":[{"id":"t1","name":"MARKER-TEAM-NAME","members":[{"user_id":"u1","email":"MARKER-EMAIL-1","display_name":"MARKER-JUDGE-NAME"}]}],"on_no_team":[]}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/assignments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"assignments":[{"judge_id":"j1","team_id":"t1"}]}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/judging/dashboard", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"teams":[{"team_id":"t1","team_name":"MARKER-TEAM-NAME","judges_scored":0}],"judges":[{"judge_id":"j1","judge_name":"MARKER-JUDGE-NAME","done_count":0,"assigned_count":1}],"former_judges":[{"user_id":"u3","display_name":"MARKER-FORMER-JUDGE","counted_teams":1,"assigned_count":1}]}`))
	})
	mux.HandleFunc("POST /v1/hack/events/mockjam/stage", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"open","stages_offered":["open"]},"role":"organiser","organiser":{"join_code":"jc","join_url":"https://x/MARKER-JOIN-URL","judge_code":"gc","judge_url":"https://x/MARKER-JUDGE-URL"}}`))
	})
	mux.HandleFunc("PATCH /v1/hack/events/mockjam", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"event":{"slug":"mockjam","title":"Mock Jam","stage":"judging","stages_offered":["judging"],"judging_ends_at":"2026-01-01T00:00:00Z"},"role":"organiser","organiser":{"join_code":"jc","join_url":"https://x/MARKER-JOIN-URL","judge_code":"gc","judge_url":"https://x/MARKER-JUDGE-URL"}}`))
	})
	mux.HandleFunc("POST /v1/hack/events/mockjam/codes/join", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"newcode","url":"https://x/MARKER-REGEN-URL"}`))
	})
	mux.HandleFunc("POST /v1/hack/events/mockjam/organiser-invite", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"https://x/MARKER-INVITE-URL","expires_at":"2026-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/judge/queue", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"queue":[{"team_id":"t1","title":"MARKER-ENTRY-TITLE"}]}`))
	})
	mux.HandleFunc("GET /v1/hack/events/mockjam/judge/scores/t1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"scores":[{"criterion_id":"c1","points":3}],"comment":"MARKER-SCORE-COMMENT"}`))
	})
	return mux
}

// Owner "j1" matches the fixture conflict's judge_id below, so hack_app_
// get_my_conflicts' own minimisation (hack_get_my_conflicts keeps only
// the caller's OWN conflicts, mcp.Caller.accountUserID()) does not filter the
// fixture row out before this test can assert on it.
func appOnlyCaller() mcp.Caller {
	return mcp.Caller{APIKey: "app-only-key", Mode: mcp.CallerModeEvents, Handle: "alice", Owner: "j1"}
}

func runAppOnlyTool(t *testing.T, s *mcp.Server, name string, args map[string]any) mcp.Output {
	t.Helper()
	tool, ok := findServerTool(s, name)
	if !ok {
		t.Fatalf("%s: not a registered tool", name)
	}
	call := mcp.NewCall(s, httptest.NewRequest("POST", "/mcp", nil), appOnlyCaller()).WithUICapable(true)
	out, err := mcp.CallTool(tool, call, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// Every twin a screen calls keeps personal text, emails, links and codes out
// of content and structuredContent; they reach the view only through _meta.
func TestHackAppOnlyTwinsContentCarriesNoPersonalData(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(appOnlyUpstream())
	cases := []struct {
		tool       string
		args       map[string]any
		wantInMeta []string
	}{
		{"hack_app_preview_results", map[string]any{"slug": "mockjam"}, []string{"MARKER-TEAM-NAME"}},
		{"hack_app_get_results", map[string]any{"slug": "mockjam"}, []string{"MARKER-TEAM-NAME"}},
		{"hack_app_publish_results", map[string]any{"slug": "mockjam", "body": map[string]any{"rank_overrides": []any{map[string]any{"team_id": "t1", "rank": 1}}}}, []string{"MARKER-TEAM-NAME"}},
		{"hack_app_get_people", map[string]any{"slug": "mockjam"}, []string{"MARKER-EMAIL-1", "MARKER-TEAM-NAME"}},
		{"hack_app_get_applications", map[string]any{"slug": "mockjam"}, []string{"MARKER-EMAIL-2", "MARKER-JUDGE-NAME", "MARKER-CITY", "MARKER-QUESTION", "MARKER-ANSWER"}},
		{"hack_app_get_registration", map[string]any{"slug": "mockjam"}, []string{"MARKER-QUESTION"}},
		{"hack_app_get_rubric", map[string]any{"slug": "mockjam"}, []string{"MARKER-CRITERION"}},
		{"hack_app_get_judging_settings", map[string]any{"slug": "mockjam"}, nil},
		{"hack_app_get_conflicts", map[string]any{"slug": "mockjam"}, []string{"MARKER-JUDGE-NAME", "MARKER-TEAM-NAME"}},
		{"hack_app_get_assignments", map[string]any{"slug": "mockjam"}, nil},
		// Emails are omitted here by hack_get_teams' own existing
		// minimisation unless include_emails is passed (hack_results.go's
		// minimiseHackOrganiserResult) -- unrelated to app-only twinning,
		// so MARKER-EMAIL-1 is correctly absent from both content and meta.
		{"hack_app_get_teams", map[string]any{"slug": "mockjam"}, []string{"MARKER-TEAM-NAME", "MARKER-JUDGE-NAME"}},
		{"hack_app_get_judging_dashboard", map[string]any{"slug": "mockjam"}, []string{"MARKER-TEAM-NAME", "MARKER-JUDGE-NAME", "MARKER-FORMER-JUDGE"}},
		{"hack_app_set_event_stage", map[string]any{"slug": "mockjam", "stage": "open"}, []string{"MARKER-JOIN-URL", "MARKER-JUDGE-URL"}},
		{"hack_app_set_judging_ends_at", map[string]any{"slug": "mockjam", "judging_ends_at": "2026-01-01T00:00:00Z"}, []string{"MARKER-JOIN-URL", "MARKER-JUDGE-URL"}},
		{"hack_app_regenerate_code", map[string]any{"slug": "mockjam", "kind": "join"}, []string{"MARKER-REGEN-URL"}},
		{"hack_app_create_organiser_invite", map[string]any{"slug": "mockjam"}, []string{"MARKER-INVITE-URL"}},
		{"hack_app_get_judge_queue", map[string]any{"slug": "mockjam"}, []string{"MARKER-ENTRY-TITLE"}},
		{"hack_app_get_judge_scores", map[string]any{"slug": "mockjam", "team_id": "t1"}, []string{"MARKER-SCORE-COMMENT"}},
		{"hack_app_get_my_conflicts", map[string]any{"slug": "mockjam"}, []string{"MARKER-JUDGE-NAME", "MARKER-TEAM-NAME"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			out := runAppOnlyTool(t, s, tc.tool, tc.args)
			if out.Text == "" {
				t.Fatalf("%s: empty text summary", tc.tool)
			}
			structuredJSON, err := json.Marshal(out.Structured)
			if err != nil {
				t.Fatalf("%s: marshal structured: %v", tc.tool, err)
			}
			for _, marker := range appOnlyMarkers {
				if strings.Contains(out.Text, marker) {
					t.Errorf("%s: marker %q leaked into model-visible Text: %q", tc.tool, marker, out.Text)
				}
				if strings.Contains(string(structuredJSON), marker) {
					t.Errorf("%s: marker %q leaked into model-visible Structured: %s", tc.tool, marker, structuredJSON)
				}
			}
			metaJSON, err := json.Marshal(out.Meta)
			if err != nil {
				t.Fatalf("%s: marshal meta: %v", tc.tool, err)
			}
			for _, marker := range tc.wantInMeta {
				if !strings.Contains(string(metaJSON), marker) {
					t.Errorf("%s: marker %q missing from out.Meta (the view-only channel): %s", tc.tool, marker, metaJSON)
				}
			}
		})
	}
}

// TestHackAppOnlyTwinsRefuseWithoutUIDeclared mirrors hack_show_judge/
// hack_show_organiser's own call-time gate: a twin has nothing to do for
// a connection with no view to hand _meta to. Both flags on, so the
// refusal is provably about uiClient, not a flag that happens to be off.
func TestHackAppOnlyTwinsRefuseWithoutUIDeclared(t *testing.T) {
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(appOnlyUpstream())
	for _, name := range appOnlyTwinNames() {
		tool := serverTool(t, s, name)
		call := mcp.NewCall(s, httptest.NewRequest("POST", "/mcp", nil), appOnlyCaller()).WithUICapable(false)
		args := map[string]any{"slug": "mockjam", "kind": "join", "stage": "open", "team_id": "t1"}
		if _, err := mcp.CallTool(tool, call, args); err == nil {
			t.Fatalf("%s: ran for a connection with no declared UI support", name)
		}
	}
}

func appOnlyTwinNames() []string {
	return append(append(append([]string{}, hackAppOnlyJudgeNames...), hackAppOnlyOrganiserNames...), hackAppOnlySharedNames...)
}
