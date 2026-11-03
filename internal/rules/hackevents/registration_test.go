package hackevents

import (
	"strings"
	"testing"
)

func TestTrackSlugLength(t *testing.T) {
	for _, n := range []int{1, 32, 79, 80, 81} {
		if got := TrackSlug(strings.Repeat("a", n)); got != (n <= 80) {
			t.Errorf("length %d: %v", n, got)
		}
	}
}
