package entries

import "testing"

func TestOverallPrizeWinners(t *testing.T) {
	prizes := []string{"", "$200", "$100"}
	for _, tc := range []struct {
		rank  int
		label string
	}{{0, ""}, {1, ""}, {2, "2nd · $200"}, {3, "3rd · $100"}, {4, ""}} {
		if got := OverallPrizeLabel(tc.rank, prizes); got != tc.label {
			t.Errorf("rank %d: %q", tc.rank, got)
		}
	}
	for rank := 1; rank <= 4; rank++ {
		for _, track := range []bool{false, true} {
			if got := ShownInWinnersOnly(rank, track, nil); got != (rank == 1 || track) {
				t.Errorf("no prizes changed rank %d track %v", rank, track)
			}
		}
	}
}
