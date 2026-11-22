package teams

import eventapp "github.com/vsriram/simple-host/products/hack/app/events"

// OrganiserMessage checks an organiser's message: event not archived, 1 to 25 distinct team ids, 1 to 1000 characters.
func OrganiserMessage(stage string, teamIDs []string, text string) (string, error) {
	if err := organiserStage(stage, msgEnded); err != nil {
		return "", err
	}
	if len(teamIDs) < 1 || len(teamIDs) > 25 {
		return "", &Error{Code: "invalid_team", Message: "send 1 to 25 team ids"}
	}
	seen := map[string]bool{}
	for _, id := range teamIDs {
		if !IsID(id) || seen[id] {
			return "", &Error{Code: "invalid_team", Message: "team_ids must be distinct ids of this event's teams"}
		}
		seen[id] = true
	}
	return messageText(text)
}

// TeamMessage checks a team's message to the organisers: event not archived, 1 to 1000 characters.
func TeamMessage(stage, text string) (string, error) {
	if err := organiserStage(stage, msgEnded); err != nil {
		return "", err
	}
	return messageText(text)
}

func messageText(s string) (string, error) {
	v, e := eventapp.Text(s, "text", 1, 1000)
	if e != nil {
		return "", &Error{Code: e.Code, Message: e.Message}
	}
	return v, nil
}
