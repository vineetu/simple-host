package operations

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/edition"
	repo "github.com/vsriram/simple-host/internal/repository/operations"
	"log"
	"os"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/netusage"
	"github.com/vsriram/simple-host/internal/workers"
)

// Network use on the admin page's Overview: what the web server sent for
// sites (the analytics log's bytes, per UTC day, from traffic_daily and
// site_traffic_daily) and the box's own bytes in and out this month
// (internal/netusage), against NETWORK_MONTHLY_ALLOWANCE_GB.

// trafficTopSites is how many sites the "most traffic" table names.
const trafficTopSites = 10

// SetNetworkUsage turns on the box's network counters. iface is
// NETWORK_INTERFACE. Inside a container the default route is the container's
// own interface, not the box's, so there it stays off unless iface is set.
func (h *NetworkUsage) SetNetworkUsage(iface string) {
	if iface == "" && inContainer() {
		log.Printf("network usage: off (in a container; set NETWORK_INTERFACE to the host's interface to count it)")
		return
	}
	h.netUsage = netusage.New(h.database, iface)
}

// SetTrafficLog records whether the analytics log (and so traffic figures)
// is being read.
func (h *NetworkUsage) SetTrafficLog(on bool) { h.trafficOn = on }

func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// StartNetworkUsage samples the counters every NETWORK_SAMPLE_MINUTES.
// NetworkUsageWorker is nil when network accounting is disabled.
func (h *NetworkUsage) NetworkUsageWorker() workers.Owner {
	if h.netUsage == nil {
		return nil
	}
	h.workerOnce.Do(func() {
		h.worker = &networkUsageWorker{h: h, Owner: h.netUsage.Worker(h.ed.Limits().NetworkSample)}
	})
	return h.worker
}

type networkUsageWorker struct {
	startOnce sync.Once
	workers.Owner
	h *NetworkUsage
}

func (w *networkUsageWorker) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		iface, err := w.h.netUsage.Interface()
		if err != nil {
			log.Printf("network usage: %v", err)
			return
		}
		log.Printf("network usage: counting %s every %s", iface, w.h.ed.Limits().NetworkSample)
		w.Owner.Start(ctx)
	})
}
func (h *NetworkUsage) StartNetworkUsage(ctx context.Context) {
	if w := h.NetworkUsageWorker(); w != nil {
		w.Start(ctx)
	}
}

// networkUsage is the "network" object of GET /v1/admin/usage. Each half is
// null when it is off or could not be read, and the rest still answers.
func (h *NetworkUsage) Report(ctx context.Context, now time.Time) map[string]any {
	lim := h.ed.Limits()
	out := map[string]any{
		"allowance_gb": lim.NetworkAllowanceGB,
		"alert_pct":    lim.NetworkAlertPct,
		"box":          nil,
		"served":       nil,
	}
	if h.netUsage != nil {
		if m, err := h.netUsage.ThisMonth(ctx); err != nil {
			log.Printf("admin usage: network counters: %v", err)
		} else {
			out["box"] = m
		}
	}
	if h.trafficOn {
		if s, err := repo.ServedTraffic(ctx, h.database, now); err != nil {
			log.Printf("admin usage: traffic: %v", err)
		} else {
			out["served"] = s
		}
	}
	return out
}

// NetworkUsage owns instance network accounting and served-traffic reporting.
type NetworkUsage struct {
	database   *sql.DB
	ed         *edition.Edition
	netUsage   *netusage.Sampler
	trafficOn  bool
	workerOnce sync.Once
	worker     *networkUsageWorker
}

func NewNetworkUsage(database *sql.DB, ed *edition.Edition) *NetworkUsage {
	return &NetworkUsage{database: database, ed: ed}
}
