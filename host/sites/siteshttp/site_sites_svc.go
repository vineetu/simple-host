package siteshttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	accountapp "github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

func (h *SiteHandler) SitesSvc() *sites.Service {
	return sites.New(h.Database, h.Disk, h.PreviewAccounts, h.PreviewTTL, sites.Deps{
		Sitemap:        h.sitemapFilter,
		SiteViewersMax: h.Limits.SiteViewersMax, ContentHost: h.ContentHost, SiteDomain: h.SiteDomain, PersonalSitesEnabled: h.Ed.Identity().PersonalSites,
		LockSite:         h.LockSite,
		DeleteViewerPush: h.Notify.DeleteViewerPush,
		ResponseState: func(site model.Site, version int, preview, footprint bool) any {
			return h.captureSiteResponseState(site, version, preview, footprint)
		},
		PhotoLimits: func(ctx context.Context, user *model.User) (int64, bool) {
			limit, override := accountapp.New(h.Database).SiteLimit(ctx, user, h.Limits, sites.Upload.Body)
			return limit, override
		},
		DefaultRetention: web.DefaultKeepVersions,
		CanSetRetention: func(ctx context.Context, user *model.User) bool {
			return h.Limits.CanSetKeepVersions(accountapp.New(h.Database).SiteAccountHandles(ctx, user)...)
		},
		MaxFilesPerSite:   h.Limits.MaxFilesPerSite,
		RedirectsMaxRules: h.Limits.RedirectsMaxRules,

		MaxSites: func(ctx context.Context, user *model.User) int {
			return accountapp.New(h.Database).MaxSites(ctx, user, h.Limits)
		},
		SiteURL: h.Hosts.Addressing().SiteURLFor,
		RenameURL: func(handle, name string) string {
			url := h.SiteURL(handle, name)
			if url == "" {
				url = fmt.Sprintf("https://%s/%s/%s/", h.ContentHost, handle, name)
			}
			return url
		},
		AccountRetention: func(ctx context.Context, userID string, keep int) (int, error) {
			value, _, err := accountRetention(ctx, h.Database, userID, keep, h.Limits)
			return value, err
		},
		RetainedFloor: func(versions []model.Version, keep, active, siteKeep int) int {
			floor := sites.RetainedFloor(versions, keep)
			if h.Ed.Storage().EventVersionRetention {
				floor, _ = pruneThreshold(active, effectiveKeepVersions(siteKeep))
			}
			return floor
		},
		PinnedFloor: h.groupPinnedFloor,
		GroupGate:   h.groupGate,
		NotifyUpdate: func(ctx context.Context, user *model.User, site model.Site, n, prev int, published bool, files map[string][]byte) {
			if published {
				h.Notify.EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: site.ID, Name: "site.deployed", Data: map[string]any{"version": n, "previous_version": prev, "via": "deploy"}})
				if h.Ed.Identity().PaperMetrics && user.Member != nil {
					papermetrics.From(ctx).Deploy(ctx)
				}
			}
		},
		NotifyRollback: func(siteID string, version, prev int) {
			h.Notify.EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: siteID, Name: "site.deployed", Data: map[string]any{"version": version, "previous_version": prev, "via": "active_version"}})
		},
		Assign: func(ctx context.Context, user *model.User) {
			accounts.AssignHandle(ctx, h.Database, user.ID, sites.HandleSeed(user, h.SiteDomain), h.Ed.Identity().RandomHandles)
		},
		NotifyCreate: func(ctx context.Context, user *model.User, site model.Site, files map[string][]byte) {
			if h.Ed.Identity().PaperMetrics && user.Member != nil {
				papermetrics.From(ctx).Deploy(ctx)
			}
		},
		FileCaps: h.fileCapsEnabled(), CapCutoff: h.Limits.SiteTotalCapFrom,
		CapLimits: func(ctx context.Context, user *model.User) (int, int) {
			handles := accountapp.New(h.Database).SiteAccountHandles(ctx, user)
			return h.Limits.SiteTotalMBFor(handles...), h.Limits.AccountMBFor(handles...)
		},
		Retention: func(ctx context.Context, user *model.User, site model.Site) (int, error) {
			keep, _, err := accountRetention(ctx, h.Database, user.ID, site.KeepVersions, h.Limits)
			return keep, err
		}, LockAccount: h.LockAccountFiles, Summarize: ExtractSiteSummary, Conflict: h.deletedNameConflict, RequestCert: h.RequestSiteCert, HasLiveFamilies: h.Hosts.HasLiveFamilies, SyncRedirect: h.Hosts.SyncDomainRedirect, DeployEnabled: h.DeployScript != ""})
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
			web.WriteSiteSuspended(w, siteErr.Message)
			return
		case status != 0:
			httpx.WriteError(w, status, siteErr.Code, siteErr.Message)
			return
		}
	}
	httpx.WriteInternal(w)
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
	httpx.WriteJSON(w, status, httpx.ErrorResponse{Error: e.Message, Code: e.Code})
}

func (h *SiteHandler) opsSitesSvc() *sites.Service {
	return h.SitesSvc().WithPlatformDomains(h.storeAccounts().NamespaceDomains())
}

func (h *SiteHandler) storeAccounts() *accountapp.Service { return accountapp.New(h.Database) }
