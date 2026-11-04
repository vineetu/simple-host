package sites

// TeamRefusalMessage returns the message for a team write refusal.
func TeamRefusalMessage(code string) string {
	switch code {
	case "event_taken_down":
		return "this event has been taken down"
	case "event_closed":
		return "this event has ended, so its team sites no longer change"
	case "team_site_taken_down":
		return "the organiser has taken your team's site down; ask them about it"
	case "submissions_closed":
		return "the submission deadline has passed, so your team's site and entry are frozen. Ask the organiser if you need more time"
	}
	return "your team cannot change its site now"
}
