package hackmcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// What the tool server's shared machinery (telemetry, website tools,
// instructions, recipes) does with Hack's tools, through its public surface.

// A planted private value in the arguments or the result of these tools never
// reaches the telemetry copy of the call.
func TestAnalyticsHackToolsKeepPrivateValuesOut(t *testing.T) {
	s := newTestServer(nil)
	const planted = "PLANTED_PRIVATE_VALUE"
	for i, tc := range []struct {
		name             string
		args, structured map[string]any
		text             string
	}{
		{"hack_create_event", map[string]any{"contact_email": planted}, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_update_event", map[string]any{"contact_email": planted}, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_get_event", nil, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_export_results", nil, map[string]any{"csv": planted, "filename": "results.csv"}, planted},
		{"hack_export_scores", nil, map[string]any{"csv": planted, "filename": "scores.csv"}, planted},
		{"hack_regenerate_code", nil, map[string]any{"url": planted, "code": planted}, planted},
		{"hack_export_participants", nil, map[string]any{"csv": planted, "filename": "participants.csv"}, planted},
		// Shapes from a seeded event (scripts/mcpjam/seed.py): emails, join
		// and judge codes, team links and other people's names.
		{"hack_get_event", nil, map[string]any{"organiser": map[string]any{"join_code": planted, "judge_code": planted, "join_url": planted, "judge_url": planted}}, ""},
		{"hack_get_people", nil, map[string]any{"response": map[string]any{"people": []any{map[string]any{"display_name": planted, "email": planted, "role": "participant"}}}}, ""},
		{"hack_get_teams", nil, map[string]any{"response": map[string]any{"teams": []any{map[string]any{"name": "Byte Club", "link": planted, "members": []any{map[string]any{"display_name": planted, "email": planted}}}}}}, ""},
		{"hack_list_open_teams", nil, map[string]any{"teams": []any{map[string]any{"name": "Byte Club", "members": []any{planted}}}}, ""},
		{"hack_get_entry", nil, map[string]any{"entry": map[string]any{"title": "Lunch Line", "updated_by": planted}, "submitted_by": planted}, ""},
		{"hack_get_judging_settings", nil, map[string]any{"response": map[string]any{"judges": []any{map[string]any{"name": planted, "organiser": false}}}}, ""},
		{"hack_get_judging_dashboard", nil, map[string]any{"response": map[string]any{"judges": []any{map[string]any{"judge_name": planted, "scored": 1}}}}, ""},
		{"hack_application_status", nil, map[string]any{"account_name": planted, "display_name": planted}, ""},
		{"hack_join_event", map[string]any{"code": planted, "display_name": planted}, nil, ""},
	} {
		if tc.text == "" && tc.structured != nil {
			body, _ := json.Marshal(tc.structured)
			tc.text = string(body)
		}
		t.Run(fmt.Sprintf("%d_%s", i, tc.name), func(t *testing.T) {
			originalArgs, _ := json.Marshal(tc.args)
			originalStructured, _ := json.Marshal(tc.structured)
			parameters, response := s.AnalyticsCopy(tc.name, tc.args, tc.text, tc.structured)
			body, err := json.Marshal([]any{parameters, response})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), planted) {
				t.Errorf("telemetry copy contains the planted value: %s", body)
			}
			after, _ := json.Marshal(tc.structured)
			afterArgs, _ := json.Marshal(tc.args)
			if string(after) != string(originalStructured) || string(afterArgs) != string(originalArgs) {
				t.Fatal("source mutated")
			}
		})
	}
}

// An applicant's name, city, age range, gender and answers are removed from
// the telemetry copy of hack_apply_to_event's arguments and of
// hack_get_applications' response, with the applicant's account ID; unrelated
// fields stay.
func TestAnalyticsHackApplicantFieldsRemoved(t *testing.T) {
	s := newTestServer(nil)
	args := map[string]any{
		"slug": "spring", "accept_coc": true, "display_name": "Dewi",
		"city": "Jakarta", "country": "ID", "age_range": "under_18", "gender": "woman",
		"answers": map[string]any{"why": "I want to build an app for my school library."},
	}
	applyCopy, _ := s.AnalyticsCopy("hack_apply_to_event", args, "", nil)
	text := `{"response":[{"user_id":"u1","display_name":"Dewi","city":"Jakarta","country":"ID","age_range":"under_18","gender":"woman","answers":[{"id":"why","prompt":"Why?","answer":"I want to build an app for my school library."}]}],"status":200}`
	_, applicationsCopy := s.AnalyticsCopy("hack_get_applications", nil, text, nil)
	for _, tc := range []struct {
		copy any
		keep string
	}{{applyCopy, "spring"}, {applicationsCopy, "status"}} {
		body, err := json.Marshal(tc.copy)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaked := range []string{"Jakarta", "\"ID\"", "under_18", "woman", "Dewi", "school library", "u1"} {
			if strings.Contains(string(body), leaked) {
				t.Errorf("telemetry copy still carries %q: %s", leaked, body)
			}
		}
		if !strings.Contains(string(body), tc.keep) {
			t.Errorf("unrelated fields were also removed: %s", body)
		}
	}
}

// Simple Hack has no named viewers: its create_site has neither input.
func TestHackCreateSiteHasNoNamedViewers(t *testing.T) {
	for _, tool := range mcp.ToolsWithLimits(mcp.Limits{SiteViewersMax: 3}) {
		if tool.Name != "create_site" {
			continue
		}
		if tool.InputSchema["properties"].(map[string]any)["viewers"] == nil {
			t.Fatal("the platform's create_site has no viewers input to remove")
		}
		hp := hackSiteCreateSite(tool, mcp.Limits{}).InputSchema["properties"].(map[string]any)
		if hp["access"] != nil || hp["viewers"] != nil {
			t.Fatal("Hack exposes named viewers")
		}
		return
	}
	t.Fatal("no create_site")
}

// Clients cut instructions past 2000 characters; the longest instance host
// must still fit.
func TestHackInstructionsLength(t *testing.T) {
	for _, host := range []string{"simple-hack.app", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)} {
		for _, caller := range []mcp.Caller{{Mode: mcp.CallerModeEvents, APIKey: "fixture"}, {Mode: mcp.CallerModeEvents, GrantID: "personal", APIKey: "fixture"}} {
			product := Contribution()
			text := instructionsAs(t, mcp.NewServer(mcp.Config{Contribution: &product, APIHost: host, SelfHosted: host != "simple-hack.app"}), caller)
			if n := len([]rune(text)); n >= 2000 || !strings.Contains(text, host) {
				t.Fatalf("%s/%s: %d characters, host named: %t", host, caller.GrantID, n, strings.Contains(text, host))
			}
		}
	}
}

// Simple Hack's tools change nothing for a Simple Host connection: the
// listing and the instructions are the ones a server with no product gives.
func TestHackContributionLeavesHostConnectionsAlone(t *testing.T) {
	for _, events := range []bool{false, true} {
		product := Contribution()
		composedCfg := mcp.Config{APIHost: "connector.example.test", Contribution: &product}
		hostCfg := mcp.Config{APIHost: "connector.example.test"}
		if events {
			composedCfg.Events, hostCfg.Events = wireEvents{}, wireEvents{}
		}
		composed, host := mcp.NewServer(composedCfg), mcp.NewServer(hostCfg)
		for _, caller := range []mcp.Caller{{APIKey: "fixture"}, {APIKey: "fixture", AppName: "ChatGPT", DeployOnly: true}} {
			if listingBytes(t, composed, caller) != listingBytes(t, host, caller) || instructionsAs(t, composed, caller) != instructionsAs(t, host, caller) {
				t.Fatalf("Host wire differs with Simple Hack contributed (events=%t caller=%+v)", events, caller)
			}
		}
	}
}

// The recipe tool returns the same recipe in text and structured.
func TestHackPageRecipeTool(t *testing.T) {
	c := mcp.NewCall(newTestServer(nil), nil, mcp.Caller{})
	out, err := mcp.CallTool(hackPageRecipeTool(), c, map[string]any{"topic": "booking", "site": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, `"policies"`) || out.Structured["recipe"] != out.Text {
		t.Fatalf("recipe: %v", out.Structured)
	}
}

// Simple Hack refuses the recipes it cannot honour: portal (no per-client
// pages), comments/guestbook (no spam check) and people (Host only, not even
// in the topic enum). Every other topic is served.
func TestHackRecipeRefusals(t *testing.T) {
	refused := map[string]bool{"portal": true, "comments": true, "guestbook": true, "people": true}
	for _, topic := range append(mcp.RecipeTopics(), "event") {
		_, err := HackPageRecipe(topic, "my-event")
		if refused[topic] != (err != nil) {
			t.Errorf("%s: err=%v, want refused=%t", topic, err, refused[topic])
		}
	}
	topicEnum, _ := hackPageRecipeTool().InputSchema["properties"].(map[string]any)["topic"].(map[string]any)["enum"].([]string)
	if len(topicEnum) == 0 {
		t.Fatal("hack_get_page_recipe has no topic enum")
	}
	for _, topic := range topicEnum {
		if topic == "people" {
			t.Fatal("hack_get_page_recipe's topic enum still lists people")
		}
	}
}
