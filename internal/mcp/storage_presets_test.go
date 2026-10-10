package mcp

import (
	"strings"
	"testing"
)

// Every storage preset has a recipe; recipes and the Simple Host tools speak
// presets, while Simple Hack's event tools keep the rules from before them.
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
		if strings.Contains(tool.Description, "preset") {
			t.Fatalf("%s mentions presets: %s", tool.Name, tool.Description)
		}
	}
}
