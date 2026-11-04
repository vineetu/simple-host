package main

import (
	"context"
	"os"
	"time"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/workers"
)

type workerDependencies struct {
	cfg          config.Config
	site         *handler.SiteHandler
	user         *handler.UserHandler
	oauth        *handler.OAuthHandler
	connector    *handler.ConnectorHandler
	hack         *hackhttp.HackHandler
	image        *handler.ImageScan
	paper        *papermetrics.Collector
	metrics      *handler.APIMetrics
	geo          *geoip.DB
	analytics    *analytics.Ingester
	eventDomains *handler.EventDomainHandler
	hackSweep    func(context.Context)
}

// legacyWorker is a temporary startup seam while the other 5.4 owners land.
// It reports only enabled/running; its Wait covers the adapter, not legacy children.
// Replace these registrations with each owner's hook when merging B/C/D.
type legacyWorker struct {
	start  func(context.Context)
	runner *workers.Runner
}

func (o *legacyWorker) Start(ctx context.Context) {
	o.runner.Go(ctx, func(ctx context.Context) { o.start(ctx); <-ctx.Done() })
}
func (o *legacyWorker) Stop()                    { o.runner.Stop() }
func (o *legacyWorker) Wait()                    { o.runner.Wait() }
func (o *legacyWorker) Status() []workers.Status { return o.runner.Status() }
func addWorker(s *workers.Set, name string, enabled bool, candidate any, start func(context.Context)) {
	if !enabled {
		s.AddDisabled(name)
		return
	}
	if o, ok := candidate.(workers.Owner); ok {
		s.Add(o)
		return
	}
	s.Add(&legacyWorker{start, workers.NewRunner(workers.NewTracker(name))})
}

// startWorkers is the single registry of process background owners.
func startWorkers(ctx context.Context, d workerDependencies) *workers.Set {
	s := &workers.Set{}
	s.Add(d.site.HTTPCaches())
	s.Add(d.user.HTTPCaches())
	s.Add(d.oauth.HTTPCaches())
	s.Add(d.connector.HTTPCaches())
	s.Add(d.oauth.VisitorAuthWorkers())
	if d.hack != nil {
		s.Add(d.hack.LimiterWorkers())
	} else {
		s.AddDisabled("http.hack.codes_ip", "http.hack.codes_user", "http.hack.names_user", "http.hack.entry_writes", "http.hack.shot_writes", "http.hack.apply_ip", "http.hack.apply_user", "http.hack.screenshots")
	}
	addWorker(s, "accounts.connector", true, d.connector, func(context.Context) { d.connector.StartSweep(time.Hour) })
	addWorker(s, "sites.event_dns", d.eventDomains != nil, d.eventDomains, func(context.Context) { d.eventDomains.StartSweep(time.Hour) })
	addWorker(s, "geoip.watch", true, d.geo, func(context.Context) { d.geo.Watch(time.Minute) })
	addWorker(s, "metrics.api", true, d.metrics, func(context.Context) {}) // Legacy constructor starts it; D makes construction inert.
	addWorker(s, "analytics.ingest", d.analytics != nil, d.analytics, func(context.Context) { d.analytics.Start(5 * time.Minute) })
	addWorker(s, "sites.certificates", d.cfg.PersonHosts != "off" && d.cfg.SiteHosts != "off" && (d.cfg.SiteCertDir != "" || d.cfg.SiteBaseCertDir != ""), nil, func(ctx context.Context) { d.site.StartSiteCertRequests(ctx, 10*time.Minute) })
	addWorker(s, "events.reconcile", d.hack != nil, nil, func(ctx context.Context) { d.site.StartHackSweep(ctx, time.Minute, d.hackSweep) })
	addWorker(s, "events.cleanup", d.hack != nil, nil, func(ctx context.Context) { d.hack.StartEventCleanup(ctx) })
	addWorker(s, "notifications.content", d.hack != nil, nil, func(ctx context.Context) { d.hack.StartContentDelivery(ctx) })
	addWorker(s, "sites.deleted", true, nil, func(ctx context.Context) { d.site.StartDeletedSitePurge(ctx, time.Hour) })
	addWorker(s, "sites.idle", d.cfg.IdleCleanup, nil, d.site.StartIdleCleanup)
	addWorker(s, "legacy.saved_data", true, nil, d.site.StartSavedDataSweep)
	addWorker(s, "notifications.submissions", true, nil, d.site.StartSubmissionEmails)
	addWorker(s, "notifications.delivery", true, nil, d.site.StartNotifications)
	addWorker(s, "sites.address_families", d.cfg.Limits.AddressFamilies && d.cfg.FamilyCertDir != "", nil, d.site.StartAddressFamilies)
	addWorker(s, "network.sample", networkEnabled(d.cfg.NetworkInterface), nil, d.site.StartNetworkUsage)
	addWorker(s, "images.scan", d.image != nil, d.image, func(ctx context.Context) { d.image.Start(ctx) })
	addWorker(s, "metrics.paper", d.paper != nil, d.paper, func(ctx context.Context) { d.paper.Start(ctx) })
	// C replaces these constructor-started loops with owner hooks.
	addWorker(s, "sites.preview_expiry", len(d.cfg.PreviewAccounts) > 0, nil, func(context.Context) {})
	addWorker(s, "sites.domain_checks", d.cfg.CustomDomainIP != "", nil, func(context.Context) {})
	s.Start(ctx)
	return s
}

func networkEnabled(iface string) bool {
	if iface != "" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err != nil
}
