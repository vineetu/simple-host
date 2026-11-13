package hackevents

// LeaveCaseFor names which of the six outcomes applies to one membership,
// from role, approval status and how many other organisers the event has.
// A pending or rejected participant is the same "applicant" case either
// way: withdrawing and leaving a rejected application do the same thing
// (delete the row), so there is nothing left to tell them apart on. An
// organiser with nobody else left is last_organiser whether or not they
// are the creator; a creator with others is creator; any other organiser
// with others is co_organiser.
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
