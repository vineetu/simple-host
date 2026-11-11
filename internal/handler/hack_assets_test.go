package handler

import (
	"strings"
	"testing"
)

// TestHackConnectStepsJSON checks hack-connect-steps.json, the one source
// for per-app connect steps: it parses, the Website screen's tab apps are
// there with the shared publish steps, and /get-started renders every app's
// list from it (no marker left behind, each step's text on the page).
//
// Moved verbatim from hack_site_status_test.go (cleanup 3.2 slice 2a): its
// subject (loadConnectSteps, connectAppByID, expandConnectSteps) moved from
// hack_connect_steps.go (moving) to hack_assets.go (staying), and this test
// needs no teamSiteApp/hackApp fixture either way.
func TestHackConnectStepsJSON(t *testing.T) {
	doc, err := loadConnectSteps()
	if err != nil {
		t.Fatalf("hack-connect-steps.json does not parse: %v", err)
	}
	tabs := 0
	for _, a := range doc.Apps {
		if a.Tab {
			tabs++
		}
	}
	if tabs != 3 || len(doc.Publish) < 2 || !doc.Publish[0].Prompt || !strings.Contains(doc.SigninHint, "{email}") {
		t.Fatalf("connect steps doc: tabs=%d publish=%d hint=%q", tabs, len(doc.Publish), doc.SigninHint)
	}
	for _, id := range []string{"claude", "chatgpt", "claude-code"} {
		if a, ok := connectAppByID(id); !ok || !a.Tab || len(a.Connect) < 2 {
			t.Fatalf("%s: missing or not a tab app", id)
		}
	}
	raw, err := staticFiles.ReadFile("static/hack-get-started.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(expandConnectSteps(raw))
	if strings.Contains(page, "<!--sh:connect") {
		t.Fatal("get-started still has a connect marker after expansion")
	}
	for _, a := range doc.Apps {
		for _, s := range a.Connect {
			if !strings.Contains(page, s.HTML) {
				t.Fatalf("get-started is missing %s step %q", a.ID, s.HTML)
			}
		}
		if a.Fallback != "" && !strings.Contains(page, a.Fallback) {
			t.Fatalf("get-started is missing %s fallback", a.ID)
		}
	}
}
