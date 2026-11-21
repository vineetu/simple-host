package events

import (
	"testing"
	"time"
)

func TestPageStage(t *testing.T) {
	for _, c := range []struct {
		stage     string
		published bool
		want      string
	}{
		{"building", true, "results"}, {"closed", true, "results"}, {"judging", true, "results"},
		{"judging", false, "judging"}, {"open", true, "open"}, {"archived", true, "archived"},
		{"results", false, "results"},
	} {
		if got := PageStage(c.stage, c.published); got != c.want {
			t.Errorf("PageStage(%q, %v) = %q, want %q", c.stage, c.published, got, c.want)
		}
	}
}

func TestEntryDeadlineState(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	for _, c := range []struct {
		stage                 string
		deadline              time.Time
		countdown, passedWant bool
	}{
		{"open", later, true, false}, {"building", earlier, false, true}, {"building", now, false, true},
		{"closed", later, false, false}, {"open", time.Time{}, false, false},
	} {
		cd, passed := EntryDeadlineState(c.stage, c.deadline, now)
		if cd != c.countdown || passed != c.passedWant {
			t.Errorf("EntryDeadlineState(%q, %v) = %v, %v", c.stage, c.deadline, cd, passed)
		}
	}
}

func TestResultsReplaceStageCard(t *testing.T) {
	for stage, want := range map[string]bool{"results": true, "archived": true, "judging": false, "building": false} {
		if got := ResultsReplaceStageCard(stage, true); got != want {
			t.Errorf("%s: got %v", stage, got)
		}
		if ResultsReplaceStageCard(stage, false) {
			t.Errorf("%s unpublished: replaced", stage)
		}
	}
}
