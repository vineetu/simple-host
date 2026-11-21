package hackmcp

import (
	"strings"
	"testing"
)

// Every Hack route resolves a team argument by its slug (the name in its
// address), never by its ID, so no tool may tell the model to send an ID.
func TestHackTeamArgumentsAreSlugs(t *testing.T) {
	for _, tool := range Tools() {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		field, ok := props["team"].(map[string]any)
		if !ok {
			continue
		}
		desc, _ := field["description"].(string)
		if !strings.Contains(strings.ToLower(desc), "slug") || strings.Contains(desc, " ID") {
			t.Errorf("%s: team is described as %q", tool.Name, desc)
		}
	}
}
