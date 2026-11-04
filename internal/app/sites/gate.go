package sites

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	model "github.com/vsriram/simple-host/internal/model"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	siterules "github.com/vsriram/simple-host/internal/rules/sites"
)

func (s *Service) createGate(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *GateError {
	if s.deps.TeamKeys && user.EventWebsite != nil {
		id := user.EventWebsite
		if siteName != id.EventID || user.ID == "" {
			return &GateError{Code: "event_not_found", Message: "event not found"}
		}
		role, stage, takenDown, err := repo.LockEventOrganiser(ctx, tx, id.EventID, id.EventSlug, user.ID, id.OrganiserID)
		if err != nil || role != "organiser" {
			return &GateError{Code: "event_not_found", Message: "event not found"}
		}
		if takenDown {
			return &GateError{Code: "event_taken_down", Message: "this event has been taken down"}
		}
		if stage == "archived" {
			return &GateError{Code: "event_closed", Message: "this event has ended"}
		}
		return nil
	}
	if !s.deps.TeamKeys || user.Team == nil {
		return nil
	}
	if !strings.EqualFold(siteName, user.Team.TeamSlug) {
		return &GateError{Message: "this team key works on your team's site only: " + user.Team.TeamSlug, Code: "team_site_only"}
	}
	if err := repo.LockApprovedTeamMember(ctx, tx, user.Team.MemberID, user.Team.TeamID); err != nil {
		return &GateError{Code: "event_not_found", Message: "event not found"}
	}
	s.deps.PaperSite(ctx, user, siteName)
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, user.Team.TeamID, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &GateError{Message: "this team key stopped working: its team is gone", Code: "team_key_inactive"}
		}
		return &GateError{Message: "internal server error"}
	}
	s.deps.PaperState(ctx, user, st.Stage)
	if code := st.WriteRefusal(); code != "" {
		msg := siterules.TeamRefusalMessage(code)
		return &GateError{Message: msg, Code: code}
	}
	if !s.deps.TeamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			s.deps.RequestCert(user.Handle.String)
		}
		return &GateError{Message: "your event's team addresses are still being set up; try again in a few minutes", Code: "team_sites_not_ready"}
	}
	return nil
}
