package data

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/sites"
)

// Event is a committed write's notification, sent with Service.Emit once
// the write's answer is settled.
type Event struct {
	Call Call
	Name string
	Data map[string]any
}

// Emit queues a committed storage write's event. Called only after the
// write commits: notification trouble never changes the answer.
func (s *Service) Emit(ev Event) {
	if s.notified != nil {
		s.notified(ev)
	}
	EmitEvent(s.deps.Notifications, s.deps.Hosts, ev)
}

// EmitEvent queues ev on the engine. An owner write stays silent unless it
// is to a visitor's row (so that visitor's own push devices hear of it). A
// nil engine sends nothing.
func EmitEvent(e *notify.Engine, hosts *sites.Hosts, ev Event) {
	if _, ok := ev.Data["row_visitor_id"]; e == nil || ev.Call.Owner && !ok {
		return
	}
	e.Emit(func(ctx context.Context) { StoreEvent(ctx, e, hosts, ev) })
}

// StoreEvent stores ev now (EmitEvent's work), when the site has
// subscriptions for it.
func StoreEvent(parent context.Context, e *notify.Engine, hosts *sites.Hosts, ev Event) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	c := ev.Call
	site, ok := e.EventSite(ctx, c.SiteID, ev.Name, "", 0)
	if !ok {
		return
	}
	siteName, siteURL, err := hosts.NotificationSite(ctx, site)
	if err != nil {
		return
	}
	e.StoreRecordEvent(ctx, notify.RecordEvent{SiteID: c.SiteID, Name: ev.Name, SiteName: siteName, SiteURL: siteURL, Resource: c.ResourceName, Kind: c.Resource.Kind, Writer: StampID(c.Owner, c.VisitorID), Data: ev.Data})
}
