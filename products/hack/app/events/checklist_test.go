package events

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	rules "github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

func errCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestReadableRole(t *testing.T) {
	for _, tc := range []struct {
		role, member string
		readOnly     bool
		want, code   string
	}{
		{"", "participant", false, "participant", ""},
		{"judge", "organiser", false, "judge", ""},
		{"judge", "organiser", true, "", "checklist_role_forbidden"},
		{"organiser", "judge", false, "", "checklist_role_forbidden"},
		{"", "owner", false, "", "checklist_role_forbidden"},
	} {
		got, err := readableRole(tc.role, tc.member, tc.readOnly)
		if got != tc.want || errCode(err) != tc.code {
			t.Errorf("%q as %q: got %q %v", tc.role, tc.member, got, err)
		}
	}
}

func TestJoinRefusal(t *testing.T) {
	down := model.Event{Stage: "open", TakenDownAt: sql.NullTime{Valid: true, Time: time.Now()}}
	for _, tc := range []struct {
		ev    model.Event
		judge bool
		code  string
	}{
		{model.Event{Stage: "building"}, false, ""},
		{model.Event{Stage: "judging"}, false, "joining_closed"},
		{model.Event{Stage: "judging"}, true, ""},
		{model.Event{Stage: "results"}, true, "judging_closed"},
		{down, false, "event_taken_down"},
	} {
		if got := errCode(joinRefusal(tc.ev, tc.judge)); got != tc.code {
			t.Errorf("%s judge=%v: got %q want %q", tc.ev.Stage, tc.judge, got, tc.code)
		}
	}
}

func TestBlockOrganiserFactsLaterReasonWins(t *testing.T) {
	f := map[string]rules.ChecklistFact{"o.rubric": {Done: true, Version: "v"}}
	ev := model.Event{Stage: "judging", JudgingLockedAt: sql.NullTime{Valid: true}}
	blockOrganiserFacts(ev, false, f)
	for id, want := range map[string]string{"o.rubric": "scores_locked", "o.tie-break": "tie_rule_closed", "o.judges": "judging_setup_locked", "o.scores": "scores_locked", "o.entry-rules": "entry_rules_closed"} {
		if f[id].BlockedReason != want {
			t.Errorf("%s: got %q want %q", id, f[id].BlockedReason, want)
		}
	}
	if !f["o.rubric"].Done || f["o.rubric"].Version != "v" {
		t.Errorf("blocking dropped the fact: %+v", f["o.rubric"])
	}
}

func TestEntryRulesValid(t *testing.T) {
	for raw, want := range map[string]bool{
		``:                                   false,
		`{"fields":{}}`:                      false,
		`{"fields":{"video_url":"bogus"}}`:   false,
		`{"fields":{"nope":"required"}}`:     false,
		`{"fields":{"code_url":"required"}}`: true,
	} {
		if got := entryRulesValid([]byte(raw)); got != want {
			t.Errorf("%s: got %v", raw, got)
		}
	}
}
