package hackteams

func ParticipantTeamStages(stage string) bool {
	return stage == "open" || stage == "building"
}
