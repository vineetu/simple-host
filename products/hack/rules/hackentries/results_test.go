package hackentries

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/model"
)

// TestComputeResultsTieRules pins ranking and tie-breaking: total, then the
// tie criterion's mean, then judges' pairwise majority, else unresolved.
func TestComputeResultsTieRules(t *testing.T) {
	scores := map[string][][3]int{ // team: {judge, c1, c2}
		"A": {{1, 10, 10}, {2, 10, 10}},
		"B": {{1, 8, 2}, {2, 8, 2}},
		"C": {{1, 2, 8}, {2, 2, 8}},
		"D": {{1, 5, 4}, {2, 5, 4}, {3, 5, 1}},
		"E": {{1, 5, 3}, {2, 5, 3}, {3, 5, 3}},
		"F": {{1, 3, 3}},
		"H": {{1, 3, 3}},
	}
	in := ResultsInput[string]{
		Rubric:          []model.RubricCriterion{{ID: "c1", Weight: 1, MaxPoints: 10}, {ID: "c2", Weight: 1, MaxPoints: 10}},
		Eligible:        map[string]map[string]bool{},
		Tracks:          map[string]*string{"A": new(string), "B": new(string)},
		TrackIDs:        map[string]string{"A": "t", "B": "t"},
		TieCriterionID:  "c1",
		HasTieCriterion: true,
	}
	for _, name := range []string{"G", "H", "F", "E", "D", "C", "B", "A"} {
		in.Teams = append(in.Teams, model.NamedTeam{ID: name, Name: name})
		for _, s := range scores[name] {
			judge := fmt.Sprint("J", s[0])
			if in.Eligible[judge] == nil {
				in.Eligible[judge] = map[string]bool{}
			}
			in.Eligible[judge][name] = true
			for i, c := range []string{"c1", "c2"} {
				in.Raw = append(in.Raw, model.RawScoreRow{JudgeID: judge, TeamID: name, CriterionID: c, Points: s[i+1]})
			}
		}
	}
	var got []string
	for _, r := range ComputeResults(in) {
		row := r.TeamID + " -"
		if r.Rank != nil {
			row = fmt.Sprintf("%s %d %.2f %s tied=%v", r.TeamID, *r.Rank, *r.Total, r.TieDecider, r.Tied)
		}
		if r.TrackRank != nil {
			row += fmt.Sprintf(" track=%d winner=%v", *r.TrackRank, r.TrackWinner)
		}
		got = append(got, row)
	}
	want := strings.Join([]string{
		"A 1 2.00 score tied=false track=1 winner=true",
		"B 2 1.00 score tied=false track=2 winner=false",
		"C 3 1.00 criterion tied=false",
		"D 4 0.80 score tied=false",
		"E 5 0.80 judge_majority tied=false",
		"F 6 0.60 unresolved tied=true",
		"H 6 0.60 unresolved tied=true",
		"G -",
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
}

// TestApplyRankOverrides pins organiser tie choices: only named groups are
// reordered and re-ranked; untouched ties stay tied.
func TestApplyRankOverrides(t *testing.T) {
	rows := func() []TeamResult[string] {
		var out []TeamResult[string]
		for _, r := range []struct {
			id   string
			rank int
			tied bool
		}{{"A", 1, false}, {"B", 2, true}, {"C", 2, true}, {"D", 3, false}, {"E", 4, true}, {"F", 4, true}, {"G", 0, false}} {
			row := TeamResult[string]{TeamID: r.id, TieDecider: "score", Tied: r.tied}
			if r.rank > 0 {
				rank, total := r.rank, 1.0
				row.Rank, row.Total = &rank, &total
			}
			out = append(out, row)
		}
		return out
	}
	results := rows()
	if err := ApplyRankOverrides(results, []RankOverride{{"B", 2}, {"C", 1}}, nil); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range results {
		row := r.TeamID + " -"
		if r.Rank != nil {
			row = fmt.Sprintf("%s %d %s tied=%v", r.TeamID, *r.Rank, r.TieDecider, r.Tied)
		}
		got = append(got, row)
	}
	want := "A 1 score tied=false|C 2 organiser tied=false|B 3 organiser tied=false|D 4 score tied=false|E 5 score tied=true|F 5 score tied=true|G -"
	if strings.Join(got, "|") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, "|"), want)
	}
	for _, tc := range []struct {
		overrides []RankOverride
		msg       string
	}{
		{[]RankOverride{{"D", 1}}, "D is not part of a tie."},
		{[]RankOverride{{"B", 1}, {"B", 2}}, "name each tied team once"},
		{[]RankOverride{{"C", 1}}, "give every tied team in a group a rank, or none of them"},
		{[]RankOverride{{"B", 1}, {"C", 1}}, "tied teams need different ranks from each other"},
	} {
		if err := ApplyRankOverrides(rows(), tc.overrides, nil); err == nil || err.Error() != tc.msg {
			t.Errorf("%v: got %v, want %q", tc.overrides, err, tc.msg)
		}
	}
}
