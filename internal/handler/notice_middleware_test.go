package handler

import "testing"

func TestSkillIsStale(t *testing.T) {
	tests := []struct {
		name   string
		client string
		server string
		want   bool
	}{
		{"no header at all", "", "0.9.0", true},
		{"exact match", "0.9.0", "0.9.0", false},
		{"client one patch behind", "0.8.4", "0.9.0", true},
		{"client one major behind", "0.9.0", "1.0.0", true},
		{"client one patch ahead", "0.9.1", "0.9.0", false},
		{"client one minor ahead", "0.10.0", "0.9.0", false},
		{"minor compared numerically, not lexically", "0.9.0", "0.10.0", true},
		{"patch compared numerically, not lexically", "0.9.9", "0.9.10", true},
		{"client unparseable", "banana", "0.9.0", true},
		{"server unparseable", "0.9.0", "banana", true},
		{"both unparseable but equal", "banana", "banana", false},
		{"whitespace tolerated", " 0.9.0 ", "0.9.0", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := skillIsStale(tc.client, tc.server); got != tc.want {
				t.Errorf("skillIsStale(%q, %q) = %v, want %v", tc.client, tc.server, got, tc.want)
			}
		})
	}
}

// A client running ahead of the server is the normal state for anyone who
// installed with `npx skills add`, which pulls from the GitHub repository rather
// than from this server's embedded bundle. They must not be told to update.
func TestSkillAheadOfServerIsNotStale(t *testing.T) {
	if skillIsStale("1.0.0", "0.9.0") {
		t.Fatal("a client newer than the server was reported stale")
	}
}
