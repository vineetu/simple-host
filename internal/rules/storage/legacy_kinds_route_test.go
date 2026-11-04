package storage

import "testing"

func TestLegacyEntryRoute(t *testing.T) {
	tests := []struct {
		kind            string
		owner, deleting bool
		want            LegacyEntryDestination
	}{
		{"entries", false, false, LegacyEntryVisitor},
		{"entries", true, false, LegacyEntryOwner},
		{"board", false, false, LegacyEntryBoard},
		{"board", true, false, LegacyEntryBoard},
		{"board", true, true, LegacyEntryOwner},
		{"board", false, true, LegacyEntryBoard},
	}
	for _, tt := range tests {
		if got := LegacyEntryRoute(tt.kind, tt.owner, tt.deleting); got != tt.want {
			t.Errorf("route(%q, %t, %t) = %v, want %v", tt.kind, tt.owner, tt.deleting, got, tt.want)
		}
	}
}
