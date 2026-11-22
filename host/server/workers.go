package server

import (
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/geoip"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites/analytics"
	"github.com/vsriram/simple-host/host/sites/events"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/workers"
)

// registerWorkers is the single registry of process background owners, in
// start order. Retain every owner before any potentially blocking Start call.
func registerWorkers(site *siteshttp.SiteHandler, data *appstorage.Handler, user *accesshttp.UserHandler, oauth *accesshttp.OAuthHandler, connector *accesshttp.ConnectorHandler,
	parts extension.Parts, metrics *operations.APIMetrics, geo *geoip.DB, eventDomains *events.Handler, ingester *analytics.Ingester, paper *papermetrics.Collector) *workers.Set {
	s := &workers.Set{}
	s.Add(site.HTTPCaches())
	s.Add(user.HTTPCaches())
	s.Add(oauth.HTTPCaches())
	s.Add(connector.HTTPCaches())
	s.Add(oauth.VisitorAuthWorkers())
	for _, owner := range parts.Workers {
		s.Add(owner)
	}
	s.Add(connector.SweepWorker(time.Hour))
	if eventDomains != nil {
		s.Add(eventDomains.Owner())
	} else {
		s.AddDisabled("sites.event_dns_sweep")
	}
	s.Add(geo.WatchWorker(time.Minute))
	s.Add(metrics)
	if ingester != nil {
		s.Add(operations.NewAnalyticsIngest(ingester, 5*time.Minute))
	} else {
		s.AddDisabled("operations.analytics_ingest")
	}
	s.Add(site.SiteWorkers(parts.SiteSweep))
	s.Add(data.NotificationOwner())
	if w := site.NetworkUsageWorker(); w != nil {
		s.Add(w)
	} else {
		s.AddDisabled("operations.network_usage")
	}
	if paper != nil {
		s.Add(operations.NewPaperMetrics(paper))
	} else {
		s.AddDisabled("operations.paper_metrics")
	}
	return s
}
