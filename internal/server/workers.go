package server

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/workers"
)

type workerDependencies struct {
	site           *handler.SiteHandler
	user           *handler.UserHandler
	oauth          *handler.OAuthHandler
	connector      *handler.ConnectorHandler
	productWorkers []workers.Owner
	image          *operations.ImageScan
	paper          *operations.PaperMetrics
	metrics        *operations.APIMetrics
	geo            *geoip.DB
	analytics      *operations.AnalyticsIngest
	eventDomains   *handler.EventDomainHandler
	siteSweep      func(context.Context)
}

// registerWorkers is the single registry of process background owners.
// Retain every owner before any potentially blocking Start call.
func registerWorkers(d workerDependencies) *workers.Set {
	s := &workers.Set{}
	s.Add(d.site.HTTPCaches())
	s.Add(d.user.HTTPCaches())
	s.Add(d.oauth.HTTPCaches())
	s.Add(d.connector.HTTPCaches())
	s.Add(d.oauth.VisitorAuthWorkers())
	if len(d.productWorkers) > 0 {
		for _, owner := range d.productWorkers {
			s.Add(owner)
		}
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
		s.Add(d.analytics)
	} else {
		s.AddDisabled("operations.analytics_ingest")
	}
	s.Add(d.site.SiteWorkers(d.siteSweep))
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
	return s
}
