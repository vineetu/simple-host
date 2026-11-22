package sitesroute

import (
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *Handler) LifecycleSvc() *sites.Service {
	return sites.New(h.Database, h.Disk, h.PreviewAccounts, h.PreviewTTL, sites.Deps{
		GroupGate: h.groupGate,
		Locks:     &h.UploadLocks, Hosts: h.Hosts, DeletedRetention: h.Limits.DeletedRetention,
	})
}

// SiteArchive exports a site's files and saved data (operations.SiteArchive).
func (h *Handler) SiteArchive() *operations.SiteArchive {
	return &operations.SiteArchive{Database: h.Database, Disk: h.Disk, Locks: &h.UploadLocks, Slots: h.SqliteSites}
}
