package siteshttp

import (
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *SiteHandler) LifecycleSvc() *sites.Service {
	return sites.New(h.Database, h.Disk, h.PreviewAccounts, h.PreviewTTL, sites.Deps{
		GroupGate: h.groupGate,
		ResponseState: func(site model.Site, version int, preview, footprint bool) any {
			return h.captureSiteResponseState(site, version, preview, footprint)
		},
		Locks: &h.UploadLocks, Hosts: h.Hosts, DeletedRetention: h.Limits.DeletedRetention,
	})
}

// SiteArchive exports a site's files and saved data (operations.SiteArchive).
func (h *SiteHandler) SiteArchive() *operations.SiteArchive {
	return &operations.SiteArchive{Database: h.Database, Disk: h.Disk, Locks: &h.UploadLocks, Slots: h.SqliteSites}
}
