package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
// the caller's OWN conflicts, Caller.accountUserID()) does not filter the
// fixture row out before this test can assert on it.
func appOnlyCaller() Caller {
	return Caller{APIKey: "app-only-key", Mode: CallerModeEvents, Handle: "alice", Owner: "j1"}
}

func runAppOnlyTool(t *testing.T, s *Server, name string, args map[string]any) output {
	t.Helper()
	tool, ok := s.hackByName[name]
	if !ok {
		t.Fatalf("%s: not a registered tool", name)
	}
	call := &call{server: s, caller: appOnlyCaller(), orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
	out, err := tool.run(call, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

// TestHackAppOnlyTwinsAreMarkedAppOnly pins the one thing a compliant
// host reads to leave every twin off the model's own tool list (Grok
// security review finding 1, SIM-344 phase 2): _meta.ui.visibility ===
// ["app"], the same key passcode_screen already carries.
func TestHackAppOnlyTwinsAreMarkedAppOnly(t *testing.T) {
	s := newTestServer(appOnlyUpstream())
	for _, name := range appOnlyTwinNames() {
		tool, ok := s.hackByName[name]
		if !ok {
			t.Fatalf("%s: not a registered tool", name)
		}
		ui, _ := tool.Meta["ui"].(map[string]any)
		vis, _ := ui["visibility"].([]string)
		if len(vis) != 1 || vis[0] != "app" {
			t.Fatalf("%s: _meta.ui.visibility = %#v, want [\"app\"]", name, ui["visibility"])
		}
	}
}

// TestHackAppOnlyTwinsContentCarriesNoPersonalData is the explicit test
// the owner's follow-up asked for: every tool either the judge or
// organiser view calls on its own must have no personal text, email, URL
// or code in content or structuredContent (Grok security review findings
// 1 and 2, SIM-344 phase 2). It runs every app-only twin against the
// fixture upstream above (which plants a distinct marker in place of
// every applicant email, free-text answer, rubric criterion, judge/team
// name, former judge, join/judge/regenerated/invite URL and judge
// scoring comment) and asserts:
//   - out.Text contains no marker
//   - json.Marshal(out.Structured) contains no marker
//   - out.Meta DOES carry every marker this specific twin's own REST
//     response planted, proving the data reached the view (via _meta,
//     read only through callToolResult) rather than being silently
//     dropped.
func TestHackAppOnlyTwinsContentCarriesNoPersonalData(t *testing.T) {
	// Both flags on: this test exercises twins from both apps, and each
	// twin's own run() now gates on the matching show tool's flag, not
	// just c.uiClient (owner follow-up review, SIM-344 phase 2) --
	// TestHackAppOnlyTwinsListingGating below covers the flag/app-name/
	// UI-declared gate itself in isolation.
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
			// SIM-384: every hack_* tool's real output, checked against its
			// own declared outputSchema in strict mode (additionalProperties:
			// false as authored, outputs.go) -- the class of bug where an
			// added field breaks a client that cached an older schema can
			// only recur in the wire copy; the authored one here must still
			// agree with what the tool actually sends.
			if err := ValidateOutput(s.hackByName[tc.tool].OutputSchema, out.Structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.tool, err)
			}
			recordHackOutputValidated(tc.tool)
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
		tool := s.hackByName[name]
		call := &call{server: s, caller: appOnlyCaller(), orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: false}
		args := map[string]any{"slug": "mockjam", "kind": "join", "stage": "open", "team_id": "t1"}
		if _, err := tool.run(call, args); err == nil {
			t.Fatalf("%s: ran for a connection with no declared UI support", name)
		}
	}
}

// TestHackAppOnlyTwinsListingGating is the owner follow-up review's own
// ask (SIM-344 phase 2): the 15 twins were being listed in tools/list for
// every events connection, including ones that never declared UI support
// -- _meta.ui.visibility only helps a host that honours it. Each twin is
// now filtered in hackShowToolsAllowed (hack_apps.go) the same way hack_
// show_judge/hack_show_organiser already are: the matching app's own
// three-part gate (flag, events mode, not ChatGPT/OpenAI/Codex, UI
// declared).
func TestHackAppOnlyTwinsListingGating(t *testing.T) {
	s := newTestServer(appOnlyUpstream())
	allTwins := appOnlyTwinNames()

	listedNames := func(c Caller) map[string]bool {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(WithCaller(req.Context(), c))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		out := map[string]bool{}
		for _, tool := range listedTools(t, rec) {
			out[tool["name"].(string)] = true
		}
		return out
	}

	t.Run("non-UI client sees none", func(t *testing.T) {
		t.Setenv("MCP_APP_HACK_JUDGE", "on")
		t.Setenv("MCP_APP_HACK_ORGANISER", "on")
		noUI := Caller{APIKey: "k-noui", Mode: CallerModeEvents, Handle: "alice"}
		listed := listedNames(noUI)
		for _, name := range allTwins {
			if listed[name] {
				t.Errorf("non-UI client sees %s", name)
			}
		}
	})

	t.Run("ChatGPT, OpenAI and Codex app names see none", func(t *testing.T) {
		t.Setenv("MCP_APP_HACK_JUDGE", "on")
		t.Setenv("MCP_APP_HACK_ORGANISER", "on")
		for _, appName := range []string{"ChatGPT", "OpenAI", "Codex"} {
			c := Caller{APIKey: "k-" + appName, Mode: CallerModeEvents, Handle: "alice", AppName: appName}
			s.clientUI.remember(c, true, time.Now())
			listed := listedNames(c)
			for _, name := range allTwins {
				if listed[name] {
					t.Errorf("%s sees %s", appName, name)
				}
			}
		}
	})

	t.Run("UI client with only MCP_APP_HACK_JUDGE sees just the judge twins", func(t *testing.T) {
		t.Setenv("MCP_APP_HACK_JUDGE", "on")
		t.Setenv("MCP_APP_HACK_ORGANISER", "off")
		c := Caller{APIKey: "k-judgeonly", Mode: CallerModeEvents, Handle: "alice"}
		s.clientUI.remember(c, true, time.Now())
		listed := listedNames(c)
		// hack_app_get_rubric is shared: available whenever EITHER app's
		// gate passes, so a judge-only connection sees it too.
		for _, name := range append(append([]string{}, hackAppOnlyJudgeNames...), hackAppOnlySharedNames...) {
			if !listed[name] {
				t.Errorf("judge-only client missing %s", name)
			}
		}
		for _, name := range hackAppOnlyOrganiserNames {
			if listed[name] {
				t.Errorf("judge-only client sees organiser-only twin %s", name)
			}
		}
	})

	t.Run("UI client with only MCP_APP_HACK_ORGANISER sees just the organiser twins", func(t *testing.T) {
		t.Setenv("MCP_APP_HACK_JUDGE", "off")
		t.Setenv("MCP_APP_HACK_ORGANISER", "on")
		c := Caller{APIKey: "k-organiseronly", Mode: CallerModeEvents, Handle: "alice"}
		s.clientUI.remember(c, true, time.Now())
		listed := listedNames(c)
		for _, name := range append(append([]string{}, hackAppOnlyOrganiserNames...), hackAppOnlySharedNames...) {
			if !listed[name] {
				t.Errorf("organiser-only client missing %s", name)
			}
		}
		for _, name := range hackAppOnlyJudgeNames {
			if listed[name] {
				t.Errorf("organiser-only client sees judge-only twin %s", name)
			}
		}
	})
}

func appOnlyTwinNames() []string {
	return []string{
		"hack_app_preview_results", "hack_app_get_results", "hack_app_publish_results",
		"hack_app_get_people", "hack_app_get_applications", "hack_app_get_registration",
		"hack_app_get_rubric", "hack_app_get_judging_settings", "hack_app_get_conflicts",
		"hack_app_get_teams", "hack_app_get_assignments", "hack_app_get_judging_dashboard",
		"hack_app_set_event_stage", "hack_app_regenerate_code", "hack_app_create_organiser_invite",
		"hack_app_set_judging_ends_at",
		"hack_app_get_judge_queue", "hack_app_get_judge_scores", "hack_app_get_my_conflicts",
	}
}
