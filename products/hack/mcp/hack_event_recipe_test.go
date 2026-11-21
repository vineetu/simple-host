package hackmcp

import (
	"github.com/vsriram/simple-host/host/mcp"
	"strings"
	"testing"
)

func TestHackEventRecipe(t *testing.T) {
	text, err := HackPageRecipe("event", "my-hack")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "{event}") || strings.Contains(text, hackRecipeNote) {
		t.Fatal("unfilled slug or storage preamble")
	}
	for _, topic := range []string{"event", "hack-event"} {
		if _, err := mcp.PageRecipe(topic, "my-hack"); err == nil {
			t.Fatalf("Host serves %s", topic)
		}
	}
	for _, tc := range []struct {
		tool mcp.Tool
		want bool
	}{{hackPageRecipeTool(), true}, {mcp.PageRecipeTool(), false}} {
		topics := tc.tool.InputSchema["properties"].(map[string]any)["topic"].(map[string]any)["enum"].([]string)
		found := false
		for _, topic := range topics {
			if topic == "event" {
				found = true
			}
		}
		if found != tc.want {
			t.Fatalf("%s event topic: %v, want %v", tc.tool.Name, found, tc.want)
		}
	}
	product := Contribution()
	s := mcp.NewServer(mcp.Config{Contribution: &product, APIHost: "hack.example.test"})
	out, err := mcp.CallTool(hackPageRecipeTool(), mcp.NewCall(s, nil, mcp.Caller{}), map[string]any{"topic": "event", "site": "my-hack"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "https://hack.example.test/v1/hack/events/my-hack/public") || strings.Contains(out.Text, "simple-hack.app") {
		t.Fatal("event feed did not use instance host")
	}
	if out.Structured["recipe"] != out.Text {
		t.Fatal("recipe text and structured output differ")
	}
}

func TestHackEventRecipeRejectsInvalidSlug(t *testing.T) {
	for _, slug := range []string{"", "My-hack", "../hack", "hack?x=1", "hack'", "</script>", "-hack", strings.Repeat("a", 64)} {
		if _, err := HackPageRecipe("event", slug); err == nil {
			t.Errorf("accepted invalid event slug %q", slug)
		}
	}
}
