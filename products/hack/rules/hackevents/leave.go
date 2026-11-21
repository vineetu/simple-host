package hackevents

// LeaveCaseFor names which of the six outcomes applies to one membership, from role, approval status and how many other organisers the event has.
func LeaveCaseFor(role, approvalStatus string, isCreator bool, otherOrganisers int) string {
	switch role {
	case "participant":
		if approvalStatus == "pending" || approvalStatus == "rejected" {
			return "applicant"
		}
		return "participant"
	case "judge":
		return "judge"
	case "organiser":
		if otherOrganisers == 0 {
			return "last_organiser"
		}
		if isCreator {
			return "creator"
		}
		return "co_organiser"
	}
	return ""
}
