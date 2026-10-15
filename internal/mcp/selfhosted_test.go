package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// A server without Turnstile keys (a self-hosted box by default) refuses the
// comments preset, so its tools, recipe and instructions never offer it.
func TestNoSpamCheckLeavesOutComments(t *testing.T) {
	rewrite := strings.NewReplacer("<site>.<handle>.simple-host.app", "sites.box.example/<handle>/<site>", "simple-host.app", "box.example").Replace
	s := NewServer(Config{APIHost: "box.example", ContentOrigin: "https://sites.box.example", NoSpamCheck: true, SelfHosted: true, Rewrite: rewrite, SupportContact: "ops@box.example"})
	defer NewServer(Config{})
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
	for _, w := range []string{"topic wall (people sign in", "Add custom connector, name it Simple Host, paste https://box.example/mcp", "https://box.example/auth.js", "ops@box.example"} {
		if !strings.Contains(text, w) {
			t.Errorf("instructions lack %q", w)
		}
	}
	if strings.Contains(text, "simple-host.app") {
		t.Errorf("self-hosted instructions name simple-host.app")
	}
	for _, tool := range s.tools {
		b, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(tool.Description+string(b), "simple-host.app") {
			t.Errorf("self-hosted %s names simple-host.app", tool.Name)
		}
	}
	out, err := s.byName["get_page_recipe"].run(nil, map[string]any{"topic": "booking"})
	if err != nil || strings.Contains(out.Text, "simple-host.app") || !strings.Contains(out.Text, "https://box.example/auth.js") {
		t.Errorf("self-hosted booking recipe does not name this server: %v", err)
	}

	NewServer(Config{})
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
