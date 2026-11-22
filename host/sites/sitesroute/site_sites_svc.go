package sitesroute

import (
	"errors"
	"net/http"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

func (h *Handler) SitesSvc() *sites.Service {
	return sites.New(h.Database, h.Disk, h.PreviewAccounts, h.PreviewTTL, sites.Deps{
		Sitemap:        h.sitemapFilter,
		SiteViewersMax: h.Limits.SiteViewersMax, ContentHost: h.ContentHost, SiteDomain: h.SiteDomain, PersonalSitesEnabled: h.Ed.Identity().PersonalSites,
		Locks: &h.UploadLocks, Hosts: h.Hosts, NoVisitorSignIn: h.noVisitorSignIn, EventVersionRetention: h.Ed.Storage().EventVersionRetention, DeletedRetention: h.Limits.DeletedRetention,
		DefaultRetention: web.DefaultKeepVersions, Limits: h.Limits, RandomHandles: h.Ed.Identity().RandomHandles,
		MaxFilesPerSite:   h.Limits.MaxFilesPerSite,
		RedirectsMaxRules: h.Limits.RedirectsMaxRules,
		GroupGate:         h.groupGate,
		FileCaps:          h.fileCapsEnabled(), CapCutoff: h.Limits.SiteTotalCapFrom,
		DeployEnabled: h.DeployScript != ""})
}

func WriteSitesError(w http.ResponseWriter, err error, siteName string) {
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		switch status := httpx.RefusalStatus(siteErr.Code, 0); {
		case siteErr.Code == "site_not_found":
			WriteSiteNotFound(w, siteName)
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

func WriteRollbackGateError(w http.ResponseWriter, e *sites.GateError) {
	status := e.Status
	switch {
	case e.Code == "":
		status = http.StatusInternalServerError
	case status == 0:
		status = http.StatusConflict
	}
	httpx.WriteJSON(w, status, httpx.ErrorResponse{Error: e.Message, Code: e.Code})
}

func (h *Handler) OpsSitesSvc() *sites.Service {
	return h.SitesSvc().WithPlatformDomains(h.StoreAccounts().NamespaceDomains())
}

func (h *Handler) StoreAccounts() *accountapp.Service { return accountapp.New(h.Database) }
