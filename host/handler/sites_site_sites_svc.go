package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
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
			floor := sites.RetainedFloor(versions, keep)
			if h.ed.Storage().EventVersionRetention {
				floor, _ = pruneThreshold(active, effectiveKeepVersions(siteKeep))
			}
			return floor
		},
		PinnedFloor: h.groupPinnedFloor,
		GroupGate:   h.groupGate,
		NotifyUpdate: func(ctx context.Context, user *model.User, site model.Site, n, prev int, published bool, files map[string][]byte) {
			if published {
				h.emitSiteNotification(site.ID, "site.deployed", map[string]any{"version": n, "previous_version": prev, "via": "deploy"}, 0)
				if h.ed.Identity().PaperMetrics && user.Member != nil {
					paperRecorder(ctx).Deploy(ctx)
				}
			}
		},
		NotifyRollback: func(siteID string, version, prev int) {
			h.emitSiteNotification(siteID, "site.deployed", map[string]any{"version": version, "previous_version": prev, "via": "active_version"}, 0)
		},
		Assign: func(ctx context.Context, user *model.User) {
			assignHandle(ctx, h.database, user.ID, handleSeed(user, h.siteDomain), h.ed.Identity().RandomHandles)
		},
		NotifyCreate: func(ctx context.Context, user *model.User, site model.Site, files map[string][]byte) {
			if h.ed.Identity().PaperMetrics && user.Member != nil {
				paperRecorder(ctx).Deploy(ctx)
			}
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

// writeSitesError answers a site workflow refusal at its code's status.
func writeSitesError(w http.ResponseWriter, err error, siteName string) {
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		switch status := httpx.RefusalStatus(siteErr.Code, 0); {
		case siteErr.Code == "site_not_found":
			writeSiteNotFound(w, siteName)
			return
		case siteErr.Code == "site_suspended":
			writeSiteSuspended(w, siteErr.Message)
			return
		case status != 0:
			writeProductErr(w, status, siteErr.Code, siteErr.Message)
			return
		}
	}
	writeInternal(w)
}

// writeRollbackGateError maps a transaction gate decision to its HTTP response.
func writeRollbackGateError(w http.ResponseWriter, e *sites.GateError) {
	status := e.Status
	switch {
	case e.Code == "":
		status = http.StatusInternalServerError
	case status == 0:
		status = http.StatusConflict
	}
	writeJSON(w, status, errorResponse{Error: e.Message, Code: e.Code})
}
