package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/app/notifications"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/workers"
)

type notificationEngine struct {
	// ownerConcurrency is NotifyOwnerConcurrency from the limits snapshot the
	// handler was built with (acquireOwner has no SiteHandler of its own).
	ownerConcurrency       int
	client                 notifyClient
	minute, day, challenge *rateLimiter
	perURL                 *rateLimiter
	ownersMu               sync.Mutex
	owners                 map[string]int
	wake                   chan struct{}
	emitting               chan struct{}
	verifying              chan struct{}
	rest                   *rateLimiter
	start                  sync.Once
	stop                   sync.Once
	stopped                chan struct{}
	emitMu                 sync.Mutex
	emitIdle               chan struct{}
	emitPending            int
	dropped                atomic.Uint64
	// Both runners are joined before database shutdown; emits may precede Start.
	runner                     *workers.Runner
	delivery, retention, emits *workers.Tracker
	emitRunner                 *workers.Runner
	submissions                *submissionEmailOwner
}

// WaitNotifications blocks until every notification goroutine has
// returned: the workers and sweeper (which stop when the context passed to
// StartNotifications is cancelled) and any emit still storing an event.
// Tests call it before closing the database; the server does not.
func (h *SiteHandler) WaitNotifications() { h.NotificationOwner().Wait() }

// SetNotifyClientForTest swaps h's webhook-delivery client for a test
// double trusting roots (a local httptest.TLS server's certificate pool)
// and allowing loopback addresses, and lifts its per-URL rate limit so a
// burst of test deliveries never throttles. Test hook (cleanup 3.2 slice
// 3): the moved hack_notifications_test.go needs the same client swap
// notify_test.go's notificationFixture does in-package, but notifyClient
// is unexported, so a cross-package caller cannot build or assign one
// itself.
func (h *SiteHandler) SetNotifyClientForTest(roots *x509.CertPool) {
	h.notifyEngine().client = notifyClient{secrets: &h.passcode, allowLoopback: true, roots: roots}
	h.notifyEngine().perURL = newRateLimiter(100000, 0)
}

// PendingNotificationsForTest is len(h.notifyEngine().emitting): how many
// webhook deliveries are in flight right now. Test hook (cleanup 3.2 slice
// 3): the moved hack_notifications_test.go polls this waiting for delivery
// to finish, the same way notify_test.go's own tests do in-package.
func (h *SiteHandler) PendingNotificationsForTest() int {
	return len(h.notifyEngine().emitting)
}

func (h *SiteHandler) notifyEngine() *notificationEngine {
	h.notifyOnce.Do(func() {
		l := h.limits
		// A one-attempt URL burst keeps the first minute within the configured rate.
		h.notifications = &notificationEngine{ownerConcurrency: l.NotifyOwnerConcurrency, client: notifyClient{secrets: &h.passcode, timeout: l.NotifyTimeout}, minute: newRateLimiter(float64(l.NotifyPerMinute), float64(l.NotifyPerMinute)/60), day: newRateLimiter(float64(l.NotifyPerDay), float64(l.NotifyPerDay)/86400), challenge: newRateLimiter(10, 10.0/60), wake: make(chan struct{}, l.NotifyWorkers), emitting: make(chan struct{}, 64), verifying: make(chan struct{}, 10), rest: newRateLimiter(60, 1), perURL: newRateLimiter(1, float64(l.NotifyPerURLPerMinute)/60), owners: make(map[string]int)}

		e := h.notifications
		e.stopped = make(chan struct{})
		e.emitIdle = make(chan struct{})
		close(e.emitIdle)
		e.delivery = workers.NewTracker("notifications.delivery")
		e.retention = workers.NewTracker("notifications.retention")
		e.emits = workers.NewTracker("notifications.emit")
		e.runner = workers.NewRunner(e.delivery, e.retention)
		e.emitRunner = workers.NewRunner(e.emits)
		e.submissions = &submissionEmailOwner{h: h, tracker: workers.NewTracker("mail.submissions")}
		e.submissions.runner = workers.NewRunner(e.submissions.tracker)
	})
	return h.notifications
}
func (e *notificationEngine) acquireOwner(owner string) bool {
	e.ownersMu.Lock()
	defer e.ownersMu.Unlock()
	if e.owners[owner] >= e.ownerConcurrency {
		return false
	}
	e.owners[owner]++
	return true
}
func (e *notificationEngine) releaseOwner(owner string) {
	e.ownersMu.Lock()
	defer e.ownersMu.Unlock()
	e.owners[owner]--
	if e.owners[owner] == 0 {
		delete(e.owners, owner)
	}
}
func notifyRandom(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}
func notifyEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "evt_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}
func (e *notificationEngine) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Admission and the idle signal share a lock so shutdown cannot race a new
// Runner.Go against the drain. Accepted persistence keeps its own context.
func (e *notificationEngine) emit(fn func(context.Context)) {
	e.emitMu.Lock()
	defer e.emitMu.Unlock()
	select {
	case <-e.stopped:
		return
	default:
	}
	select {
	case e.emitting <- struct{}{}:
	default:
		e.emits.Fail("enqueue event", fmt.Errorf("queue full, dropped=%d", e.dropped.Add(1)))
		return
	}
	if e.emitPending == 0 {
		e.emitIdle = make(chan struct{})
	}
	e.emitPending++
	e.emitRunner.Go(context.Background(), func(ctx context.Context) {
		defer func() {
			e.emitMu.Lock()
			defer e.emitMu.Unlock()
			<-e.emitting
			e.emitPending--
			if e.emitPending == 0 {
				close(e.emitIdle)
			}
		}()
		e.emits.Attempt()
		fn(ctx)
	})
}

// Called only after a storage write commits. Notification trouble must never
// change the response to a successful visitor save.
func (h *SiteHandler) emitNotification(c storageCall, name string, data map[string]any) {
	if c.owner {
		return
	}
	e := h.notifyEngine()
	e.emit(func(ctx context.Context) {
		h.storeNotificationContext(ctx, c, name, data)
	})
}
func (h *SiteHandler) storeNotification(c storageCall, name string, data map[string]any) {
	h.storeNotificationContext(context.Background(), c, name, data)
}
func (h *SiteHandler) storeNotificationContext(parent context.Context, c storageCall, name string, data map[string]any) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	yes, err := h.notificationsSvc().HasNotificationsFor(ctx, c.siteID, name)
	if err != nil {
		h.notifyEngine().emits.Fail("lookup subscriptions", err)
		return
	}
	if !yes {
		return
	}
	e := h.notifyEngine()
	if !e.minute.allow(c.siteID) || !e.day.allow(c.siteID) {
		log.Printf("notifications: site %s event cap reached, dropped=%d", c.siteID, e.dropped.Add(1))
		return
	}
	site, err := h.sitesSvc().GetSiteByID(ctx, c.siteID)
	if err != nil {
		e.emits.Fail("load event site", err)
		return
	}
	data["v"] = 1
	siteName, siteURL, err := h.notificationSiteInfo(ctx, site)
	if err != nil {
		return
	}
	data["site"] = siteName
	data["site_url"] = siteURL
	data["storage"] = c.resourceName
	data["kind"] = c.resource.Kind
	data["preset"] = c.resource.PresetBase
	data["visitor"] = nil
	if c.stampID() != "" {
		data["visitor"] = c.stampID()
	}
	untrusted := []string{}
	for _, key := range []string{"fields", "key", "path"} {
		if _, ok := data[key]; ok {
			untrusted = append(untrusted, key)
		}
	}
	data["untrusted"] = untrusted
	if value, ok := data["fields"].(json.RawMessage); ok {
		data["fields"] = notifyFields(value)
	}
	notifyTruncate(data, h.limits.NotifyFieldMaxBytes)
	raw, err := json.Marshal(data)
	if len(raw) > h.limits.NotifyBodyMaxBytes {
		delete(data, "fields")
		data["truncated"] = []string{"fields"}
		raw, err = json.Marshal(data)
	}
	if err != nil {
		return
	}
	if err = h.notificationsSvc().EmitSiteEvent(ctx, c.siteID, notifyEventID(), name, c.resourceName, raw); err != nil {
		e.emits.Fail("store event", err)
		return
	}
	e.emits.Progress()
	e.signal()
}

// Site events (SIM-356, phase 2): a new version live, a custom domain's
// status, a refused named-viewers sign-in, a first visitor sign-in. The
// caller has already finished its own work and answered; nothing here can
// change that. Simple Host only: Simple Hack follows later (INTENT
// 2026-11-02). data carries the event's own fields; the engine adds v, site
// and site_url. A visitor-bearing event about the site's owner (signed in
// on their own site) is dropped, and dedupe > 0 drops a repeat of the same
// event for the same visitor inside that window.
func (h *SiteHandler) emitSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	if h.ed.Notifications().EventScoped {
		return
	}
	e := h.notifyEngine()
	e.emit(func(ctx context.Context) {
		h.storeSiteNotificationContext(ctx, siteID, name, data, dedupe)
	})
}
func (h *SiteHandler) storeSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	h.storeSiteNotificationContext(context.Background(), siteID, name, data, dedupe)
}
func (h *SiteHandler) storeSiteNotificationContext(parent context.Context, siteID, name string, data map[string]any, dedupe time.Duration) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	yes, err := h.notificationsSvc().HasNotificationsFor(ctx, siteID, name)
	if err != nil {
		h.notifyEngine().emits.Fail("lookup subscriptions", err)
		return
	}
	if !yes {
		return
	}
	site, err := h.sitesSvc().GetSiteByID(ctx, siteID)
	if err != nil {
		h.notifyEngine().emits.Fail("load event site", err)
		return
	}
	visitor, _ := data["visitor"].(string)
	if visitor != "" && visitor == site.UserID {
		return
	}
	if dedupe > 0 && visitor != "" {
		recent, err := h.notificationsSvc().RecentSiteEvent(ctx, siteID, name, visitor, dedupe)
		if err != nil || recent {
			return
		}
	}
	e := h.notifyEngine()
	if !e.minute.allow(siteID) || !e.day.allow(siteID) {
		log.Printf("notifications: site %s event cap reached, dropped=%d", siteID, e.dropped.Add(1))
		return
	}
	data["v"] = 1
	siteName, siteURL, err := h.notificationSiteInfo(ctx, site)
	if err != nil {
		return
	}
	data["site"] = siteName
	data["site_url"] = siteURL
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	if len(raw) > h.limits.NotifyBodyMaxBytes {
		log.Printf("notifications: site %s %s event too large, dropped=%d", siteID, name, e.dropped.Add(1))
		return
	}
	stored := true
	if dedupe > 0 && visitor != "" {
		stored, err = h.notificationsSvc().EmitSiteEventOnce(ctx, siteID, notifyEventID(), name, raw, visitor, dedupe)
	} else {
		err = h.notificationsSvc().EmitSiteEvent(ctx, siteID, notifyEventID(), name, "", raw)
	}
	if err != nil {
		e.emits.Fail("store event", err)
		return
	}
	if stored {
		e.emits.Progress()
		e.signal()
	}
}

// notifyCut shortens s to at most max bytes on a UTF-8 boundary.
func notifyCut(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}
func notifyTruncate(data map[string]any, fieldMax int) {
	fields, ok := data["fields"].(map[string]any)
	if !ok {
		return
	}
	truncated := []string{}
	var cut func(any, string) any
	cut = func(value any, path string) any {
		switch v := value.(type) {
		case string:
			if len(v) > fieldMax {
				n := fieldMax
				for n > 0 && !utf8.ValidString(v[:n]) {
					n--
				}
				truncated = append(truncated, path)
				return v[:n]
			}
		case map[string]any:
			for k, x := range v {
				v[k] = cut(x, path+"."+k)
			}
		case []any:
			for i, x := range v {
				v[i] = cut(x, fmt.Sprintf("%s.%d", path, i))
			}
		}
		return value
	}
	for key, value := range fields {
		fields[key] = cut(value, key)
	}
	if len(truncated) > 0 {
		sort.Strings(truncated)
		data["truncated"] = truncated
	}

}
func notifyValue(raw json.RawMessage) any {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !json.Valid(raw) || dec.Decode(&value) != nil {
		return string(raw)
	}
	return value
}
func notifyFields(raw json.RawMessage) map[string]any {
	value := notifyValue(raw)
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	return map[string]any{"value": value}
}

// Convert at delivery so REST history and plain webhooks retain their format.
// Old queued events receive the same projection as newly saved events.
func (h *SiteHandler) notificationBody(n model.Notification, d model.NotificationDelivery, cursor int64) []byte {
	if n.Source == "mcp" && !h.ed.Notifications().EventScoped {
		var data map[string]any
		decoder := json.NewDecoder(bytes.NewReader(d.Data))
		decoder.UseNumber()
		if decoder.Decode(&data) != nil {
			return nil
		}
		if id, ok := data["visitor"].(string); ok && id != "" {
			ref, err := mcp.NewVisitorReferences(h.visitorRefKey()).Encode(n.SiteID, id)
			if err != nil {
				return nil
			}
			data["visitor"] = ref
		}
		d.Data, _ = json.Marshal(data)
	}
	return notifyBody(d, cursor, h.limits.NotifyBodyMaxBytes)
}

func notifyBody(d model.NotificationDelivery, cursor int64, bodyMax int) []byte {
	body := map[string]any{"eventId": d.EventID, "name": d.Name, "timestamp": d.CreatedAt.UTC().Format(time.RFC3339), "cursor": fmt.Sprintf("c_%d", cursor), "data": d.Data}
	raw, _ := json.Marshal(body)
	if len(raw) > bodyMax {
		data := notifyFields(d.Data)
		delete(data, "fields")
		data["truncated"] = []string{"fields"}
		body["data"] = data
		raw, _ = json.Marshal(body)
		if len(raw) > bodyMax {
			truncated := []string{"fields"}
			for _, key := range []string{"path", "key"} {
				if value, ok := data[key].(string); ok && len(value) > 256 {
					n := 256
					for n > 0 && !utf8.ValidString(value[:n]) {
						n--
					}
					data[key] = value[:n]
					truncated = append(truncated, key)
				}
			}
			data["truncated"] = truncated
			raw, _ = json.Marshal(body)
		}
	}
	return raw
}

type notificationOwner struct {
	h *SiteHandler
	*workers.Runner
}

func (o *notificationOwner) Stop() {
	e := o.h.notifyEngine()
	e.stop.Do(func() {
		e.emitMu.Lock()
		close(e.stopped)
		idle := e.emitIdle
		e.emitMu.Unlock()
		o.Runner.Stop()
		// Each event already has a five-second persistence deadline. Allow
		// that full budget after HTTP drain before cancelling accepted writes.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-idle:
		case <-timer.C:
		}
		e.emitRunner.Stop()
	})
}
func (o *notificationOwner) Wait() { o.Runner.Wait(); o.h.notifyEngine().emitRunner.Wait() }
func (o *notificationOwner) Status() []workers.Status {
	e := o.h.notifyEngine()
	e.emits.SetPending(int64(len(e.emitting)))
	return append(o.Runner.Status(), e.emitRunner.Status()...)
}
func (h *SiteHandler) NotificationOwner() workers.Owner {
	return &notificationOwner{h, h.notifyEngine().runner}
}
func (h *SiteHandler) StartNotifications(ctx context.Context) { h.NotificationOwner().Start(ctx) }
func (o *notificationOwner) Start(ctx context.Context) {
	h := o.h
	e := h.notifyEngine()
	e.start.Do(func() {
		select {
		case <-e.stopped:
			return
		default:
		}
		e.runner.Go(ctx, func(ctx context.Context) { <-ctx.Done(); o.Stop() })
		// Keep the initial retention pass synchronous, using the owner's context.
		ready := make(chan struct{})
		e.runner.Go(ctx, func(ctx context.Context) { h.sweepNotifications(ctx); close(ready) })
		select {
		case <-ready:
		case <-e.stopped:
			return
		case <-ctx.Done():
		}
		for i := 0; i < h.limits.NotifyWorkers; i++ {
			e.runner.Go(ctx, func(ctx context.Context) {
				tick := time.NewTicker(time.Second)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
					case <-e.wake:
					}
					for ctx.Err() == nil && h.deliverNotification(ctx) {
					}
				}
			})
		}
		e.runner.Go(ctx, func(ctx context.Context) {
			tick := time.NewTicker(time.Hour)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					h.sweepNotifications(ctx)
					e.minute.evictIdle(time.Hour)
					e.day.evictIdle(48 * time.Hour)
					e.challenge.evictIdle(time.Hour)
					e.perURL.evictIdle(time.Hour)
					e.rest.evictIdle(time.Hour)
				}
			}
		})
	})
}

// DeliverNotificationForTest is deliverNotification, exported as a test
// hook (cleanup 3.2 slice 3): the moved hack_notifications_test.go drains
// the delivery queue directly, the same way notify_test.go's own tests do
// in-package.
func (h *SiteHandler) DeliverNotificationForTest(ctx context.Context) bool {
	return h.deliverNotification(ctx)
}

func (h *SiteHandler) deliverNotification(ctx context.Context) bool {
	return h.notificationsSvc().Deliver(ctx, h.ed, h.notificationDeliveryDeps())
}
func (h *SiteHandler) processNotification(ctx context.Context, d model.NotificationDelivery) bool {
	return h.notificationsSvc().ProcessDelivery(ctx, d, h.ed, h.notificationDeliveryDeps())
}
func (h *SiteHandler) notifySuspension(ctx context.Context, n model.Notification) {
	mailer, ok := h.mailer.(noticeSender)
	if ok {
		h.notificationsSvc().NotifySuspension(ctx, n, mailer, h.notifyEngine().retention)
	}
}

func (h *SiteHandler) sweepNotifications(ctx context.Context) {
	mailer, _ := h.mailer.(noticeSender)
	h.notificationsSvc().SweepSuspensionEmails(ctx, h.limits.NotifyHistoryDays, mailer, h.notifyEngine().retention)
}

// Hack's organiser website uses an internal UUID for storage; delivery payloads
// use the event slug and public address, as the website tools do.
func (h *SiteHandler) notificationSiteInfo(ctx context.Context, site model.Site) (string, string, error) {
	if !h.ed.Notifications().EventScoped {
		return site.Name, h.siteURLFor(site), nil
	}
	ev, err := h.storeAccounts().GetEventByAccount(ctx, site.UserID)
	if err != nil {
		return "", "", err
	}
	if site.Name == ev.ID {
		return ev.Slug, "https://" + ev.Slug + "." + h.siteDomain + "/", nil
	}
	return site.Name, h.TeamSiteURL(ev.Slug, site.Name), nil
}

func (h *SiteHandler) notificationPending(ctx context.Context) {
	t := h.notifyEngine().delivery
	n, err := h.notificationsSvc().PendingNotificationDeliveries(ctx)
	if err != nil {
		t.Fail("count pending deliveries", err)
		return
	}
	t.SetPending(n)
}

// Mail errors may echo recipients or provider credentials; expose only known causes.
func notificationMailError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return errors.New("mail provider timeout")
	}
	if err.Error() == "RESEND_API_KEY not configured" {
		return errors.New("mail provider is not configured")
	}
	var status int
	if n, _ := fmt.Sscanf(err.Error(), "resend returned status %d", &status); n == 1 {
		return fmt.Errorf("mail provider returned HTTP %d", status)
	}
	return errors.New("mail provider delivery failed")
}

func (h *SiteHandler) notificationDeliveryDeps() notifications.DeliveryDeps {
	e := h.notifyEngine()
	mailer, _ := h.mailer.(noticeSender)
	return notifications.DeliveryDeps{Tracker: e.delivery, Retention: e.retention, Mailer: mailer, AcquireOwner: e.acquireOwner, ReleaseOwner: e.releaseOwner, AllowURL: e.perURL.allow, Body: h.notificationBody, Pending: h.notificationPending, Send: func(ctx context.Context, n model.Notification, id string, body []byte) notifications.DeliveryResult {
		result := e.client.send(ctx, n, id, body)
		return notifications.DeliveryResult{Delivered: result.Delivered, Status: result.Status, Result: result.Result}
	}}
}
