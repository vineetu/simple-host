package notify

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/sites"
)

// EmitSiteEvent queues ev with the site's name and address as
// notification payloads show them. Nothing on an install whose
// notifications are scoped to events.
func (e *Engine) EmitSiteEvent(hosts *sites.Hosts, ev sites.SiteEvent) {
	if hosts.EventScoped() {
		return
	}
	e.Emit(func(ctx context.Context) { e.StoreSiteEventNow(ctx, hosts, ev) })
}

// StoreSiteEventNow writes ev now (EmitSiteEvent's work).
func (e *Engine) StoreSiteEventNow(parent context.Context, hosts *sites.Hosts, ev sites.SiteEvent) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	visitor, _ := ev.Data["visitor"].(string)
	site, ok := e.EventSite(ctx, ev.SiteID, ev.Name, visitor, ev.Dedupe)
	if !ok {
		return
	}
	siteName, siteURL, err := hosts.NotificationSite(ctx, site)
	if err != nil {
		return
	}
	e.StoreSiteEvent(ctx, SiteEvent{SiteID: ev.SiteID, Name: ev.Name, SiteName: siteName, SiteURL: siteURL, Visitor: visitor, Dedupe: ev.Dedupe, Data: ev.Data})
}
