package hackhttp

import (
	"context"
	"log"
	"time"

	"github.com/vsriram/simple-host/internal/email"
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
func (h *HackHandler) SetMailer(m email.Sender) { h.mailer = m }

// hackReplySender is handler's replyNoticeSender (idle.go), duplicated here
// (cleanup 3.2 slice 1c) so this type assertion needs no cross-package
// reference once HackHandler moves to internal/hackhttp: any email.Sender
// that also exposes SendNoticeReplyTo satisfies both interfaces identically.
type hackReplySender interface {
	SendNoticeReplyTo(toEmail, replyTo, subject, text string) error
}

// StartEventCleanup runs the sweep shortly after boot and then daily.
// EVENTS=hosted only: main starts it from the hosted-mode block.
func (h *HackHandler) StartEventCleanup(ctx context.Context) {
	keep := h.limits.EventSitesKeep
	warn := h.limits.EventRemovalWarn
	log.Printf("event-site cleanup: on (team sites removed %d days after an event archives, one warning %d days before, then daily)", int(keep.Hours()/24), int(warn.Hours()/24))
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(eventCleanupDelay):
		}
		h.runEventCleanup(ctx, time.Now())
		t := time.NewTicker(eventCleanupEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.runEventCleanup(ctx, time.Now())
			}
		}
	}()
}

func (h *HackHandler) runEventCleanup(ctx context.Context, now time.Time) {
	var send func(string, string, string, string) error
	if m, ok := h.mailer.(hackReplySender); ok {
		send = m.SendNoticeReplyTo
	}
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	h.events.RunEventCleanup(ctx, now, h.limits.EventSitesKeep, h.limits.EventRemovalWarn, trash, send, h.idleReplyTo())
}
