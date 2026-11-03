package teams

import (
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
	Track            *db.EventTrack
	YouStatus        string
	OtherTeamName    string
	HasOtherTeamName bool
}

// GetTeamLink reads a team link and the caller's relationship to the team.
func (s *Service) GetTeamLink(ctx context.Context, code, userID string) (LinkPreview, error) {
	var out LinkPreview
	team, err := db.GetEventTeamByCodeOnly(ctx, s.db, code)
	if errors.Is(err, sql.ErrNoRows) {
		return out, &Error{Code: "team_link_not_found"}
	}
	if err != nil {
		return out, err
	}
	ev, err := db.GetEventByID(ctx, s.db, team.EventID)
	if err != nil {
		return out, err
	}
	if ev.TakenDown() {
		return out, &Error{Code: "team_link_not_found"}
	}
	count, err := db.CountTeamMembers(ctx, s.db, team.ID)
	if err != nil {
		return out, err
	}
	deadline, err := s.teamDeadlinePassed(ctx, team.ID)
	if err != nil {
		return out, err
	}
	track, trackErr := db.TeamTrack(ctx, s.db, team.ID)
	if trackErr != nil {
		track = nil
	}
	out = LinkPreview{Event: ev, Team: team, MemberCount: count, DeadlinePassed: deadline, Track: track}
	if userID == "" {
		return out, nil
	}
	member, err := db.GetEventMember(ctx, s.db, ev.ID, userID)
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
	status, err := db.MemberApprovalStatus(ctx, s.db, ev.ID, userID)
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
	other, err := db.GetEventTeamByID(ctx, s.db, member.TeamID.String)
	if err == nil {
		out.OtherTeamName = other.Name
		out.HasOtherTeamName = true
	}
	return out, nil
}
