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
		// Job topics and plain-word aliases set the preset their job needs.
		preset := map[string]string{"booking": "records", "reservation": "records", "appointment": "records", "portal": "wall",
			"quote": "inbox", "waitlist": "inbox", "contact": "inbox", "form": "inbox", "menu": "public"}[topic]
		if preset == "" {
			preset = topic
		}
		if topic != "admin" && topic != "gallery" && !strings.Contains(text, `"preset": "`+preset+`"`) {
			t.Fatalf("%s: does not set preset %s", topic, preset)
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
		if topic == "portal" {
			continue // refused on Simple Hack (TestHackRefusesPortalRecipe)
		}
		text, err := HackPageRecipe(topic, "team")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "https://simple-host.app/auth.js") || strings.Contains(text, "Simple Host account") || strings.Contains(text, "who_am_i") || !strings.HasPrefix(text, "On Simple Hack") {
			t.Fatalf("hack recipe %s keeps Host wording", topic)
		}
	}
}

// Simple Hack has no named viewers, so the portal recipe is refused there.
func TestHackRefusesPortalRecipe(t *testing.T) {
	if _, err := HackPageRecipe("portal", "team"); err == nil {
		t.Fatal("portal recipe served on Simple Hack")
	}
	if _, err := HackPageRecipe("booking", "team"); err != nil {
		t.Fatal(err)
	}
}
