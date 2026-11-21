package siteshttp

import "testing"

func TestPruneThreshold(t *testing.T) {
	cases := []struct {
		name         string
		active, keep int
		wantFrom     int
		wantPrune    bool
	}{
		{"keep everything", 9, 0, 0, false},
		{"second deploy, keeping one", 2, 1, 2, true},
		{"tenth deploy, keeping three", 10, 3, 8, true},
		// After a rollback the active version is not the newest. The threshold
		// sits at the active version, so nothing above it is a candidate — the
		// SQL then excludes the active row itself and deletes only what is older.
		{"rolled back to v3 of v10", 3, 1, 3, true},
	}
	for _, c := range cases {
		gotFrom, gotPrune := pruneThreshold(c.active, c.keep)
		if gotFrom != c.wantFrom || gotPrune != c.wantPrune {
			t.Errorf("%s: pruneThreshold(%d, %d) = (%d, %v), want (%d, %v)",
				c.name, c.active, c.keep, gotFrom, gotPrune, c.wantFrom, c.wantPrune)
		}
	}
}
