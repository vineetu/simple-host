package teams

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
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

const organiserTakedownPrefix = "Taken down by the event organiser"

func (g SiteGate) Suspension(ctx context.Context, tx *sql.Tx, site model.Site) (string, string, error) {
	down, reason, err := repo.LockTeamSiteSuspension(ctx, tx, site.UserID, site.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if !down {
		return organiserTakedownPrefix, "", nil
	}
	full := organiserTakedownPrefix
	if reason != "" {
		full += ": " + reason
	}
	return organiserTakedownPrefix, full, nil
}

// Check decides in the deploy's transaction whether user may change the site: organisers the event website until it ends, teammates their team's site until its deadline.
func (g SiteGate) Check(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *sites.GateError {
	ev, err := eventrepo.GetEventByAccount(ctx, tx, user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return &sites.GateError{Message: "internal server error"}
	}
	if strings.EqualFold(siteName, ev.ID) {
		switch {
		case ev.TakenDownAt.Valid:
			return &sites.GateError{Status: 403, Code: "event_taken_down", Message: "this event has been taken down"}
		case ev.Stage == "archived":
			return &sites.GateError{Code: "event_closed", Message: "this event has ended"}
		}
		return nil
	}
	if user.Member.Site == "" {
		return &sites.GateError{Status: 403, Code: "organiser_read_only", Message: "organisers read team sites but do not change them; take a site down or change the team's deadline instead"}
	}
	g.PaperSite(ctx, user, siteName)
	st, err := repo.TeamWriteStateBySlug(ctx, tx, ev.ID, siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return &sites.GateError{Status: 404, Code: "event_not_found", Message: "event not found"}
	}
	if err != nil {
		return &sites.GateError{Message: "internal server error"}
	}
	g.PaperState(ctx, user, st.Stage)
	if code := st.WriteRefusal(); code != "" {
		return &sites.GateError{Status: httpx.RefusalStatus(code, http.StatusConflict), Message: hackevents.TeamRefusalMessage(code), Code: code}
	}
	if !g.TeamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			g.RequestCert(user.Handle.String)
		}
		return &sites.GateError{Message: "your event's team addresses are still being set up; try again in a few minutes", Code: "team_sites_not_ready"}
	}
	return nil
}
