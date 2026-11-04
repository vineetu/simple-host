package hackhttp

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/workers"
)

// Archive cleanup (M4). Once an event is archived, its team sites stay up
// for EventSitesKeep. EventRemovalWarn before they go, the organiser gets
// one email. At the end of the keep window each team's site moves to
// Recently deleted; the event page and the results stay. keep_sites, set by
// the platform admin, leaves the sites up. The sweep runs daily.

const (
	eventCleanupEvery = 24 * time.Hour
	eventCleanupDelay = 10 * time.Minute
)

// SetMailer is the notice sender for the archive warning. Call once at startup.
func (h *HackHandler) SetMailer(m events.Mailer) { h.mailer = m }

// StartEventCleanup runs the sweep shortly after boot and then daily.
// EVENTS=hosted only: main starts it from the hosted-mode block.
func (h *HackHandler) StartEventCleanup(ctx context.Context) { h.EventCleanupOwner().Start(ctx) }

func (h *HackHandler) runEventCleanup(ctx context.Context, now time.Time) {
	h.runEventCleanupTracked(ctx, now, nil)
}
func (h *HackHandler) runEventCleanupTracked(ctx context.Context, now time.Time, report *workers.Tracker) {
	send := events.ArchiveWarningSender(h.mailer)
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	var reports []events.CleanupReport
	if report != nil {
		reports = append(reports, report)
	}
	h.events.RunEventCleanup(ctx, now, h.limits.EventSitesKeep, h.limits.EventRemovalWarn, trash, send, h.idleReplyTo(), reports...)
}
