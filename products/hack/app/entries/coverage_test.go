package entries

import (
	"testing"

	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

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

func TestCoverageFixTarget(t *testing.T) {
	judges := []model.NamedJudge{{ID: "j"}}
	short := []CoverageShort{{TeamID: "t", Slug: "team-t", MissingTrack: true}}
	for _, tc := range []struct {
		name     string
		in       coverageInput
		short    []CoverageShort
		want     string
		wantTeam string
	}{
		{"no judges", coverageInput{mode: "manual"}, short, "o.organiser-judges", ""},
		{"conflict wins", coverageInput{mode: "panel", judges: judges, conflicts: map[string]bool{hackentries.JudgeTeamKey("j", "t"): true}}, short, "o.conflicts", ""},
		{"manual", coverageInput{mode: "manual", judges: judges}, nil, "o.assignments", ""},
		{"panel without tracks", coverageInput{mode: "panel", judges: judges}, short, "o.tracks", ""},
		{"panel team without track", coverageInput{mode: "panel", judges: judges, eventTracks: 1}, short, "o.team-track", "team-t"},
		{"panel", coverageInput{mode: "panel", judges: judges, eventTracks: 1}, nil, "o.panel-assignments", ""},
		{"automatic", coverageInput{mode: "automatic", judges: judges}, short, "o.organiser-judges", ""},
	} {
		got, team := tc.in.fixTarget(tc.short)
		if got != tc.want || team != tc.wantTeam {
			t.Errorf("%s: got %q %q, want %q %q", tc.name, got, team, tc.want, tc.wantTeam)
		}
	}
}
