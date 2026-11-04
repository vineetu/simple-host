package handler

import (
	"context"
	"database/sql"
	"strings"

	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/sites"
)

func (h *SiteHandler) sitesSvc() *sites.Service {
	return sites.New(h.database, h.disk, h.previewAccounts, h.previewTTL, sites.Deps{
		ContentHost: h.contentHost, SiteDomain: h.siteDomain, PersonalSitesEnabled: h.ed.Identity().PersonalSites,
		LockSite:   h.lockSite,
		MaxSites:   func(ctx context.Context, user *db.User) int { return h.maxSitesFor(ctx, h.database, user) },
		WantMarker: h.wantUnlistedMarker, SiteURL: h.siteURLFor,
		RenameURL: func(handle, name string) string {
			url := h.SiteURL(handle, name)
			if url == "" {
				url = fmt.Sprintf("https://%s/%s/%s/", h.contentHost, handle, name)
			}
			return url
		},
		AccountRetention: func(ctx context.Context, userID string, keep int) (int, error) {
			value, _, err := accountRetention(ctx, h.database, userID, keep, h.limits)
			return value, err
		},
		RetainedFloor: func(versions []db.Version, keep, active, siteKeep int) int {
			floor := retainedFloor(versions, keep)
			if h.ed.Storage().EventVersionRetention {
				floor, _ = pruneThreshold(active, effectiveKeepVersions(siteKeep))
			}
			return floor
		},
		PinnedFloor: h.hackPinnedFloor,
		CreateGate:  h.rollbackTeamGate,
		NotifyRollback: func(siteID string, version, prev int) {
			h.emitSiteNotification(siteID, "site.deployed", map[string]any{"version": version, "previous_version": prev, "via": "active_version"}, 0)
		},
		Assign: func(ctx context.Context, user *db.User) {
			assignHandle(ctx, h.database, user.ID, handleSeed(user, h.siteDomain), h.ed.Identity().RandomHandles)
		},
		NotifyCreate: func(ctx context.Context, user *db.User, site db.Site, files map[string][]byte) {
			if h.ed.Identity().PaperMetrics && user.Team != nil {
				papermetrics.HackDeploy(ctx)
			}
			h.imageScan.EnqueueSiteFiles(site.ID, siteWriterID(user), site.Name, 1, files)
		},
		FileCaps: h.ed.Storage().FileCaps && (h.limits.MaxSiteTotalMB != 0 || h.limits.MaxAccountMB != 0 || h.limits.MaxSiteTotalOverrides != "" || h.limits.MaxAccountOverrides != ""), CapCutoff: h.limits.SiteTotalCapFrom,
		CapLimits: func(ctx context.Context, user *db.User) (int, int) {
			handles := accountHandles(ctx, h.database, user)
			return h.limits.SiteTotalMBFor(handles...), h.limits.AccountMBFor(handles...)
		},
		Retention: func(ctx context.Context, user *db.User, site db.Site) (int, error) {
			keep, _, err := accountRetention(ctx, h.database, user.ID, site.KeepVersions, h.limits)
			return keep, err
		}, LockAccount: h.lockAccountFiles, Summarize: ExtractSiteSummary, Conflict: h.deletedNameConflict, Marker: h.syncUnlistedMarker, RequestCert: h.RequestSiteCert, HasLiveFamilies: h.hasLiveFamilies, SyncRedirect: h.syncDomainRedirect, DeployEnabled: h.deployScript != ""})
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

// writeRollbackGateError maps a transaction gate decision to its HTTP response.
func writeRollbackGateError(w http.ResponseWriter, e *sites.GateError) {
	status := http.StatusConflict
	switch e.Code {
	case "event_not_found":
		status = http.StatusNotFound
	case "event_taken_down", "team_site_only", "team_site_taken_down":
		status = http.StatusForbidden
	case "team_key_inactive":
		status = http.StatusUnauthorized
	case "":
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, errorResponse{Error: e.Message, Code: e.Code})
}

// rollbackTeamGate repeats the deploy gate with plain request facts so the
// service can keep the transaction open around the authorization check.
func (h *SiteHandler) rollbackTeamGate(ctx context.Context, tx *sql.Tx, user *db.User, siteName string) error {
	notFound := func() error { return &sites.GateError{Message: "event not found", Code: "event_not_found"} }
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
			return &sites.GateError{Message: "this event has been taken down", Code: "event_taken_down"}
		}
		if stage == "archived" {
			return &sites.GateError{Message: "this event has ended", Code: "event_closed"}
		}
		return nil
	}
	if !h.ed.Identity().TeamKeys || user.Team == nil {
		return nil
	}
	if !strings.EqualFold(siteName, user.Team.TeamSlug) {
		return &sites.GateError{Message: "this team key works on your team's site only: " + user.Team.TeamSlug, Code: "team_site_only"}
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
			return &sites.GateError{Message: "this team key stopped working: its team is gone", Code: "team_key_inactive"}
		}
		return &sites.GateError{Message: "internal server error", Code: ""}
	}
	papermetrics.ResolveHackEvent(ctx, user.Team.EventSlug, st.Stage, user.Team.MemberID)
	if code := st.WriteRefusal(); code != "" {
		_, msg := auth.TeamRefusal(code)
		return &sites.GateError{Message: msg, Code: code}
	}
	if !h.teamSitesReady(user.Handle.String) {
		if !st.EventTakenDown && (st.Stage == "open" || st.Stage == "building" || st.Stage == "closed") {
			h.RequestSiteCert(user.Handle.String)
		}
		return &sites.GateError{Message: "your event's team addresses are still being set up; try again in a few minutes", Code: "team_sites_not_ready"}
	}
	return nil
}
