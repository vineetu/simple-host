package db

import (
	"database/sql"
	"testing"
	"time"
)

func TestTeamWriteStateJudgingFreezesExtensions(t *testing.T) {
	now := time.Now()
	for _, stage := range []string{"building", "closed", "judging", "results", "archived"} {
		for _, deadline := range []sql.NullTime{{}, {Time: now.Add(time.Hour), Valid: true}} {
			s := TeamWriteState{Stage: stage, Deadline: deadline, Now: now, HasOverride: true}
			frozen := stage == "judging" || stage == "results" || stage == "archived"
			if s.Frozen() != frozen {
				t.Errorf("%s deadline %v: Frozen = %v", stage, deadline, s.Frozen())
			}
			want := ""
			if frozen {
				want = "submissions_closed"
			}
			if stage == "archived" {
				want = "event_closed"
			}
			if s.WriteRefusal() != want {
				t.Errorf("%s: refusal %q, want %q", stage, s.WriteRefusal(), want)
			}
		}
	}
}
