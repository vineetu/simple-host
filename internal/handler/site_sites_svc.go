package handler

import (
	"context"
	"database/sql"
	"strings"

	"errors"
	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/sites"
)

func (h *SiteHandler) sitesSvc() *sites.Service {
	return sites.New(h.database, h.disk, h.lockSite, h.previewAccounts, h.previewTTL)
}

// writeSitesError maps site workflow failures to their existing HTTP responses.
func writeSitesError(w http.ResponseWriter, err error, siteName string) {
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		switch siteErr.Code {
		case "site_not_found":
			writeSiteNotFound(w, siteName)
			return
		case "site_suspended":
			writeSiteSuspended(w, siteErr.Message)
			return
		case "not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: siteErr.Message})
			return
		case "version_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found", Code: "version_not_found"})
			return
		case "rollback_site_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		case "rollback_version_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
			return
		case "version_live", "version_published":
			writeJSON(w, http.StatusConflict, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return
		case "marker_failed":
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: siteErr.Message})
			return
		}
	}
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}

// rollbackGateResult carries the handler's transaction-time authorization decision.
type rollbackGateResult struct {
	status  int
	message string
	code    string
}

func (e *rollbackGateResult) Error() string { return e.message }

// rollbackTeamGate repeats the deploy gate with plain request facts so the
// service can keep the transaction open around the authorization check.
func (h *SiteHandler) rollbackTeamGate(ctx context.Context, tx *sql.Tx, user *db.User, siteName string) error {
	notFound := func() error { return &rollbackGateResult{http.StatusNotFound, "event not found", "event_not_found"} }
	if h.ed.Identity().TeamKeys && user.EventWebsite != nil {
		id := user.EventWebsite
		if siteName != id.EventID || user.ID == "" {
			return notFound()
		}
		var role, stage string
		var takenDown bool
		err := tx.QueryRowContext(ctx, `SELECT m.role, e.stage, e.taken_down_at IS NOT NULL
			FROM events e JOIN event_members m ON m.event_id = e.id
			WHERE e.id = $1 AND e.slug = $2 AND e.account_id = $3 AND m.user_id = $4
			FOR SHARE OF e, m`, id.EventID, id.EventSlug, user.ID, id.OrganiserID).Scan(&role, &stage, &takenDown)
		if err != nil || role != "organiser" {
			return notFound()
		}
		if takenDown {
			return &rollbackGateResult{http.StatusForbidden, "this event has been taken down", "event_taken_down"}
		}
		if stage == "archived" {
			return &rollbackGateResult{http.StatusConflict, "this event has ended", "event_closed"}
		}
		return nil
	}
	if !h.ed.Identity().TeamKeys || user.Team == nil {
		return nil
	}
	if !strings.EqualFold(siteName, user.Team.TeamSlug) {
		return &rollbackGateResult{http.StatusForbidden, "this team key works on your team's site only: " + user.Team.TeamSlug, "team_site_only"}
	}
	var memberID string
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM event_members WHERE user_id = $1 AND team_id = $2 AND role = 'participant' AND approval_status = 'approved' FOR SHARE`, user.Team.MemberID, user.Team.TeamID).Scan(&memberID); err != nil {
		return notFound()
	}
	if h.ed.Identity().PaperMetrics {
		papermetrics.ResolveHackSite(ctx, user.Team.AccountID, siteName, user.Team.MemberID, false)
	}
	st, err := db.TeamWriteStateFor(ctx, tx, user.Team.TeamID, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &rollbackGateResult{http.StatusUnauthorized, "this team key stopped working: its team is gone", "team_key_inactive"}
		}
		return &rollbackGateResult{http.StatusInternalServerError, "internal server error", ""}
	}
	papermetrics.ResolveHackEvent(ctx, user.Team.EventSlug, st.Stage, user.Team.MemberID)
	if code := st.WriteRefusal(); code != "" {
		status, msg := auth.TeamRefusal(code)
		return &rollbackGateResult{status, msg, code}
	}
	if !h.teamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			h.RequestSiteCert(user.Handle.String)
		}
		return &rollbackGateResult{http.StatusConflict, "your event's team addresses are still being set up; try again in a few minutes", "team_sites_not_ready"}
	}
	return nil
}
