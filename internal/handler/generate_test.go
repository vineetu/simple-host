package handler

import "testing"

// The parser has to tell a generated PAGE from an assistant that merely talks
// about HTML — which it does constantly, being a web-design assistant.
func TestSplitReplyAndHTML(t *testing.T) {
	full := "<!DOCTYPE html><html><body>hi</body></html>"
	cases := []struct {
		name      string
		in        string
		wantReply string
		wantHTML  bool
	}{
		{"sentinel", "Here you go.\n<<<SITE_HTML>>>\n" + full, "Here you go.", true},
		{"invented tag", "Nice one.\n\n<sh-site-builder>\n" + full, "Nice one.", true},
		{"fenced", "Done.\n```html\n" + full + "\n```", "Done.", true},
		{"bare doctype", "All set.\n" + full, "All set.", true},
		{"chat only", "What's it for?", "What's it for?", false},

		// regressions: every one of these used to be split into a broken "site"
		{"mentions html tag", "In HTML, <html> is the root element. Want me to build one?",
			"In HTML, <html> is the root element. Want me to build one?", false},
		{"mentions doctype", "I'd start the file with <!DOCTYPE html> and go from there. Shall I?",
			"I'd start the file with <!DOCTYPE html> and go from there. Shall I?", false},
		{"fenced snippet, not a page", "Here's a snippet:\n```html\n<div class=\"card\">hi</div>\n```\nUse it?",
			"Here's a snippet:\n```html\n<div class=\"card\">hi</div>\n```\nUse it?", false},
		{"sentinel path keeps trailing tag", "I'll wrap it in a <section>\n<<<SITE_HTML>>>\n" + full,
			"I'll wrap it in a <section>", true},
	}
	for _, c := range cases {
		reply, html, err := splitReplyAndHTML(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if reply != c.wantReply {
			t.Errorf("%s: reply = %q, want %q", c.name, reply, c.wantReply)
		}
		if (html != "") != c.wantHTML {
			t.Errorf("%s: html present = %v, want %v (got %q)", c.name, html != "", c.wantHTML, html)
		}
	}
}

// A page whose own JS contains a triple backtick must not be cut at it.
func TestFencedDocumentWithBackticks(t *testing.T) {
	in := "Done!\n```html\n<!DOCTYPE html><html><body><script>const t=`a ``` b`;</script></body></html>\n```"
	_, html, err := splitReplyAndHTML(in)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(html, "</html>") {
		t.Errorf("document truncated at an inner backtick: %q", html)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })()
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
