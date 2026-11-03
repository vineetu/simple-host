package hackteams

// ParticipantTeamStages reports whether participants may change teams.
func ParticipantTeamStages(stage string) bool {
	return stage == "open" || stage == "building"
}
