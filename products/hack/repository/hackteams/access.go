package hackteams

import (
	"context"

	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

// EventAccess is who may act on the event's sites, as Simple Hack decides
// it: the organisers (members of the event's account), and for each team
// its approved participants (editors of the team's site).
func EventAccess(ctx context.Context, q Querier, eventID string) (accountID string, organisers []string, editors map[string][]string, err error) {
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil {
		return "", nil, nil, err
	}
	accountID = ev.AccountID
	members, err := hackmembers.Members(ctx, q, hackmembers.Filter{EventID: eventID})
	if err != nil {
		return "", nil, nil, err
	}
	teams, err := ListEventTeams(ctx, q, eventID)
	if err != nil {
		return "", nil, nil, err
	}
	slugs := map[string]string{}
	for _, t := range teams {
		slugs[t.ID] = t.Slug
	}
	editors = map[string][]string{}
	for _, m := range members {
		switch {
		case m.Organiser():
			organisers = append(organisers, m.UserID)
		case m.Participant() && m.Approved() && m.TeamID.Valid:
			editors[slugs[m.TeamID.String]] = append(editors[slugs[m.TeamID.String]], m.UserID)
		}
	}
	return accountID, organisers, editors, nil
}
