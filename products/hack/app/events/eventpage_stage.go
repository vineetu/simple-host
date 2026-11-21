package events

import "time"

// PageStage is the stage the public event page lays itself out for.
func PageStage(stage string, resultsPublished bool) string {
	switch stage {
	case "building", "closed", "judging":
		if resultsPublished {
			return "results"
		}
	}
	return stage
}

// EntryDeadlineState is whether the stage card counts down to the entry deadline, or says plainly that it has passed.
func EntryDeadlineState(stage string, deadline, now time.Time) (countdown, passed bool) {
	if deadline.IsZero() || (stage != "open" && stage != "building") {
		return false, false
	}
	return now.Before(deadline), !now.Before(deadline)
}

// ResultsReplaceStageCard is true when the published results block takes the stage card's place: on the results and archived stages only.
func ResultsReplaceStageCard(stage string, resultsPublished bool) bool {
	return resultsPublished && (stage == "results" || stage == "archived")
}
