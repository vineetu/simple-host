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
	loops map[string]*siteLoop
	also  func(context.Context)
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

// SiteWorkers owns the site scans. The optional callback runs after each Hack
// sweep, preserving open-event certificate requests followed by entry settling.
// Configure it before Start.
func (h *SiteHandler) SiteWorkers(also ...func(context.Context)) workers.Owner {
	h.siteWorkersOnce.Do(func() {
		g := &siteWorkerGroup{loops: map[string]*siteLoop{}}
		h.siteWorkers = g
		add := func(name string, enabled func() bool, run func(context.Context)) {
			g.loops[name] = newSiteLoop(name, enabled, run)
		}
		yes := func() bool { return true }
		add("sites.cert_requests", func() bool { return h.siteHostsOn() && len(h.certRequestBases()) > 0 }, func(ctx context.Context) { h.startSiteCertRequests(ctx, 10*time.Minute) })
		add("sites.hack_sweep", func() bool { return h.ed.Identity().TeamKeys }, func(ctx context.Context) { h.startHackSweep(ctx, time.Minute, g.also) })
		add("sites.trash_purge", yes, func(ctx context.Context) { h.startDeletedSitePurge(ctx, time.Hour) })
		add("sites.idle_cleanup", func() bool { return h.idleCleanup }, h.startIdleCleanup)
		add("sites.address_families", h.familiesOn, h.startAddressFamilies)
		add("sites.domain_checks", func() bool { return h.customDomainIP != "" }, func(ctx context.Context) { h.startDomainChecksContext(ctx, h.domainCheckInterval()) })
		add("sites.preview_expiry", func() bool { return len(h.previewAccounts) > 0 }, func(ctx context.Context) { h.startExpirySweepContext(ctx, time.Hour) })
	})
	if len(also) > 0 {
		h.siteWorkers.also = also[0]
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
