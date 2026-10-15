package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// A server without Turnstile keys (a self-hosted box by default) refuses the
// comments preset, so its tools, recipe and instructions never offer it.
func TestNoSpamCheckLeavesOutComments(t *testing.T) {
	s := NewServer(Config{APIHost: "box.example", ContentOrigin: "https://sites.box.example", NoSpamCheck: true, SelfHosted: true})
	for _, name := range []string{"storage_set_resource", "storage_sql_schema", "get_page_recipe"} {
		tool := s.byName[name]
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		text := tool.Description + string(b)
		banned := []string{"comments (", "wall, comments", `"checked"`, "or checked", "approve_first", "Turnstile"}
		if name == "get_page_recipe" {
			banned = []string{"comments (a guestbook"}
		}
		for _, w := range banned {
			if strings.Contains(text, w) {
				t.Errorf("%s mentions %q without the spam check", name, w)
			}
		}
	}
	for _, topic := range []string{"comments", "guestbook"} {
		_, err := s.byName["get_page_recipe"].run(nil, map[string]any{"topic": topic})
		if err == nil || !strings.Contains(err.Error(), "topic wall") {
			t.Errorf("recipe %s: %v, want a pointer to wall", topic, err)
		}
	}
	text := s.instructionsFor(Caller{})
	for _, w := range []string{"topic comments", "comments (anyone / checked", "wall, comments, board", "Browse connectors", "Plugins, then Discover"} {
		if strings.Contains(text, w) {
			t.Errorf("instructions mention %q on a self-hosted server without the spam check", w)
		}
	}
	for _, w := range []string{"topic wall (people sign in", "Add custom connector"} {
		if !strings.Contains(text, w) {
			t.Errorf("instructions lack %q", w)
		}
	}

	hosted := newTestServer(nil)
	text = hosted.instructionsFor(Caller{})
	for _, w := range []string{"topic comments", "comments (anyone / checked", "Browse connectors"} {
		if !strings.Contains(text, w) {
			t.Errorf("hosted instructions lack %q", w)
		}
	}
	if _, err := hosted.byName["get_page_recipe"].run(nil, map[string]any{"topic": "comments"}); err != nil {
		t.Errorf("hosted comments recipe: %v", err)
	}
}
