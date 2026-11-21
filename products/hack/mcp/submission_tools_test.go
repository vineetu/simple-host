package hackmcp

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

type submissionEvents struct{ mcp.EventsBackend }

// The ChatGPT kit (plugins/openai-hack) lists exactly the tools a ChatGPT
// connection to Simple Hack sees, with the same three hints. Production
// Simple Hack has Events and Teams on.
func TestHackSubmissionToolsMatchChatGPTListing(t *testing.T) {
	product := Contribution()
	s := mcp.NewServer(mcp.Config{
		Contribution: &product, Events: submissionEvents{}, Teams: true,
		APIHost: "simple-hack.app", ContentOrigin: "https://sites.simple-hack.app", SiteBases: []string{"simple-hack.app"},
		ServerName: "simple-hack", Version: "9.9.9",
	})
	caller := mcp.Caller{APIKey: "k", Owner: "o", OAuthGrantID: "g", GrantID: "g", Mode: mcp.CallerModeEvents, AppName: "ChatGPT"}
	listed := listedTools(t, rpcAs(t, s, caller, "2025-06-18", "tools/list", nil, false))

	data, err := os.ReadFile("../../../plugins/openai-hack/chatgpt-app-submission.json")
	if err != nil {
		t.Fatal(err)
	}
	var submission struct {
		Tools map[string]struct {
			Annotations map[string]any `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &submission); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed {
		name := tool["name"].(string)
		if names[name] {
			t.Errorf("ChatGPT lists duplicate tool %s", name)
		}
		names[name] = true
		submitted, ok := submission.Tools[name]
		if !ok {
			t.Errorf("kit is missing tool %s", name)
			continue
		}
		annotations, _ := tool["annotations"].(map[string]any)
		for _, hint := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
			want, serverOK := annotations[hint].(bool)
			got, kitOK := submitted.Annotations[hint].(bool)
			if !serverOK || !kitOK || got != want {
				t.Errorf("%s %s: kit = %v, server = %v; both must be explicit matching booleans", name, hint, submitted.Annotations[hint], annotations[hint])
			}
		}
	}
	for name := range submission.Tools {
		if !names[name] {
			t.Errorf("kit lists tool %s absent from ChatGPT tools/list", name)
		}
	}
}
