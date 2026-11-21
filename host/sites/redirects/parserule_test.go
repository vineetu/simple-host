package redirects

import (
	"strings"
	"testing"
)

func TestParseRule(t *testing.T) {
	cases := []struct {
		line    string
		problem string
		status  int
		force   bool
	}{
		{"/old /new", "", 301, false},
		{"/old /new 302!", "", 302, true},
		{"/old https://example.com/x 410", "404 and 410 need a local target page", 0, false},
		{"/old", "write FROM", 0, false},
		{"/old /new 303", "supported status codes", 0, false},
		{"old /new", "FROM must be a path", 0, false},
		{"/a/*/b /new", "use literal segments", 0, false},
		{"/a id /new", "use literal segments", 0, false},
		{"/a/:id /new/:other", "TO uses an undefined placeholder", 0, false},
		{"/a id=:id /new/:id", "", 301, false},
		{"/a https://[::abc]/x", "TO must be /path or http(s)://literal-host", 0, false},
		{"/a /x#frag", "TO must be a local /path", 0, false},
	}
	for _, c := range cases {
		r, problem := parseRule(ruleTokens(c.line), 7)
		if c.problem == "" {
			if problem != "" || r.status != c.status || r.force != c.force || r.line != 7 {
				t.Errorf("%q: got %+v, %q", c.line, r, problem)
			}
			continue
		}
		if !strings.HasPrefix(problem, c.problem) {
			t.Errorf("%q: problem %q, want prefix %q", c.line, problem, c.problem)
		}
	}
}

func TestRuleTokensDropsComments(t *testing.T) {
	if got := ruleTokens("  /a /b # note #x"); strings.Join(got, " ") != "/a /b" {
		t.Fatalf("got %q", got)
	}
	if got := ruleTokens("# whole line"); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}
