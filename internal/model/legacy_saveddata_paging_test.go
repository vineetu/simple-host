package model

import (
	"testing"
)

func TestLegacyHistoryPage(t *testing.T) {
	for _, tc := range []struct {
		limit, before string
		wantLimit     int
		wantBefore    int64
	}{
		{"", "", 50, 0}, {"-1", "-5", 50, 0}, {"201", "42", 200, 42}, {"1", "bad", 1, 0}, {"999999999999999999999", "9223372036854775808", 50, 0},
	} {
		limit, before := LegacyHistoryPage(tc.limit, tc.before)
		if limit != tc.wantLimit || before != tc.wantBefore {
			t.Errorf("page(%q,%q) = %d,%d", tc.limit, tc.before, limit, before)
		}
	}
}

func TestLegacyHistoryNext(t *testing.T) {
	entries := []HistoryEntry{{ID: 7}, {ID: 3}}
	if n := LegacyHistoryNext(entries, 3); n != nil {
		t.Fatalf("partial page cursor = %v", *n)
	}
	if n := LegacyHistoryNext(entries, 2); n == nil || *n != 3 {
		t.Fatalf("full page cursor = %v", n)
	}
	if n := LegacyHistoryNext(nil, 0); n != nil {
		t.Fatalf("empty page cursor = %v", *n)
	}
}
