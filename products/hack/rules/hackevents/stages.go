package hackevents

// SubmissionsClosedStage reports whether the stage closes submissions.
func SubmissionsClosedStage(stage string) bool {
	return stage == "closed" || stage == "judging" || stage == "results"
}
