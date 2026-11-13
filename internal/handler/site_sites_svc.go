package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *SiteHandler) sitesSvc() *sites.Service {
	return sites.New(h.database, h.disk, h.previewAccounts, h.previewTTL, sites.Deps{
		Sitemap:        h.sitemapFilter,
		SiteViewersMax: h.limits.SiteViewersMax, ContentHost: h.contentHost, SiteDomain: h.siteDomain, PersonalSitesEnabled: h.ed.Identity().PersonalSites,
		LockSite:         h.lockSite,
		DeleteViewerPush: h.deletePushForEmail,
		ResponseState: func(site model.Site, version int, preview, footprint bool) any {
			return h.captureSiteResponseState(site, version, preview, footprint)
		},
		PhotoLimits: func(ctx context.Context, user *model.User) (int64, bool) {
			limit, override := h.siteLimitFor(ctx, h.database, user)
			return limit, override
		},
		DefaultRetention: keepVersions,
		CanSetRetention: func(ctx context.Context, user *model.User) bool {
			return h.limits.CanSetKeepVersions(accountHandles(ctx, h.database, user)...)
		},
		MaxFilesPerSite:   h.limits.MaxFilesPerSite,
		RedirectsMaxRules: h.limits.RedirectsMaxRules,
		NormalizePatch:    normalizeFilePatch,

		MaxSites: func(ctx context.Context, user *model.User) int { return h.maxSitesFor(ctx, h.database, user) },
		SiteURL:  h.siteURLFor,
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
		RetainedFloor: func(versions []model.Version, keep, active, siteKeep int) int {
			floor := retainedFloor(versions, keep)
			if h.ed.Storage().EventVersionRetention {
				floor, _ = pruneThreshold(active, effectiveKeepVersions(siteKeep))
			}
			return floor
		},
		PinnedFloor: h.hackPinnedFloor,
		TeamKeys:    h.ed.Identity().TeamKeys, TeamSitesReady: h.teamSitesReady,
		PaperSite: func(ctx context.Context, user *model.User, name string) {
			if h.ed.Identity().PaperMetrics {
				paperRecorder(ctx).ResolveHackSite(ctx, user.Team.AccountID, name, user.Team.MemberID, false)
			}
		},
		PaperState: func(ctx context.Context, user *model.User, stage string) {
			paperRecorder(ctx).ResolveHackEvent(ctx, user.Team.EventSlug, stage, user.Team.MemberID)
		},
		NotifyUpdate: func(ctx context.Context, user *model.User, site model.Site, n, prev int, published bool, files map[string][]byte) {
			if published {
				h.emitSiteNotification(site.ID, "site.deployed", map[string]any{"version": n, "previous_version": prev, "via": "deploy"}, 0)
				if h.ed.Identity().PaperMetrics && user.Team != nil {
					paperRecorder(ctx).HackDeploy(ctx)
				}
			}
			h.imageScan.EnqueueSiteFiles(site.ID, siteWriterID(user), site.Name, n, files)
		},
		NotifyRollback: func(siteID string, version, prev int) {
			h.emitSiteNotification(siteID, "site.deployed", map[string]any{"version": version, "previous_version": prev, "via": "active_version"}, 0)
		},
		Assign: func(ctx context.Context, user *model.User) {
			assignHandle(ctx, h.database, user.ID, handleSeed(user, h.siteDomain), h.ed.Identity().RandomHandles)
		},
		NotifyCreate: func(ctx context.Context, user *model.User, site model.Site, files map[string][]byte) {
			if h.ed.Identity().PaperMetrics && user.Team != nil {
				paperRecorder(ctx).HackDeploy(ctx)
			}
			h.imageScan.EnqueueSiteFiles(site.ID, siteWriterID(user), site.Name, 1, files)
		},
		FileCaps: h.fileCapsEnabled(), CapCutoff: h.limits.SiteTotalCapFrom,
		CapLimits: func(ctx context.Context, user *model.User) (int, int) {
			handles := accountHandles(ctx, h.database, user)
			return h.limits.SiteTotalMBFor(handles...), h.limits.AccountMBFor(handles...)
		},
		Retention: func(ctx context.Context, user *model.User, site model.Site) (int, error) {
			keep, _, err := accountRetention(ctx, h.database, user.ID, site.KeepVersions, h.limits)
			return keep, err
		}, LockAccount: h.lockAccountFiles, Summarize: ExtractSiteSummary, Conflict: h.deletedNameConflict, RequestCert: h.RequestSiteCert, HasLiveFamilies: h.hasLiveFamilies, SyncRedirect: h.syncDomainRedirect, DeployEnabled: h.deployScript != ""})
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
