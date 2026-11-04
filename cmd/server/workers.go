package main

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/workers"
)

type workerDependencies struct {
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
		s.Add(d.hack.EventCleanupOwner())
		s.Add(d.hack.ContentDeliveryOwner())
	} else {
		s.AddDisabled("http.hack.limiters", "hack.event_cleanup", "hack.content_delivery")
	}
	s.Add(d.connector.SweepWorker(time.Hour))
	if d.eventDomains != nil {
		s.Add(d.eventDomains.Owner())
	} else {
		s.AddDisabled("sites.event_dns_sweep")
	}
	s.Add(d.geo.WatchWorker(time.Minute))
	s.Add(d.metrics)
	if d.analytics != nil {
		s.Add(d.analytics.Worker(5 * time.Minute))
	} else {
		s.AddDisabled("operations.analytics_ingest")
	}
	s.Add(d.site.SiteWorkers(d.hackSweep))
	s.Add(d.site.SavedDataWorker())
	s.Add(d.site.SubmissionEmailOwner())
	s.Add(d.site.NotificationOwner())
	if w := d.site.NetworkUsageWorker(); w != nil {
		s.Add(w)
	} else {
		s.AddDisabled("operations.network_usage")
	}
	if d.image != nil {
		s.Add(d.image)
	} else {
		s.AddDisabled("operations.image_scan")
	}
	if d.paper != nil {
		s.Add(d.paper)
	} else {
		s.AddDisabled("operations.paper_metrics")
	}
	s.Start(ctx)
	return s
}
