package siteshttp

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/workers"
)

// SiteHandler is Sites' routes: the shared route state (sitesroute.Handler)
// plus what only the routes still here keep: the sitemap cache and the
// site workers.
type SiteHandler struct {
	*sitesroute.Handler
	Sitemaps        sitemapCache
	siteWorkersOnce sync.Once
	siteWorkers     *siteWorkerGroup
}

func (h *SiteHandler) startExpirySweepContext(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.preview_expiry")
	o.periodic(ctx, 30*time.Second, every, func(ctx context.Context) { h.deploys().SweepExpiredSites(ctx) })
}

func (h *SiteHandler) SweepExpiredSites() { h.deploys().SweepExpiredSites(context.Background()) }

func (h *SiteHandler) HTTPCaches() workers.Owner { return &h.Caches }
