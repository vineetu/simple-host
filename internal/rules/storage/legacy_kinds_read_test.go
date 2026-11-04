package storage

import "testing"

func TestLegacyDataReadLimited(t *testing.T) {
	for _, tc := range []struct {
		kind          string
		shared, count bool
		list, limited bool
	}{
		{"content", false, false, false, true},
		{"mine", false, false, false, true},
		{"entries", false, false, true, false},
		{"entries", false, true, true, true},
		{"board", false, false, true, false},
		{"board", false, true, true, true},
		{"", true, false, true, false},
		{"", false, false, false, true},
	} {
		if got := LegacyDataListReadable(tc.kind, tc.shared); got != tc.list {
			t.Errorf("list %q shared=%v: got %v, want %v", tc.kind, tc.shared, got, tc.list)
		}
		if got := LegacyDataReadLimited(tc.kind, tc.shared, tc.count); got != tc.limited {
			t.Errorf("limited %q shared=%v count=%v: got %v, want %v", tc.kind, tc.shared, tc.count, got, tc.limited)
		}
	}
}

func TestLegacyDataReadRoute(t *testing.T) {
	for _, tc := range []struct {
		kind          string
		shared, count bool
		want          LegacyDataRoute
	}{
		{"mine", false, true, LegacyDataPersonal},
		{"content", false, true, LegacyDataContent},
		{"", false, true, LegacyDataUndeclared},
		{"entries", false, true, LegacyDataCount},
		{"board", false, true, LegacyDataCount},
		{"", true, true, LegacyDataCount},
		{"entries", false, false, LegacyDataList},
		{"board", false, false, LegacyDataList},
		{"", true, false, LegacyDataList},
	} {
		if got := LegacyDataReadRoute(tc.kind, tc.shared, tc.count); got != tc.want {
			t.Errorf("route %q shared=%v count=%v: got %v, want %v", tc.kind, tc.shared, tc.count, got, tc.want)
		}
	}
}
