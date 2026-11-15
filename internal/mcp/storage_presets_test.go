package mcp

import (
	"strings"
	"testing"
)

// Every storage preset has a recipe; recipes and the Simple Host tools speak
// presets, and so do Simple Hack's event tools and recipes (owner decision
// 2026-10-10), with Hack's own meaning of owner.
func TestStoragePresetRecipesAndTools(t *testing.T) {
	for _, store := range []string{"postgres", "sqlite"} {
		t.Run(store, func(t *testing.T) { t.Setenv("KV_STORE", store); runTestStoragePresetRecipesAndTools(t) })
	}
}

func runTestStoragePresetRecipesAndTools(t *testing.T) {
	for _, topic := range recipeTopics {
		text, err := PageRecipe(topic, "shop")
		if err != nil {
			t.Fatalf("%s: %v", topic, err)
		}
		if strings.Contains(text, "write_mode") || strings.Contains(text, "{site}") {
			t.Fatalf("%s: older fields or unfilled site", topic)
		}
		// Job topics and plain-word aliases set the preset their job needs.
		preset := map[string]string{"booking": "records", "reservation": "records", "appointment": "records", "portal": "wall",
			"quote": "inbox", "waitlist": "inbox", "contact": "inbox", "form": "inbox", "menu": "public", "guestbook": "comments"}[topic]
		if preset == "" {
			preset = topic
		}
		if topic != "admin" && topic != "people" && topic != "gallery" && topic != "wordpress" && !strings.Contains(text, `"preset": "`+preset+`"`) {
			t.Fatalf("%s: does not set preset %s", topic, preset)
		}
	}
	for _, tool := range storageTools() {
		if tool.Name == "storage_set_resource" {
			for _, preset := range []string{"public", "inbox", "wall", "comments", "records", "personal", "board", "private", "Lookalikes"} {
				if !strings.Contains(tool.Description, preset) {
					t.Fatalf("set_resource description lacks %s", preset)
				}
			}
		}
	}
}
