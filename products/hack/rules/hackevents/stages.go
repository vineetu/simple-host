package hackevents

func SubmissionsClosedStage(stage string) bool {
	return stage == "closed" || stage == "judging" || stage == "results"
}
