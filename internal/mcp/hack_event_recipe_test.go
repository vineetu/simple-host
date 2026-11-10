package mcp

import (
	"strings"
	"testing"
)

func TestHackEventRecipe(t *testing.T) {
	text, err := HackPageRecipe("event", "my-hack")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://simple-hack.app/v1/hack/events/my-hack/public", "60000", "visibilitychange", "document.visibilityState", "Not open yet", "Sign-up is open", "Teams are building", "Entries are closed", "Judging is under way", "Results coming soon", "Results are out", "This event has ended", "Join with the link from the organisers", "Winners will be posted on this page."} {
		if !strings.Contains(text, want) {
			t.Errorf("event recipe missing %q", want)
		}
	}
	if strings.Contains(text, "{event}") || strings.Contains(text, hackRecipeNote) {
		t.Fatal("unfilled slug or storage preamble")
	}
	for _, topic := range []string{"event", "hack-event"} {
		if _, err := PageRecipe(topic, "my-hack"); err == nil {
			t.Fatalf("Host serves %s", topic)
		}
	}
	for _, tc := range []struct {
		tool Tool
		want bool
	}{{hackPageRecipeTool(), true}, {pageRecipeTool(), false}} {
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
	s := newTestServer(nil)
	s.cfg.APIHost = "hack.example.test"
	out, err := hackPageRecipeTool().run(&call{server: s}, map[string]any{"topic": "event", "site": "my-hack"})
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
	for _, slug := range []string{"", "My-hack", "my_hack", "my hack", "../hack", "hack/other", "hack?x=1", "hack#x", "hack'", "</script>", "-hack", "hack-", strings.Repeat("a", 64)} {
		if _, err := HackPageRecipe("event", slug); err == nil {
			t.Errorf("accepted invalid event slug %q", slug)
		}
	}
}
