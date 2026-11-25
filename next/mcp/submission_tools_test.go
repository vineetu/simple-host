package mcp

import (
	"encoding/json"
	"os"
	"testing"
)

// listed is tools/list as an AI app sees it. It needs no database.
func listed() []map[string]any {
	res, _ := (&server{}).answer(nil, rpc{Method: "tools/list"})
	return res.(map[string]any)["tools"].([]map[string]any)
}

// The ChatGPT kit (plugins/openai/chatgpt-app-submission.json) lists every tool of
// tools/list with the same annotations. To write the listing as JSON for
// the kit, run: EXPORT_TOOLS=/path/tools.json go test ./mcp -run TestSubmissionToolsMatchListing
func TestSubmissionToolsMatchListing(t *testing.T) {
	if path := os.Getenv("EXPORT_TOOLS"); path != "" {
		b, _ := json.MarshalIndent(listed(), "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("../../plugins/openai/chatgpt-app-submission.json")
	if err != nil {
		t.Skip("no kit next to this code")
	}
	var kit struct {
		Tools map[string]struct{ Annotations map[string]any } `json:"tools"`
	}
	if err := json.Unmarshal(data, &kit); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range listed() {
		name := tool["name"].(string)
		seen[name] = true
		sub, ok := kit.Tools[name]
		if !ok {
			t.Errorf("the kit is missing tool %s", name)
			continue
		}
		for hint, want := range tool["annotations"].(map[string]any) {
			if got, _ := sub.Annotations[hint].(bool); got != want.(bool) || sub.Annotations[hint] == nil {
				t.Errorf("%s %s: kit says %v, server says %v", name, hint, sub.Annotations[hint], want)
			}
		}
	}
	for name := range kit.Tools {
		if !seen[name] {
			t.Errorf("the kit lists %s, which the server does not", name)
		}
	}
}
