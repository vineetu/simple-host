package handler

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/workers"
	"time"
)

func (h *SiteHandler) SetOperationsNetwork(n *operations.NetworkUsage) { h.network = n }
func (h *SiteHandler) SetNetworkUsage(iface string)                    { h.network.SetNetworkUsage(iface) }
func (h *SiteHandler) SetTrafficLog(on bool)                           { h.trafficOn = on; h.network.SetTrafficLog(on) }
func (h *SiteHandler) NetworkUsageWorker() workers.Owner               { return h.network.NetworkUsageWorker() }
func (h *SiteHandler) StartNetworkUsage(ctx context.Context)           { h.network.StartNetworkUsage(ctx) }
func (h *SiteHandler) networkUsage(ctx context.Context, now time.Time) map[string]any {
	return h.network.Report(ctx, now)
}
