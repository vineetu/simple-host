package teams

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	sites "github.com/vsriram/simple-host/internal/app/sites"
	model "github.com/vsriram/simple-host/internal/model"
	siterules "github.com/vsriram/simple-host/internal/rules/sites"
	repo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// SiteGate keeps Hack's transaction-time publishing checks and callbacks together.
type SiteGate struct {
	TeamSitesReady func(string) bool
	RequestCert    func(string)
	PaperSite      func(context.Context, *model.User, string)
	PaperState     func(context.Context, *model.User, string)
}

var _ sites.GroupGate = SiteGate{}

func (g SiteGate) Check(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *sites.GateError {
	if user.EventWebsite != nil {
		id := user.EventWebsite
		if siteName != id.EventID || user.ID == "" {
			return &sites.GateError{Code: "event_not_found", Message: "event not found"}
		}
		role, stage, takenDown, err := repo.LockEventOrganiser(ctx, tx, id.EventID, id.EventSlug, user.ID, id.OrganiserID)
		if err != nil || role != "organiser" {
			return &sites.GateError{Code: "event_not_found", Message: "event not found"}
		}
		if takenDown {
			return &sites.GateError{Code: "event_taken_down", Message: "this event has been taken down"}
		}
		if stage == "archived" {
			return &sites.GateError{Code: "event_closed", Message: "this event has ended"}
		}
		return nil
	}
	if user.Team == nil {
		return nil
	}
	if !strings.EqualFold(siteName, user.Team.TeamSlug) {
		return &sites.GateError{Message: "this team key works on your team's site only: " + user.Team.TeamSlug, Code: "team_site_only"}
	}
	if err := repo.LockApprovedTeamMember(ctx, tx, user.Team.MemberID, user.Team.TeamID); err != nil {
		return &sites.GateError{Code: "event_not_found", Message: "event not found"}
	}
	g.PaperSite(ctx, user, siteName)
	st, err := repo.TeamWriteStateFor(ctx, tx, user.Team.TeamID, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &sites.GateError{Message: "this team key stopped working: its team is gone", Code: "team_key_inactive"}
		}
		return &sites.GateError{Message: "internal server error"}
	}
	g.PaperState(ctx, user, st.Stage)
	if code := st.WriteRefusal(); code != "" {
		msg := siterules.TeamRefusalMessage(code)
		return &sites.GateError{Message: msg, Code: code}
	}
	if !g.TeamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			g.RequestCert(user.Handle.String)
		}
		return &sites.GateError{Message: "your event's team addresses are still being set up; try again in a few minutes", Code: "team_sites_not_ready"}
	}
	return nil
}
