package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateSitePrivateInputs(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){"POST /v1/sites/secret/files": func() (int, string) {
		return 201, `{"name":"secret","access":"specific","active_version":1,"url":"https://secret.example/"}`
	}}}
	text, _, failed := resultOf(t, send(t, newTestServer(up), toolCall("create_site", map[string]any{"site": "secret", "files": map[string]any{"index.html": "hello"}, "access": "specific", "viewers": []any{"Mom@example.com", "mom@example.com"}}), nil, true))
	if failed || !strings.Contains(text, "Only you and 1 named viewer") {
		t.Fatalf("result: %s", text)
	}
	var body map[string]any
	if len(up.bodies) != 1 {
		t.Fatalf("requests: %v", up.bodies)
	}
	if err := json.Unmarshal([]byte(up.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["access"] != "specific" || len(body["viewers"].([]any)) != 2 {
		t.Fatalf("body: %v", body)
	}
	for _, tool := range siteTools(Limits{SiteViewersMax: 3}) {
		if tool.Name != "create_site" {
			continue
		}
		if strings.Contains(tool.Description, "The site is public to anyone") {
			t.Fatal("always public")
		}
		props := tool.InputSchema["properties"].(map[string]any)
		if props["viewers"].(map[string]any)["maxItems"] != 3 {
			t.Fatal("viewer cap missing")
		}
		hack := hackSiteCreateSite(tool, Limits{})
		hp := hack.InputSchema["properties"].(map[string]any)
		if hp["access"] != nil || hp["viewers"] != nil {
			t.Fatal("Hack exposes named viewers")
		}
	}
}
