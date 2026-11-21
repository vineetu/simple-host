package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	repo "github.com/vsriram/simple-host/host/data/repo/notify"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// Deliver claims and sends the next due delivery; false when none is due
// or the claim failed.
func (e *Engine) Deliver(ctx context.Context, env Env) bool {
	l := e.limits
	e.delivery.Attempt()
	e.pending(ctx)
	d, err := repo.ClaimNotificationDelivery(ctx, e.db, l.NotifyTimeout+30*time.Second, l.NotifyAttempts, l.NotifyHistoryDays, l.NotifySuspendWindow)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			e.delivery.Fail("claim delivery", err)
		}
		return false
	}
	return e.Process(ctx, env, d)
}

// Process sends one claimed delivery and records the attempt, within the
// claim's lease. A delivery whose site or subscriber no longer allows it
// is recorded as skipped.
func (e *Engine) Process(ctx context.Context, env Env, d model.NotificationDelivery) bool {
	ctx, cancel := context.WithDeadline(ctx, d.LeaseUntil)
	defer cancel()
	n, err := repo.GetNotification(ctx, e.db, d.SubscriptionID)
	if err != nil {
		e.delivery.Fail("load subscription", err)
		return true
	}
	// A pause or expiry may have happened after the claim.
	if n.State != "active" || n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
		return true
	}
	start := time.Now()
	open, ok := e.siteWelcomes(ctx, n)
	if !ok {
		return true
	}
	result, state := Result{Result: "site_unavailable"}, "skipped"
	if open {
		var sent bool
		if result, sent = e.attempt(ctx, env, n, d); !sent {
			return true
		}
		state = attemptState(result, d.Attempts, e.limits.NotifyAttempts)
	}
	e.finishDelivery(ctx, env, n, d, state, result, start)
	return true
}

// attempt sends d to n when this worker still holds the claim and the
// owner's concurrency and the receiver's budget allow it; otherwise the
// delivery is deferred or left to its lease, and sent is false.
func (e *Engine) attempt(ctx context.Context, env Env, n model.Notification, d model.NotificationDelivery) (result Result, sent bool) {
	cursor, ok := e.claimStillHeld(ctx, n, d)
	if !ok {
		return Result{}, false
	}
	acquired := e.acquireOwner(n.OwnerID)
	if acquired {
		defer e.releaseOwner(n.OwnerID)
	}
	if !acquired || !e.perURL.Allow(n.URL) {
		if err := repo.DeferNotificationDelivery(ctx, e.db, d); err != nil {
			e.delivery.Fail("defer delivery", err)
		}
		return Result{}, false
	}
	body := e.body(ctx, env, n, d, cursor)
	if body == nil {
		return Result{}, false
	}
	result = e.send(ctx, env, n, d.EventID, body)
	if !result.Delivered {
		e.delivery.Fail("send webhook", fmt.Errorf("%s (HTTP %d)", result.Result, result.Status))
	}
	return result, true
}

// send hands body to n's kind of receiver.
func (e *Engine) send(ctx context.Context, env Env, n model.Notification, id string, body []byte) Result {
	if n.Kind == "web-push" {
		return e.sendPush(ctx, env, n, body)
	}
	return e.client.send(ctx, env, n, id, body)
}

// body is what n receives for d: a web push its own small payload, a
// webhook the event envelope (WebhookBody). Nil leaves the delivery to be
// retried.
func (e *Engine) body(ctx context.Context, env Env, n model.Notification, d model.NotificationDelivery, cursor int64) []byte {
	if n.Kind == "web-push" {
		return e.pushPayload(ctx, n, d)
	}
	return WebhookBody(env, e.ed.Notifications().EventScoped, n, d, cursor, e.limits.NotifyBodyMaxBytes)
}

// WebhookBody is the envelope a webhook or MCP subscriber n receives for
// d. An MCP subscriber sees visitors as connector references, except on an
// event-scoped edition. Nil: the event could not be read.
func WebhookBody(env Env, eventScoped bool, n model.Notification, d model.NotificationDelivery, cursor int64, bodyMax int) []byte {
	if n.Source == "mcp" && !eventScoped {
		data, ok := visitorAsReference(env, n.SiteID, d.Data)
		if !ok {
			return nil
		}
		d.Data = data
	}
	return Body(d, cursor, bodyMax)
}

// visitorAsReference replaces an event's visitor id with the connector's
// reference for it on site.
func visitorAsReference(env Env, site string, raw json.RawMessage) (json.RawMessage, bool) {
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&data) != nil {
		return nil, false
	}
	if id, ok := data["visitor"].(string); ok && id != "" {
		ref, err := access.NewVisitorReferences(env.VisitorRef).Encode(site, id)
		if err != nil {
			return nil, false
		}
		data["visitor"] = ref
	}
	out, _ := json.Marshal(data)
	return out, true
}

// siteWelcomes reports whether n's site is live and n's subscriber may still
// receive from it. ok is false when a check failed; the failure is recorded.
func (e *Engine) siteWelcomes(ctx context.Context, n model.Notification) (open, ok bool) {
	site, err := sitesrepo.GetSiteByID(ctx, e.db, n.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, true
	}
	if err != nil {
		e.delivery.Fail("load delivery site", err)
		return false, false
	}
	allowed, ok := e.subscriberAllowed(ctx, n, site.UserID)
	return ok && allowed && site.Live() && !site.Deleted, ok
}

// subscriberAllowed reports whether n's subscriber may still receive from
// the site owned by siteOwner. A push subscription's OwnerID is the
// subscriber: the site owner's account (or a member's, who keeps the
// subscription while they may act for the site), or for subscriber_role
// "visitor" a signed-in visitor's own users.id, which never equals
// the site owner. The event fan-out proved the visitor subscription may
// exist; access.VisitorStillWelcome rechecks whatever has changed since.
func (e *Engine) subscriberAllowed(ctx context.Context, n model.Notification, siteOwner string) (allowed, ok bool) {
	var err error
	switch {
	case n.SubscriberRole == "visitor":
		allowed, err = access.VisitorStillWelcome(ctx, e.db, n.SiteID, n.OwnerID)
		if err != nil {
			e.delivery.Fail("check visitor access", err)
		}
	case siteOwner == n.OwnerID:
		return true, true
	default:
		allowed, err = MayActOnSite(ctx, e.db, n.SiteID, n.OwnerID)
		if err != nil {
			e.delivery.Fail("check member access", err)
		}
	}
	return allowed, err == nil
}

// claimStillHeld loads n's replay cursor and confirms this worker still holds
// d's lease. ok is false when it does not or a check failed.
func (e *Engine) claimStillHeld(ctx context.Context, n model.Notification, d model.NotificationDelivery) (cursor int64, ok bool) {
	cursor, err := repo.NotificationCursor(ctx, e.db, n.ID, n.SiteID)
	if err != nil {
		e.delivery.Fail("load delivery cursor", err)
		return 0, false
	}
	held, err := repo.NotificationLeaseHeld(ctx, e.db, d)
	if err != nil {
		e.delivery.Fail("check delivery lease", err)
	}
	return cursor, err == nil && held
}

// attemptState is the delivery state after a send: failures retry until the
// attempts run out, except for answers that say retrying cannot help.
func attemptState(result Result, attempts, maxAttempts int) string {
	switch {
	case result.Delivered:
		return "delivered"
	case attempts < maxAttempts && result.Status != 410 && result.Status != 413 && result.Result != "refused_address":
		return "pending"
	}
	return "failed"
}

var retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 7 * time.Minute}

// finishDelivery records the attempt and mails the owner when it suspended
// the subscription.
func (e *Engine) finishDelivery(ctx context.Context, env Env, n model.Notification, d model.NotificationDelivery, state string, result Result, start time.Time) {
	l := e.limits
	next := time.Now().Add(retryDelays[min(d.Attempts-1, len(retryDelays)-1)])
	suspended, err := repo.FinishNotificationDelivery(ctx, e.db, d, state, result.Result, result.Status, int(time.Since(start).Milliseconds()), next, l.NotifySuspendWindow, l.NotifySuspendAttempts, l.NotifySuspendFailurePercent, l.NotifySuspendAfter)
	if err != nil {
		e.delivery.Fail("finish delivery", err)
	} else {
		e.delivery.Progress()
		e.pending(ctx)
	}
	if suspended {
		e.NotifySuspension(ctx, env.Mailer, n)
	}
}

// pending reports the queue's depth on the delivery tracker.
func (e *Engine) pending(ctx context.Context) {
	n, err := repo.PendingNotificationDeliveries(ctx, e.db)
	if err != nil {
		e.delivery.Fail("count pending deliveries", err)
		return
	}
	e.delivery.SetPending(n)
}
