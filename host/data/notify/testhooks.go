package notify

import (
	"context"
	"crypto/x509"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// Test hooks: the engine's budgets and queues, for tests outside this
// package that drive it through the HTTP routes.

// UseTestClient trusts roots (a local httptest TLS server's pool) and,
// with loopback, loopback receivers, and lifts the per-URL budget so a
// test's burst of deliveries never throttles.
func (e *Engine) UseTestClient(roots *x509.CertPool, loopback bool) {
	e.client = client{allowLoopback: loopback, roots: roots}
	e.perURL = httpx.NewRateLimiter(100000, 0)
}

// URLBudgetForTest restores the configured per-URL budget on clock now
// and returns it.
func (e *Engine) URLBudgetForTest(now func() time.Time) *httpx.RateLimiter {
	e.perURL = urlBudget(e.limits)
	e.perURL.Now = now
	return e.perURL
}

// EventCapsForTest replaces the per-site event caps.
func (e *Engine) EventCapsForTest(minute, day *httpx.RateLimiter) { e.minute, e.day = minute, day }

// PendingForTest is how many events are still being stored.
func (e *Engine) PendingForTest() int { return len(e.emitting) }

// DroppedForTest is how many events were dropped.
func (e *Engine) DroppedForTest() uint64 { return e.dropped.Load() }

// FillEmitQueueForTest takes every emit slot until release.
func (e *Engine) FillEmitQueueForTest() (release func()) { return fill(e.emitting) }

// HoldVerifySlotsForTest takes every verification slot until release.
func (e *Engine) HoldVerifySlotsForTest() (release func()) { return fill(e.verifying) }

func fill(slots chan struct{}) func() {
	for i := 0; i < cap(slots); i++ {
		slots <- struct{}{}
	}
	return func() {
		for i := 0; i < cap(slots); i++ {
			<-slots
		}
	}
}

// OwnersInFlightForTest is how many owners hold a delivery slot.
func (e *Engine) OwnersInFlightForTest() int {
	e.ownersMu.Lock()
	defer e.ownersMu.Unlock()
	return len(e.owners)
}

// StoppedForTest is closed once Stop has closed admission.
func (e *Engine) StoppedForTest() <-chan struct{} { return e.stopped }

// PushBudgetsForTest replaces web push's per-site and per-person budgets.
func (e *Engine) PushBudgetsForTest(site, person *httpx.RateLimiter) {
	e.pushSiteDay, e.pushPersonDay = site, person
}

// SendPushForTest sends body to push subscription n.
func (e *Engine) SendPushForTest(ctx context.Context, env Env, n model.Notification, body []byte) Result {
	return e.sendPush(ctx, env, n, body)
}

// PushPayloadForTest is what push subscription n receives for d: nil to
// retry, "null" to skip.
func (e *Engine) PushPayloadForTest(ctx context.Context, n model.Notification, d model.NotificationDelivery) []byte {
	return e.pushPayload(ctx, n, d)
}

// EventNamesForTest are the events a subscription may name.
func (e *Engine) EventNamesForTest() []string { return e.eventNames() }

// SendWebhookForTest posts one signed webhook to n.
func (e *Engine) SendWebhookForTest(ctx context.Context, env Env, n model.Notification, id string, body []byte) Result {
	return e.client.send(ctx, env, n, id, body)
}
