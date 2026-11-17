package storage

import "testing"

func TestLegacyPublicGate(t *testing.T) {
	for _, tt := range []struct {
		current, kind             string
		oldPrivate, private, want bool
	}{
		{"mine", "content", true, false, false},
		{"entries", "content", false, false, false},
		{"entries", "content", true, true, true},
		{"entries", "board", true, true, true},
		{"entries", "mine", true, false, false},
		{"entries", "entries", true, true, false},
		{"entries", "entries", true, false, true},
	} {
		if got := LegacyPublicGateCounts(tt.current, tt.oldPrivate, tt.kind, tt.private); got != tt.want {
			t.Errorf("gate counts %+v: got %t", tt, got)
		}
	}
	for _, tt := range []struct {
		kind          string
		confirm       bool
		live, deleted int64
		want          bool
	}{
		{"content", true, 0, 1, true},
		{"content", false, 0, 0, false},
		{"entries", false, 1, 0, true},
		{"entries", true, 1, 0, false},
	} {
		if got := LegacyPublicGateRefuses(tt.kind, tt.confirm, tt.live, tt.deleted); got != tt.want {
			t.Errorf("gate refuses %+v: got %t", tt, got)
		}
	}
}
