package main

import "testing"

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"app/{sites,storage}", "app/sites", true},
		{"app/{sites,storage}", "app/hack", false},
		{"repository/**", "repository", true},
		{"repository/**", "repository/sites/nested", true},
		{"repository/**", "repository2", false},
		{"/**", "@/cmd/server", false},
	} {
		if got := match(tc.pattern, tc.path); got != tc.want {
			t.Errorf("match(%q, %q) = %v", tc.pattern, tc.path, got)
		}
	}
}
func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"internal/a [internal/a.test]", "internal/a"},
		{"internal/a_test [internal/a.test]", "internal/a"},
		{"internal/a.test", "internal/a"},
		{"internal/a/b", "internal/a/b"},
	} {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q", tc.in, got)
		}
	}
}
