package handler

import (
	"context"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/workers"
)

// siteLoop retains a single scan and its cancellation/join handle.
type siteLoop struct {
	*workers.Runner
	tracker *workers.Tracker
	once    sync.Once
	enabled func() bool
	run     func(context.Context)
}

func newSiteLoop(name string, enabled func() bool, run func(context.Context)) *siteLoop {
	t := workers.NewTracker(name)
	return &siteLoop{Runner: workers.NewRunner(t), tracker: t, enabled: enabled, run: run}
}
func (o *siteLoop) Start(ctx context.Context) {
	o.once.Do(func() {
		if o.enabled() {
			o.run(ctx)
		}
	})
}
func (o *siteLoop) Status() []workers.Status {
	s := o.Runner.Status()
	s[0].Enabled = o.enabled()
	return s
}

// periodic preserves the delay-after-work cadence of the original sleep loops.
func (o *siteLoop) periodic(ctx context.Context, first, every time.Duration, fn func(context.Context)) {
	o.Go(ctx, func(ctx context.Context) {
		timer := time.NewTimer(first)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if ctx.Err() != nil {
				return
			}
			o.tracker.Attempt()
			fn(sites.WithWorkerReport(ctx, o.tracker))
			timer.Reset(every)
		}
	})
}

type siteWorkerGroup struct {
	loops     map[string]*siteLoop
	sweepName string
}

func (g *siteWorkerGroup) Start(ctx context.Context) {
	for _, o := range g.loops {
		o.Start(ctx)
	}
}
func (g *siteWorkerGroup) Stop() {
	for _, o := range g.loops {
		o.Stop()
	}
}
func (g *siteWorkerGroup) Wait() {
	for _, o := range g.loops {
		o.Wait()
	}
}
func (g *siteWorkerGroup) Status() []workers.Status {
	var out []workers.Status
	for _, o := range g.loops {
		out = append(out, o.Status()...)
	}
	return out
}

// SiteWorkers owns the site scans. Configure the product sweep before any worker starts.
func (h *SiteHandler) SiteWorkers(sweeps ...GroupSweep) workers.Owner {
	h.siteWorkersOnce.Do(func() {
		g := &siteWorkerGroup{loops: map[string]*siteLoop{}, sweepName: "sites.hack_sweep"}
		h.siteWorkers = g
		add := func(name string, enabled func() bool, run func(context.Context)) {
			g.loops[name] = newSiteLoop(name, enabled, run)
		}
		yes := func() bool { return true }
		add("sites.cert_requests", func() bool { return h.siteHostsOn() && len(h.certRequestBases()) > 0 }, func(ctx context.Context) { h.startSiteCertRequests(ctx, 10*time.Minute) })
		// Keep the disabled status name until D3-S4.
		add(g.sweepName, func() bool { return false }, nil)
		add("sites.trash_purge", yes, func(ctx context.Context) { h.startDeletedSitePurge(ctx, time.Hour) })
		add("sites.idle_cleanup", func() bool { return h.idleCleanup }, h.startIdleCleanup)
		add("sites.address_families", h.familiesOn, h.startAddressFamilies)
		add("sites.domain_checks", func() bool { return h.customDomainIP != "" }, func(ctx context.Context) { h.startDomainChecksContext(ctx, h.domainCheckInterval()) })
		add("sites.preview_expiry", func() bool { return len(h.previewAccounts) > 0 }, func(ctx context.Context) { h.startExpirySweepContext(ctx, time.Hour) })
	})
	if len(sweeps) > 0 {
		sweep := GroupSweep{Name: "sites.hack_sweep"} // Keep the disabled status name until D3-S4.
		if sweeps[0].Name != "" {
			sweep = sweeps[0]
		}
		delete(h.siteWorkers.loops, h.siteWorkers.sweepName)
		h.siteWorkers.sweepName = sweep.Name
		h.siteWorkers.loops[sweep.Name] = newSiteLoop(sweep.Name, func() bool { return sweep.Sweep != nil }, func(ctx context.Context) { h.startGroupSweep(ctx, time.Minute, sweep) })
	}
	return h.siteWorkers
}
func (h *SiteHandler) siteLoop(name string) *siteLoop {
	h.SiteWorkers()
	return h.siteWorkers.loops[name]
}

func (o *siteLoop) periodicTicker(ctx context.Context, first, every time.Duration, fn func(context.Context)) {
	o.Go(ctx, func(ctx context.Context) {
		if first > 0 {
			timer := time.NewTimer(first)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		run := func() {
			if ctx.Err() == nil {
				o.tracker.Attempt()
				fn(sites.WithWorkerReport(ctx, o.tracker))
			}
		}
		run()
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	})
}
