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
	for _, tc := range []struct {
		in   pkg
		want string
	}{
		{pkg{ImportPath: "internal/a [internal/a.test]", ForTest: "internal/a"}, "internal/a"},
		{pkg{ImportPath: "internal/a_test [internal/a.test]"}, "internal/a"},
		{pkg{ImportPath: "internal/a.test", Name: "main"}, "internal/a"},
		{pkg{ImportPath: "internal/a.test", Name: "a"}, "internal/a.test"},
		{pkg{ImportPath: "internal/a_test"}, "internal/a_test"},
	} {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExclusions(t *testing.T) {
	rs := []rule{{"@/cmd/server", []string{"/**", "!testdb", "!geoip/geoiptest", "@/db"}}}
	for _, tc := range []struct {
		to   string
		want bool
	}{{"handler", true}, {"testdb", false}, {"geoip/geoiptest", false}, {"@/db", true}} {
		if got := allowed(rs, "@/cmd/server", tc.to); got != tc.want {
			t.Errorf("allowed(%q) = %v", tc.to, got)
		}
	}
	if got := local("github.com/vsriram/simple-host"); got != "@/" {
		t.Errorf("module root = %q", got)
	}
}
