package siteshttp

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/sites/lifecycle"
)

// Recently deleted: DELETE /v1/sites/{site} takes a site offline at once and keeps it
// (files, versions, saved data, claimed names, custom domain) for
// db.DeletedSiteRetention() with its name held; restore brings it back as it was and an
// in-process sweep removes it after the window. Deleting a whole account (admin) is immediate.

func (h *SiteHandler) PurgeDeletedSites(ctx context.Context) {
	lifecycle.Service{Service: h.LifecycleSvc()}.PurgeDeleted(ctx)
}

func (h *SiteHandler) startDeletedSitePurge(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.trash_purge")
	o.periodicTicker(ctx, 0, every, func(ctx context.Context) { h.PurgeDeletedSites(ctx) })
}
