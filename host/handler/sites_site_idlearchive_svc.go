package handler

import (
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *SiteHandler) idleArchiveSvc() *sites.Service {
	return sites.New(h.database, h.disk, nil, 0, sites.Deps{
		LockSite: h.lockSite, DeletedRetention: h.limits.DeletedRetention,
		IdleArchive: sites.IdleArchiveDeps{
			Exempt: sites.IdleExempt{Handles: h.idleExempt.Handles, ReviewerEmail: h.idleExempt.ReviewerEmail, FamilySites: h.idleExempt.FamilySites},
			After:  operations.IdleRemoveAfter(h.ed),
		},
	})
}
