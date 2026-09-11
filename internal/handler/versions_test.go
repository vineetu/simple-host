package handler

import "testing"

func TestPruneThreshold(t *testing.T) {
	cases := []struct {
		name         string
		active, keep int
		wantFrom     int
		wantPrune    bool
	}{
		{"keep everything", 9, 0, 0, false},
		{"first deploy has no history", 1, 1, 0, false},
		{"second deploy, keeping one", 2, 1, 2, true},
		{"tenth deploy, keeping one", 10, 1, 10, true},
		{"tenth deploy, keeping three", 10, 3, 8, true},
		{"keeping more than exists", 3, 10, 0, false},
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

func TestVersionCopiesPerSite(t *testing.T) {
	before := keepVersions
	t.Cleanup(func() { keepVersions = before })

	// `current` is a full copy of the active version, not a link, so a site
	// always costs one more than its retained history. Capacity sizing divides
	// by this number; an off-by-one here is an off-by-one on every event.
	for keep, want := range map[int]int{1: 2, 3: 4, 10: 11} {
		keepVersions = keep
		if got := versionCopiesPerSite(); got != want {
			t.Errorf("keepVersions %d: copies = %d, want %d", keep, got, want)
		}
	}
	keepVersions = 0
	if got := versionCopiesPerSite(); got != 11 {
		t.Errorf("unbounded history: copies = %d, want the 11 planning estimate", got)
	}
}
