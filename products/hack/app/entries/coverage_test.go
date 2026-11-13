package entries

import "testing"

func TestCoverageDetail(t *testing.T) {
	for _, tc := range []struct {
		coverage SetupCoverage
		want     string
	}{
		{SetupCoverage{Target: 2, Min: 3}, "Every team has at least 3 judges"},
		{SetupCoverage{Target: 2, Min: 1}, "Every team has at least 1 judge"},
		{SetupCoverage{Target: 2, Short: []CoverageShort{{Name: "Team A", Have: 1}}}, "Short: Team A (1 of 2)"},
	} {
		if got := tc.coverage.Detail(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
