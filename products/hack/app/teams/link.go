package teams

import (
	"context"
	"database/sql"
	"errors"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type LinkPreview struct {
	Event            model.Event
	Team             model.EventTeam
	MemberCount      int
	DeadlinePassed   bool
	Track            *model.EventTrack
	YouStatus        string
	OtherTeamName    string
	HasOtherTeamName bool
}

func errLinkNotFound() error {
	return &Error{Code: "team_link_not_found", Message: "that link or code doesn't match a team in this event"}
}

// GetTeamLink reads a team link and the caller's relationship to the team
// (userID empty: signed out, no status).
func (s *Service) GetTeamLink(ctx context.Context, code, userID string) (LinkPreview, error) {
	out, err := s.linkTeam(ctx, code)
	if err != nil || userID == "" {
		return out, err
	}
	err = s.linkStatus(ctx, &out, userID)
	return out, err
}

// linkTeam reads the team a code names, with its event, size, deadline and
// track; an empty code or a taken-down event is team_link_not_found.
func (s *Service) linkTeam(ctx context.Context, code string) (LinkPreview, error) {
	if code == "" {
		return LinkPreview{}, errLinkNotFound()
	}
	team, err := teamrepo.GetEventTeamByCodeOnly(ctx, s.db, code)
	if err != nil {
		return LinkPreview{}, notFoundAs(err, errLinkNotFound())
	}
	ev, err := eventrepo.GetEventByID(ctx, s.db, team.EventID)
	if err != nil {
		return LinkPreview{}, err
	}
	if ev.TakenDown() {
		return LinkPreview{}, errLinkNotFound()
	}
	out := LinkPreview{Event: ev, Team: team}
	if out.MemberCount, err = teamrepo.CountTeamMembers(ctx, s.db, team.ID); err != nil {
		return LinkPreview{}, err
	}
	if out.DeadlinePassed, err = s.teamDeadlinePassed(ctx, team.ID); err != nil {
		return LinkPreview{}, err
	}
	if track, err := eventrepo.TeamTrack(ctx, s.db, team.ID); err == nil {
		out.Track = track
	}
	return out, nil
}

// linkStatus sets the caller's status: not_in_event, pending, rejected,
// judge, organiser, no_team, on_this_team or on_other_team (with that
// team's name). A lookup failure never becomes not_in_event: only a
// missing row proves it.
func (s *Service) linkStatus(ctx context.Context, out *LinkPreview, userID string) error {
	member, err := eventrepo.GetEventMember(ctx, s.db, out.Event.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		out.YouStatus = "not_in_event"
		return nil
	}
	if err != nil {
		return err
	}
	if member.Role == "organiser" || member.Role == "judge" {
		out.YouStatus = member.Role
		return nil
	}
	status, err := eventrepo.MemberApprovalStatus(ctx, s.db, out.Event.ID, userID)
	switch {
	case err != nil:
		return err
	case status == "pending" || status == "rejected":
		out.YouStatus = status
	case !member.TeamID.Valid:
		out.YouStatus = "no_team"
	case member.TeamID.String == out.Team.ID:
		out.YouStatus = "on_this_team"
	default:
		out.YouStatus = "on_other_team"
		if other, err := teamrepo.GetEventTeamByID(ctx, s.db, member.TeamID.String); err == nil {
			out.OtherTeamName, out.HasOtherTeamName = other.Name, true
		}
	}
	return nil
}
