package mcp

import (
	"strings"
	"testing"
)

// Every storage preset has a recipe; recipes and the Simple Host tools speak
// presets, and so do Simple Hack's event tools and recipes (owner decision
// 2026-10-10), with Hack's own meaning of owner.
func TestStoragePresetRecipesAndTools(t *testing.T) {
	for _, topic := range recipeTopics {
		text, err := PageRecipe(topic, "shop")
		if err != nil {
			t.Fatalf("%s: %v", topic, err)
		}
		if strings.Contains(text, "write_mode") || strings.Contains(text, "{site}") {
			t.Fatalf("%s: older fields or unfilled site", topic)
		}
		if topic != "admin" && topic != "form" && topic != "gallery" && !strings.Contains(text, `"preset": "`+topic+`"`) {
			t.Fatalf("%s: does not set its own preset", topic)
		}
	}
	for _, tool := range storageTools() {
		if tool.Name == "storage_set_resource" {
			for _, preset := range []string{"public", "inbox", "wall", "records", "personal", "board", "private", "Lookalikes"} {
				if !strings.Contains(tool.Description, preset) {
					t.Fatalf("set_resource description lacks %s", preset)
				}
			}
		}
	}
	for _, tool := range eventStorageTools() {
		if strings.Contains(tool.Description, "write_mode") || strings.Contains(tool.Description, " storage_") {
			t.Fatalf("%s keeps older or Host wording: %s", tool.Name, tool.Description)
		}
		if tool.Name == "hack_event_storage_set_resource" && (!strings.Contains(tool.Description, "organisers") || !strings.Contains(tool.Description, "Lookalikes")) {
			t.Fatalf("event set_resource: %s", tool.Description)
		}
	}
	for _, topic := range recipeTopics {
		text, err := HackPageRecipe(topic, "team")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "https://simple-host.app/auth.js") || strings.Contains(text, "Simple Host account") || strings.Contains(text, "who_am_i") || !strings.HasPrefix(text, "On Simple Hack") {
			t.Fatalf("hack recipe %s keeps Host wording", topic)
		}
	}
}
