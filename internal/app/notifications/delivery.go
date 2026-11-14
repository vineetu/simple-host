package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/workers"
	"time"
)

type DeliveryResult struct {
	Delivered bool
	Status    int
	Result    string
}
type DeliveryDeps struct {
	Tracker, Retention *workers.Tracker
	Mailer             email.NoticeSender
	AcquireOwner       func(string) bool
	ReleaseOwner       func(string)
	AllowURL           func(string) bool
	Body               func(context.Context, model.Notification, model.NotificationDelivery, int64) []byte
	Send               func(context.Context, model.Notification, string, []byte) DeliveryResult
	Pending            func(context.Context)
	// VisitorAllowed re-checks, at delivery time, whether a visitor-role
	// push subscription's own subscriber (siteID, userID == n.OwnerID) is
	// still welcome on this site right now -- the same decision an
	// ordinary read's named-viewers gate would make, since a push device
	// belongs to a specific signed-in visitor rather than the owner
	// (SIM-360). Left nil (no caller sets it today besides the handler's
	// own wiring), a visitor-role subscription is allowed, matching
	// behaviour before this check existed.
	VisitorAllowed func(ctx context.Context, siteID, userID string) (bool, error)
}

func (h *Service) Deliver(ctx context.Context, ed *edition.Edition, deps DeliveryDeps) bool {
	l := ed.Limits()
	deps.Tracker.Attempt()
	deps.Pending(ctx)
	d, err := h.ClaimNotificationDelivery(ctx, l.NotifyTimeout+30*time.Second, l.NotifyAttempts, l.NotifyHistoryDays, l.NotifySuspendWindow)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			deps.Tracker.Fail("claim delivery", err)
		}
		return false
	}
	return h.ProcessDelivery(ctx, d, ed, deps)
}
func (h *Service) ProcessDelivery(ctx context.Context, d model.NotificationDelivery, ed *edition.Edition, deps DeliveryDeps) bool {
	l := ed.Limits()
	ctx, cancel := context.WithDeadline(ctx, d.LeaseUntil)
	defer cancel()
	n, err := h.GetNotification(ctx, d.SubscriptionID)
	if err != nil {
		deps.Tracker.Fail("load subscription", err)
		return true
	}
	// A pause or expiry may have happened after the claim.
	if n.State != "active" || n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
		return true
	}
	start := time.Now()
	result := DeliveryResult{Result: "site_unavailable"}
	state := "skipped"
	site, err := h.sites.GetSiteByID(ctx, n.SiteID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		deps.Tracker.Fail("load delivery site", err)
		return true
	}
	// A push subscription's OwnerID is the subscriber -- the site owner's
	// own account id for subscriber_role "owner" (checked the same way a
	// webhook/mcp-event subscription always has been), or a signed-in
	// visitor's own users.id for subscriber_role "visitor" (SIM-360): that
	// can never equal site.UserID, so it is never refused delivery for
	// that reason alone. The event fan-out (EmitSiteEventFor) is what
	// actually proved a visitor-role subscription is allowed to exist and
	// match this event; VisitorAllowed is the delivery-time recheck for
	// whatever can have changed since (the visitor is no longer a named
	// viewer on a site that has locked itself to them) -- the same thing
	// site.Offline/Suspended/Deleted below are for an owner subscription.
	allowed := site.UserID == n.OwnerID
	if err == nil && n.SubscriberRole == "visitor" {
		if deps.VisitorAllowed == nil {
			allowed = true
		} else if allowed, err = deps.VisitorAllowed(ctx, n.SiteID, n.OwnerID); err != nil {
			deps.Tracker.Fail("check visitor access", err)
			return true
		}
	}
	if err == nil && ed.Notifications().EventScoped {
		allowed, err = h.GroupNotificationAllowed(ctx, n.SiteID, n.OwnerID, false)
		if err != nil {
			return true
		}
	}
	if err == nil && !site.Offline && !site.Suspended() && !site.Deleted && allowed {
		cursor, e := h.NotificationCursor(ctx, n.ID, n.SiteID)
		if e != nil {
			deps.Tracker.Fail("load delivery cursor", e)
			return true
		}
		held, err := h.NotificationLeaseHeld(ctx, d)
		if err != nil {
			deps.Tracker.Fail("check delivery lease", err)
		}
		if err != nil || !held {
			return true
		}
		acquired := deps.AcquireOwner(n.OwnerID)
		if acquired {
			defer deps.ReleaseOwner(n.OwnerID)
		}
		if !acquired || !deps.AllowURL(n.URL) {
			if err := h.DeferNotificationDelivery(ctx, d); err != nil {
				deps.Tracker.Fail("defer delivery", err)
			}
			return true
		}
		body := deps.Body(ctx, n, d, cursor)
		if body == nil {
			return true
		}
		result = deps.Send(ctx, n, d.EventID, body)
		if !result.Delivered {
			deps.Tracker.Fail("send webhook", fmt.Errorf("%s (HTTP %d)", result.Result, result.Status))
		}
		state = "failed"
		if result.Delivered {
			state = "delivered"
		} else if d.Attempts < l.NotifyAttempts && result.Status != 410 && result.Status != 413 && result.Result != "refused_address" {
			state = "pending"
		}
	}
	delays := []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 7 * time.Minute}
	i := d.Attempts - 1
	if i >= len(delays) {
		i = len(delays) - 1
	}
	next := time.Now().Add(delays[i])
	suspended, err := h.FinishNotificationDelivery(ctx, d, state, result.Result, result.Status, int(time.Since(start).Milliseconds()), next, l.NotifySuspendWindow, l.NotifySuspendAttempts, l.NotifySuspendFailurePercent, l.NotifySuspendAfter)
	if err != nil {
		deps.Tracker.Fail("finish delivery", err)
	} else {
		deps.Tracker.Progress()
		deps.Pending(ctx)
	}
	if suspended && deps.Mailer != nil {
		h.NotifySuspension(ctx, n, deps.Mailer, deps.Retention)
	}
	return true
}
