package hackmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/mcp"
)

// A Hack tool call sends one AgentCat event carrying the product, the
// caller's role in the event and its stage, the protocol revision, the
// stated intent, the arguments and the result, and none of the secrets or
// other people's details the result held.
func TestAnalyticsHackToolCallEvent(t *testing.T) {
	bodies := make(chan []byte, 4)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	const private = "PLANTED_PRIVATE_VALUE"
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"people": []any{
			map[string]any{"user_id": private, "display_name": private, "email": private, "role": "participant", "team": "Byte Club"},
		}, "join_code": private, "judge_url": private})
	})
	product := Contribution()
	var asked []string
	product.AnalyticsTags = AnalyticsTags(func(_ context.Context, slug, account, user string) EventFact {
		asked = append(asked, slug, account, user)
		return EventFact{Found: true, Role: "organiser", Stage: "building"}
	})
	s := mcp.NewServer(mcp.Config{
		Contribution: &product, Upstream: upstream, APIHost: "simple-hack.app",
		ContentOrigin: "https://sites.simple-hack.app", SiteBases: []string{"simple-hack.app"},
		SkillVersion: "9.9.9", ServerName: "simple-host", Version: "9.9.9",
		AnalyticsProjectID: "project-test", AnalyticsEnv: "prod", AnalyticsProduct: "simple-hack",
		AnalyticsPepper: "pepper", AnalyticsEndpoint: endpoint.URL,
	})
	caller := mcp.Caller{APIKey: "organiser-key", Mode: mcp.CallerModeEvents, Owner: "acct-org", UserID: "user-org", AppName: "ChatGPT"}

	// The optional context input is offered on Hack tools as on Host tools.
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		req = req.WithContext(mcp.WithCaller(req.Context(), caller))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	if list := post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`).Body.String(); !strings.Contains(list, `"context":{"description":`) {
		t.Fatal("Hack tools do not offer the context input")
	}

	rec := post(toolCall("hack_get_people", map[string]any{
		"slug": "Spring-Jam", "context": "The organiser checks who has joined the event before forming teams.",
	}))
	if _, _, isError := resultOf(t, rec); isError {
		t.Fatalf("tool failed: %s", rec.Body.String())
	}

	var body []byte
	select {
	case body = <-bodies:
	case <-time.After(3 * time.Second):
		t.Fatal("no AgentCat event sent")
	}
	if strings.Contains(string(body), private) || strings.Contains(string(body), "acct-org") || strings.Contains(string(body), "user-org") {
		t.Fatalf("event carries a private value: %s", body)
	}
	var event map[string]any
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	tags, _ := event["tags"].(map[string]any)
	for key, want := range map[string]any{"env": "prod", "product": "simple-hack", "role": "organiser", "stage": "building", "protocol": "2025-06-18"} {
		if tags[key] != want {
			t.Errorf("tag %s = %v, want %v (tags %v)", key, tags[key], want, tags)
		}
	}
	if event["resource_name"] != "hack_get_people" || event["client_name"] != "ChatGPT" || event["user_intent"] == "" {
		t.Errorf("event = %s", body)
	}
	params, _ := event["parameters"].(map[string]any)
	args, _ := params["arguments"].(map[string]any)
	if args["slug"] != "Spring-Jam" {
		t.Errorf("arguments = %v", params)
	}
	if response, _ := json.Marshal(event["response"]); !strings.Contains(string(response), "Byte Club") {
		t.Errorf("result missing: %s", response)
	}
	if strings.Join(asked, ",") != "spring-jam,acct-org,user-org" {
		t.Errorf("facts asked for %v", asked)
	}
}
