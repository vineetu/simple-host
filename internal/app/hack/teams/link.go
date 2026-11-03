package teams

import (
	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"

	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/internal/db"
)

// LinkPreview contains the data needed to display a team link.
type LinkPreview struct {
	Event            db.Event
	Team             db.EventTeam
	MemberCount      int
	DeadlinePassed   bool
	Track            *model.EventTrack
	YouStatus        string
	OtherTeamName    string
	HasOtherTeamName bool
}

// GetTeamLink reads a team link and the caller's relationship to the team.
func (s *Service) GetTeamLink(ctx context.Context, code, userID string) (LinkPreview, error) {
	var out LinkPreview
	team, err := teamrepo.GetEventTeamByCodeOnly(ctx, s.db, code)
	if errors.Is(err, sql.ErrNoRows) {
		return out, &Error{Code: "team_link_not_found"}
	}
	if err != nil {
		return out, err
	}
	ev, err := eventrepo.GetEventByID(ctx, s.db, team.EventID)
	if err != nil {
		return out, err
	}
	if ev.TakenDown() {
		return out, &Error{Code: "team_link_not_found"}
	}
	count, err := teamrepo.CountTeamMembers(ctx, s.db, team.ID)
	if err != nil {
		return out, err
	}
	deadline, err := s.teamDeadlinePassed(ctx, team.ID)
	if err != nil {
		return out, err
	}
	track, trackErr := eventrepo.TeamTrack(ctx, s.db, team.ID)
	if trackErr != nil {
		track = nil
	}
	out = LinkPreview{Event: ev, Team: team, MemberCount: count, DeadlinePassed: deadline, Track: track}
	// Signed-out callers have no status. Otherwise distinguish not_in_event,
	// pending, rejected, judge, organiser, no_team, on_this_team and on_other_team.
	// A lookup failure must not become not_in_event: only a missing row proves it.
	if userID == "" {
		return out, nil
	}
	member, err := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		out.YouStatus = "not_in_event"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	switch member.Role {
	case "organiser", "judge":
		out.YouStatus = member.Role
		return out, nil
	}
	status, err := eventrepo.MemberApprovalStatus(ctx, s.db, ev.ID, userID)
	if err != nil {
		return out, err
	}
	if status == "pending" || status == "rejected" {
		out.YouStatus = status
		return out, nil
	}
	if !member.TeamID.Valid {
		out.YouStatus = "no_team"
		return out, nil
	}
	if member.TeamID.String == team.ID {
		out.YouStatus = "on_this_team"
		return out, nil
	}
	out.YouStatus = "on_other_team"
	other, err := teamrepo.GetEventTeamByID(ctx, s.db, member.TeamID.String)
	if err == nil {
		out.OtherTeamName = other.Name
		out.HasOtherTeamName = true
	}
	return out, nil
}
