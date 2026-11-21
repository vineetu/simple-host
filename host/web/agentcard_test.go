package web

import (
	"testing"
)

func TestAgentCardSummary(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"First sentence. Second sentence.", "First sentence."},
		{"Use example.org for sites. More.", "Use example.org for sites."},
		{"One\nline\tplease", "One line please"},
	} {
		if got := AgentCardSummary(tt.input); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}
