package rules

import (
	"strings"
	"testing"
)

func TestNormalEmail(t *testing.T) {
	for in, want := range map[string]string{
		" Mom@Example.COM ":     "mom@example.com",
		"dad+trip@example.org":  "dad+trip@example.org",
		"first.last@sub.ex.com": "first.last@sub.ex.com",
	} {
		if got, ok := NormalEmail(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "mom", "mom@", "@example.com", "mom@example", "mоm@example.com", "mom@exa mple.com", "mom@exa\nmple.com", "a..b@example.com", "mom@example.com,dad@example.com", strings.Repeat("a", 250) + "@example.com", "a@localhost"} {
		if got, ok := NormalEmail(bad); ok {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}
