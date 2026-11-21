package entries

import (
	"database/sql"
	"errors"
	"testing"

	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func TestJudgingSettingsRefusal(t *testing.T) {
	mode, tie := "manual", "c1"
	locked := sql.NullTime{Valid: true}
	for _, tc := range []struct {
		ev   hackmodel.Event
		req  JudgingSettings
		want string
	}{
		{hackmodel.Event{Stage: "archived"}, JudgingSettings{}, "event_closed"},
		{hackmodel.Event{Stage: "closed", JudgingLockedAt: locked}, JudgingSettings{Mode: &mode}, "scores_locked"},
		{hackmodel.Event{Stage: "judging"}, JudgingSettings{Mode: &mode}, "judging_setup_locked"},
		{hackmodel.Event{Stage: "results"}, JudgingSettings{TieCriterionID: &tie}, "tie_rule_closed"},
		{hackmodel.Event{Stage: "results", JudgingLockedAt: locked}, JudgingSettings{}, ""},
		{hackmodel.Event{Stage: "closed"}, JudgingSettings{Mode: &mode, TieCriterionID: &tie}, ""},
	} {
		got := ""
		var e *Error
		if err := judgingSettingsRefusal(tc.ev, tc.req); errors.As(err, &e) {
			got = e.Code
		}
		if got != tc.want {
			t.Errorf("%s %+v: got %q, want %q", tc.ev.Stage, tc.req, got, tc.want)
		}
	}
}
