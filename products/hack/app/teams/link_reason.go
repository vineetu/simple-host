package teams

// JoinRefusal is why the previewed team cannot be joined through its link
// ("locked", "full" or "teams_closed"), or "" when it can.
func (p LinkPreview) JoinRefusal() string {
	switch {
	case p.Team.Locked():
		return "locked"
	case p.MemberCount >= p.Event.TeamSizeMax:
		return "full"
	case p.Event.Stage != "open" && p.Event.Stage != "building" || p.DeadlinePassed:
		return "teams_closed"
	}
	return ""
}
