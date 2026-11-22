package lifecycle

import (
	"context"
	"log"
	"time"

	"github.com/vsriram/simple-host/host/workers"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// Archive cleanup. Once an event is archived, its team sites stay up
// for EventSitesKeep. EventRemovalWarn before they go, the organiser gets
// one email. At the end of the keep window each team's site moves to
// Recently deleted; the event page and the results stay. keep_sites, set by
// the platform admin, leaves the sites up. The sweep runs daily.

const (
	eventCleanupEvery = 24 * time.Hour
	eventCleanupDelay = 10 * time.Minute
)

func (h *Handler) StartEventCleanup(ctx context.Context) { h.EventCleanupOwner().Start(ctx) }

func (h *Handler) runEventCleanup(ctx context.Context, now time.Time) {
	h.runEventCleanupTracked(ctx, now, nil)
}
func (h *Handler) runEventCleanupTracked(ctx context.Context, now time.Time, report *workers.Tracker) {
	send := events.ArchiveWarningSender(h.Mailer)
	var reports []CleanupReport
	if report != nil {
		reports = append(reports, report)
	}
	h.RunEventCleanup(ctx, now, h.Limits.EventSitesKeep, h.Limits.EventRemovalWarn, h.Links.Trash(), send, h.Limits.IdleReplyTo, reports...)
}

var hackStages = map[string]bool{
	"draft": true, "open": true, "building": true, "closed": true,
	"judging": true, "results": true, "archived": true,
}

// syncEventMarkers puts the event's team sites down or back with the event;
// false (after logging) when a marker could not be written.
func (h *Handler) syncEventMarkers(ctx context.Context, ev hackmodel.Event) bool {
	if h.Links.Hooks == nil {
		return true
	}
	if err := h.Links.Hooks.SyncAccountMarkers(ctx, ev.AccountID); err != nil {
		log.Printf("hack: take-down markers of %s: %v", ev.Slug, err)
		return false
	}
	return true
}

func (h *Handler) manageURL(slug string) string {
	return h.Links.PublicBaseURL + "/e/" + slug + "/manage"
}
