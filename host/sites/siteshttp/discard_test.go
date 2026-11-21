package siteshttp

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestVersionAuthor(t *testing.T) {
	long := strings.Repeat("é", 100)
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  Alice  ", "Alice"},
		{"Bo\x01b\x7f", "Bob"},
		{"A\u0085B\u009f", "AB"}, // C1 controls go too
		{"\tTab\nNew\rLine ", "TabNewLine"},
		{long, strings.Repeat("é", 80)},
		{strings.Repeat("a", 79) + " b", strings.Repeat("a", 79)}, // trimmed again after the cut
		{"Claude (release bot) <bot@example.com>", "Claude (release bot) <bot@example.com>"},
	} {
		r := httptest.NewRequest("PUT", "/v1/sites/s/files?"+url.Values{"author": {c.in}}.Encode(), nil)
		if got := versionAuthor(r); got != c.want {
			t.Errorf("versionAuthor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := versionAuthor(httptest.NewRequest("PUT", "/v1/sites/s/files", nil)); got != "" {
		t.Errorf("no ?author=: %q, want empty", got)
	}
}

func TestPendingParam(t *testing.T) {
	for in, want := range map[string]bool{"": false, "1": true, "true": true, "YES": true, "0": false, "no": false, "maybe": false} {
		r := httptest.NewRequest("GET", "/v1/sites/s/versions?pending="+in, nil)
		if got := pendingParam(r); got != want {
			t.Errorf("pending=%q: %v, want %v", in, got, want)
		}
	}
}
