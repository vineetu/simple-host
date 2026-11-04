package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"

	"fmt"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/sites"
)

func (h *SiteHandler) sitesSvc() *sites.Service {
	return sites.New(h.database, h.disk, h.previewAccounts, h.previewTTL, sites.Deps{
		ContentHost: h.contentHost, SiteDomain: h.siteDomain, PersonalSitesEnabled: h.ed.Identity().PersonalSites,
		LockSite: h.lockSite,
		PhotoLimits: func(ctx context.Context, user *db.User) (int64, int64) {
			limit, override := h.siteLimitFor(ctx, h.database, user)
			return limit, extractBudget(limit, override)
		},
		DefaultRetention: keepVersions,
		CanSetRetention: func(ctx context.Context, user *db.User) bool {
			return h.limits.CanSetKeepVersions(accountHandles(ctx, h.database, user)...)
		},
		MaxFilesPerSite: h.limits.MaxFilesPerSite,
		NormalizePatch:  normalizeFilePatch,
		PatchSite: func(ctx context.Context, userID, name string) (db.Site, error) {
			r := httptest.NewRequest(http.MethodPut, "/", nil).WithContext(ctx)
			return h.siteForCaller(r, userID, name)
		},
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
		CreateGate: func(ctx context.Context, tx *sql.Tx, user *db.User, name string) error {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			if h.hackTeamTxGate(w, r, tx, user, name) {
				return nil
			}
			var body errorResponse
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			return &sites.GateError{Message: body.Error, Code: body.Code}
		},
		NotifyUpdate: func(ctx context.Context, user *db.User, site db.Site, n, prev int, published bool, files map[string][]byte) {
			if published {
				h.emitSiteNotification(site.ID, "site.deployed", map[string]any{"version": n, "previous_version": prev, "via": "deploy"}, 0)
				if h.ed.Identity().PaperMetrics && user.Team != nil {
					papermetrics.HackDeploy(ctx)
				}
			}
			h.imageScan.EnqueueSiteFiles(site.ID, siteWriterID(user), site.Name, n, files)
		},
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
		FileCaps: h.fileCapsEnabled(), CapCutoff: h.limits.SiteTotalCapFrom,
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
