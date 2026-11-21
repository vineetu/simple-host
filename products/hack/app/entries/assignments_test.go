package entries

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
)

// errCode is the refusal code of err, or "" for nil.
func errCode(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if err != nil && !errors.As(err, &e) {
		t.Fatalf("not a refusal: %v", err)
	}
	if e == nil {
		return ""
	}
	return e.Code
}

func TestGenerateRefusal(t *testing.T) {
	auto := func(stage string) model.Event { return model.Event{Stage: stage, JudgeAssignmentMode: "automatic"} }
	locked := auto("results")
	locked.JudgingLockedAt = sql.NullTime{Time: time.Now(), Valid: true}
	for _, tc := range []struct {
		ev                 model.Event
		atMove, organisers bool
		want               string
	}{
		{auto("closed"), false, true, ""},
		{auto("archived"), false, false, "event_closed"},
		{locked, false, false, "scores_locked"},
		{auto("judging"), true, false, ""},
		{auto("judging"), false, false, "judging_setup_locked"},
		{auto("judging"), true, true, "judging_setup_locked"},
		{auto("results"), false, false, ""},
		{auto("results"), false, true, "judging_setup_locked"},
		{model.Event{Stage: "closed", JudgeAssignmentMode: "manual"}, false, false, "invalid_request"},
	} {
		if got := errCode(t, generateRefusal(tc.ev, tc.atMove, tc.organisers)); got != tc.want {
			t.Errorf("%s move=%v organisers=%v: got %q, want %q", tc.ev.Stage, tc.atMove, tc.organisers, got, tc.want)
		}
	}
}

func TestPoolTeams(t *testing.T) {
	teams := []model.NamedTeam{{ID: "a"}, {ID: "b"}}
	for _, tc := range []struct {
		judges int
		teams  []model.NamedTeam
		pool   map[string]bool
		want   string
		n      int
	}{
		{0, teams, map[string]bool{"a": true}, "too_few_judges", 0},
		{1, nil, nil, "too_few_teams", 0},
		{1, teams, nil, "too_few_teams", 0},
		{1, teams, map[string]bool{"b": true}, "", 1},
	} {
		got, err := poolTeams(tc.judges, tc.teams, tc.pool)
		if errCode(t, err) != tc.want || len(got) != tc.n {
			t.Errorf("poolTeams(%d, %v): got %v %v", tc.judges, tc.pool, got, err)
		}
	}
}

func TestParseScores(t *testing.T) {
	pts := func(f float64) *float64 { return &f }
	crit := map[string]int{"c": 5}
	for _, tc := range []struct {
		in   []ScoreInput
		want string
	}{
		{[]ScoreInput{{CriterionID: "c", Points: pts(5)}}, ""},
		{[]ScoreInput{{CriterionID: "x", Points: pts(1)}}, "criterion_not_found"},
		{[]ScoreInput{{CriterionID: "c", Points: pts(6)}}, "invalid_points"},
		{[]ScoreInput{{CriterionID: "c", Points: pts(1.5)}}, "invalid_points"},
	} {
		got, err := parseScores(crit, tc.in)
		if errCode(t, err) != tc.want || (err == nil && got[0].Points != int(*tc.in[0].Points)) {
			t.Errorf("%v: got %v %v", tc.in, got, err)
		}
	}
}
