package siteshttp

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/workers"
)

func (h *SiteHandler) SetNetworkUsage(iface string)          { h.Network.SetNetworkUsage(iface) }
func (h *SiteHandler) SetTrafficLog(on bool)                 { h.trafficOn = on; h.Network.SetTrafficLog(on) }
func (h *SiteHandler) NetworkUsageWorker() workers.Owner     { return h.Network.NetworkUsageWorker() }
func (h *SiteHandler) StartNetworkUsage(ctx context.Context) { h.Network.StartNetworkUsage(ctx) }
func (h *SiteHandler) networkUsage(ctx context.Context, now time.Time) map[string]any {
	return h.Network.Report(ctx, now)
}
