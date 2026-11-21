package entries

import (
	"testing"

	"github.com/vsriram/simple-host/products/hack/model"
)

func TestChecklistSnapshotMatches(t *testing.T) {
	total := 7.5
	current := []TeamResult{{TeamID: "a", Total: &total, ScoreMode: "raw", TieDecider: "criterion", JudgesScored: 2, Track: &model.EventTrack{ID: "t1"}}}
	const row = `"team_id":"a","total":7.5,"raw_total":null,"normalised_total":null,"deciding_mode":"raw","judges_scored":2`
	for _, tc := range []struct {
		name, snapshot string
		want           bool
	}{
		{"same inputs", `[{` + row + `,"tie_decider":"criterion","track":{"id":"t1"}}]`, true},
		{"organiser order hides the decider", `[{` + row + `,"tie_decider":"organiser","track":{"id":"t1"}}]`, true},
		{"different decider", `[{` + row + `,"tie_decider":"majority","track":{"id":"t1"}}]`, false},
		{"different track", `[{` + row + `,"tie_decider":"criterion","track":{"id":"t2"}}]`, false},
		{"no track", `[{` + row + `,"tie_decider":"criterion","track":null}]`, false},
		{"missing key", `[{` + row + `,"track":{"id":"t1"}}]`, false},
		{"repeated team", `[{` + row + `,"tie_decider":"criterion","track":{"id":"t1"}},{"team_id":"a"}]`, false},
		{"other team", `[{"team_id":"b"}]`, false},
		{"not json", `{`, false},
	} {
		if got := checklistSnapshotMatches(current, []byte(tc.snapshot)); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
