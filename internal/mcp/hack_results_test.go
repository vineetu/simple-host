package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestHackResultsLimitPersonalData(t *testing.T) {
	const event = `{"slug":"spring","role":"participant","me":{"approval_status":"approved","team":{"slug":"builders","name":"Builders","members":[{"display_name":"Ada","you":true}]},"team_key":{"last4":"1234","created_at":"2026-10-01","last_used_at":"2026-10-02"}}}`
	const eventWant = `{"slug":"spring","role":"participant","me":{"approval_status":"approved","team":{"slug":"builders","name":"Builders","members":[{"display_name":"Ada","you":true}]}}}`
	const team = `{"id":"team-id","slug":"builders","name":"Builders","code":"private-invite","created_at":"2026-10-01","members":[{"user_id":"other-member","display_name":"Ada","email":"ada@example.test"}],"site":{"taken_down":true},"deadline":"2026-10-29","deadline_override":"2026-10-29","frozen":false,"extended":true,"pinned_version":null,"pinned_url":null,"track":null}`
	const teamWant = `{"slug":"builders","name":"Builders","site":{"taken_down":true},"deadline":"2026-10-29","deadline_override":"2026-10-29","frozen":false,"extended":true,"pinned_version":null,"pinned_url":null}`
	const conflicts = `{"conflicts":[{"judge_id":"caller","judge_name":"Organiser","team_id":"one","team_name":"One","declared_by":"judge"},{"judge_id":"another-judge","judge_name":"Other judge","team_id":"two","team_name":"Two","declared_by":"organiser"}]}`
	const own = `{"judge_name":"Organiser","team_id":"one","team_name":"One","declared_by":"judge"}`
	cases := []struct {
		name, response, want string
		organiser            bool
	}{
		{"hack_join_event", event, eventWant, false},
		{"hack_join_judge", event, eventWant, false},
		{"hack_application_status", event, eventWant, false},
		{"hack_get_team_status", event, eventWant, false},
		{"hack_decide_application", `{"user_id":"applicant","status":"approved"}`, `{"status":"approved"}`, true},
		{"hack_move_member", team, `{"slug":"builders","name":"Builders"}`, true},
		{"hack_set_team_deadline", team, teamWant, true},
		{"hack_take_down_team_site", team, teamWant, true},
		{"hack_restore_team_site", team, teamWant, true},
		{"hack_get_my_conflicts", conflicts, `{"conflicts":[` + own + `]}`, false},
		{"hack_declare_my_conflict", `{"judge_id":"caller","judge_name":"Organiser","team_id":"one","team_name":"One","declared_by":"judge"}`, own, false},
		{"hack_get_team_key", `{"key":{"last4":"1234","created_at":"2026-10-01","last_used_at":null},"team":{"slug":"builders"}}`, `{"key":{},"team":{"slug":"builders"}}`, true},
		{"hack_get_team_key", `{"key":null,"team":null}`, `{"key":null,"team":null}`, true},
		{"hack_get_judging_dashboard", `{"judges":[{"judge_id":"caller","judge_name":"Organiser","done_count":1,"assigned_count":2}],"teams":[{"team_id":"one","team_name":"One"}]}`, `{"judges":[{"judge_name":"Organiser","done_count":1,"assigned_count":2}],"teams":[{"team_id":"one","team_name":"One"}]}`, true},
		// Organiser administration still needs other judges' IDs and conflicts.
		{"hack_get_conflicts", conflicts, conflicts, true},
		{"hack_accept_organiser_invite", event, event, true},
	}
	byName := map[string]Tool{}
	for _, tool := range append(HackMemberTools(), HackOrganiserTools()...) {
		byName[tool.Name] = tool
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := byName[tc.name]
			up := jsonReply(http.StatusOK, tc.response)
			c := &call{server: newTestServer(http.HandlerFunc(up)), orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "test-key", Mode: CallerModeEvents, UserID: "caller"}}
			out, err := tool.run(c, map[string]any{"slug": "spring", "code": "invite", "team": "builders", "team_id": "one", "user_id": "applicant", "body": map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if tc.organiser {
				want = map[string]any{"status": http.StatusOK, "response": want}
			}
			assertHackResult(t, tool, out, want)
		})
	}
}

func TestHackGetTeamsEmailsOptIn(t *testing.T) {
	const roster = `{"teams":[{"id":"team-id","name":"Builders","slug":"builders","code":"invite","members":[{"user_id":"member-one","display_name":"Ada","email":"ada@example.test"},{"user_id":"member-two","display_name":"Bo","email":"bo@example.test"}]}],"on_no_team":[{"user_id":"member-three","display_name":"Cy","email":"cy@example.test"}],"team_size_max":5}`
	const withoutEmails = `{"teams":[{"id":"team-id","name":"Builders","slug":"builders","code":"invite","members":[{"user_id":"member-one","display_name":"Ada"},{"user_id":"member-two","display_name":"Bo"}]}],"on_no_team":[{"user_id":"member-three","display_name":"Cy"}],"team_size_max":5}`
	for _, input := range []string{`{"slug":"spring"}`, `{"slug":"spring","include_emails":false}`, `{"slug":"spring","include_emails":true}`} {
		t.Run(input, func(t *testing.T) {
			up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){"GET /v1/hack/events/spring/teams": jsonReply(200, roster)}}
			var args map[string]any
			if err := json.Unmarshal([]byte(input), &args); err != nil {
				t.Fatal(err)
			}
			text, result, bad := resultOf(t, sendMode(t, newTestServer(up), toolCall("hack_get_teams", args), CallerModeEvents))
			if bad {
				t.Fatal(text)
			}
			wantJSON := withoutEmails
			if args["include_emails"] == true {
				wantJSON = roster
			} else if strings.Contains(text, "@example.test") {
				t.Fatal("default text contains another member's email")
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result["response"], want) {
				t.Fatalf("team roster mismatch: %v", result)
			}
			var fromText map[string]any
			if err := json.Unmarshal([]byte(text), &fromText); err != nil || !reflect.DeepEqual(fromText, result) {
				t.Fatalf("text and structured results differ: %v", err)
			}
		})
	}
}

func TestHackMyConflictsCallerScope(t *testing.T) {
	for _, userID := range []string{"judge", "organiser", "no-conflicts", ""} {
		t.Run(userID, func(t *testing.T) {
			for _, tool := range HackMemberTools() {
				if tool.Name != "hack_get_my_conflicts" && tool.Name != "hack_declare_my_conflict" {
					continue
				}
				response := `{"judge_id":"judge","team_id":"one","team_name":"One"}`
				want := map[string]any{}
				if tool.Name == "hack_get_my_conflicts" {
					response = `{"conflicts":[` + response + `,{"judge_id":"organiser","team_id":"two","team_name":"Two"}]}`
					rows := []any{}
					if userID == "judge" {
						rows = append(rows, map[string]any{"team_id": "one", "team_name": "One"})
					} else if userID == "organiser" {
						rows = append(rows, map[string]any{"team_id": "two", "team_name": "Two"})
					}
					want["conflicts"] = rows
				} else if userID == "judge" {
					want = map[string]any{"team_id": "one", "team_name": "One"}
				}
				c := &call{server: newTestServer(http.HandlerFunc(jsonReply(200, response))), orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "test-key", Mode: CallerModeEvents, UserID: userID}}
				out, err := tool.run(c, map[string]any{"slug": "spring", "team_id": "one"})
				if err != nil {
					t.Fatal(err)
				}
				assertHackResult(t, tool, out, want)
			}
		})
	}
}

func assertHackResult(t *testing.T, tool Tool, out output, want map[string]any) {
	t.Helper()
	// Normalize integer status codes before comparing JSON representations.
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotRaw, err := json.Marshal(out.Structured)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("%s structured result: %s, want %s", tool.Name, gotRaw, wantRaw)
	}
	var textResult, wantResult any
	if err := json.Unmarshal([]byte(out.Text), &textResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantRaw, &wantResult); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(textResult, wantResult) {
		t.Fatalf("%s text result differs: %s", tool.Name, out.Text)
	}
	if err := ValidateOutput(tool.OutputSchema, out.Structured, nil); err != nil {
		t.Fatalf("%s schema: %v", tool.Name, err)
	}
}
