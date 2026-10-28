package mcp

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSubmissionToolsMatchChatGPTListing(t *testing.T) {
	// Production Host enables Events, which adds the two notification tools.
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	result := eventResult(t, sendEvent(t, s, "tools/list", nil, eventTestCaller(), false), false)
	listed := result["tools"].([]any)
	if len(listed) != 55 {
		t.Fatalf("ChatGPT lists %d tools, want 55", len(listed))
	}

	data, err := os.ReadFile("../../openai-plugin/chatgpt-app-submission.json")
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

	names := make(map[string]bool, len(listed))
	for _, raw := range listed {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if names[name] {
			t.Errorf("ChatGPT lists duplicate tool %s", name)
		}
		names[name] = true
		if name == "find_tools" || name == "run_tool" {
			t.Errorf("ChatGPT lists discovery wrapper %s", name)
		}
		submitted, ok := submission.Tools[name]
		if !ok {
			t.Errorf("submission is missing tool %s", name)
			continue
		}
		annotations := tool["annotations"].(map[string]any)
		for _, hint := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
			want, serverOK := annotations[hint].(bool)
			got, submissionOK := submitted.Annotations[hint].(bool)
			if !serverOK || !submissionOK || got != want {
				t.Errorf("%s %s: submission = %v, server = %v; both must be explicit matching booleans", name, hint, submitted.Annotations[hint], annotations[hint])
			}
		}
	}
	for name := range submission.Tools {
		if !names[name] {
			t.Errorf("submission lists tool %s absent from ChatGPT tools/list", name)
		}
	}
}
