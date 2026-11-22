package core

import "testing"

// teamCodeFromInput needs no database; a pasted link's query/fragment must
// be stripped before trailing slashes are trimmed, not after (Codex
// review, 2026-11-01): ".../abcd1234/?source=chat" has no trailing slash
// of its own, but removing "?source=chat" leaves one behind
// (".../abcd1234/"), and trimming only happened first in the old code, so
// the last segment came out empty.
func TestTeamCodeFromInputTrailingSlashAndQuery(t *testing.T) {
	h := New(nil, "https://simple-hack.app", "simple-hack.test", nil)
	cases := []struct{ name, in, want string }{
		{"bare code", "abcd1234", "abcd1234"},
		{"link, no trailing slash", "https://simple-hack.app/team/abcd1234", "abcd1234"},
		{"link, trailing slash", "https://simple-hack.app/team/abcd1234/", "abcd1234"},
		{"link, query, no trailing slash", "https://simple-hack.app/team/abcd1234?source=chat", "abcd1234"},
		{"link, trailing slash then query", "https://simple-hack.app/team/abcd1234/?source=chat", "abcd1234"},
		{"link, fragment", "https://simple-hack.app/team/abcd1234#x", "abcd1234"},
		{"link, trailing slash then fragment", "https://simple-hack.app/team/abcd1234/#x", "abcd1234"},
		{"bare path, trailing slash then query", "/team/abcd1234/?source=chat", "abcd1234"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := h.Links.TeamCodeFromInput(c.in); got != c.want {
				t.Fatalf("%q: got %q want %q", c.in, got, c.want)
			}
		})
	}
}
