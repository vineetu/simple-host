package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
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
	dropped                atomic.Uint64
	// wg counts every goroutine the engine starts (workers, sweeper,
	// emits) so WaitNotifications can join them; the server never waits.
	wg sync.WaitGroup
}

// WaitNotifications blocks until every notification goroutine has
// returned: the workers and sweeper (which stop when the context passed to
// StartNotifications is cancelled) and any emit still storing an event.
// Tests call it before closing the database; the server does not.
func (h *SiteHandler) WaitNotifications() { h.notifyEngine().wg.Wait() }

func (h *SiteHandler) notifyEngine() *notificationEngine {
	h.notifyOnce.Do(func() {
		l := h.limits
		// A one-attempt URL burst keeps the first minute within the configured rate.
		h.notifications = &notificationEngine{ownerConcurrency: l.NotifyOwnerConcurrency, client: notifyClient{secrets: &h.passcode, timeout: l.NotifyTimeout}, minute: newRateLimiter(float64(l.NotifyPerMinute), float64(l.NotifyPerMinute)/60), day: newRateLimiter(float64(l.NotifyPerDay), float64(l.NotifyPerDay)/86400), challenge: newRateLimiter(10, 10.0/60), wake: make(chan struct{}, l.NotifyWorkers), emitting: make(chan struct{}, 64), verifying: make(chan struct{}, 10), rest: newRateLimiter(60, 1), perURL: newRateLimiter(1, float64(l.NotifyPerURLPerMinute)/60), owners: make(map[string]int)}
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

// Called only after a storage write commits. Notification trouble must never
// change the response to a successful visitor save.
func (h *SiteHandler) emitNotification(c storageCall, name string, data map[string]any) {
	if c.owner {
		return
	}
	e := h.notifyEngine()
	select {
	case e.emitting <- struct{}{}:
	default:
		log.Printf("notifications: emit queue full, dropped=%d", e.dropped.Add(1))
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() { <-e.emitting }()
		h.storeNotification(c, name, data)
	}()
}
func (h *SiteHandler) storeNotification(c storageCall, name string, data map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	yes, err := db.HasNotificationsFor(ctx, h.database, c.siteID, name)
	if err != nil {
		log.Printf("notifications: subscription lookup failed for site %s", c.siteID)
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
	site, err := db.GetSiteByID(ctx, h.database, c.siteID)
	if err != nil {
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
	if err = db.EmitSiteEvent(ctx, h.database, c.siteID, notifyEventID(), name, c.resourceName, raw); err != nil {
		log.Printf("notifications: emit failed for site %s", c.siteID)
		return
	}
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
	select {
	case e.emitting <- struct{}{}:
	default:
		log.Printf("notifications: emit queue full, dropped=%d", e.dropped.Add(1))
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() { <-e.emitting }()
		h.storeSiteNotification(siteID, name, data, dedupe)
	}()
}
func (h *SiteHandler) storeSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	yes, err := db.HasNotificationsFor(ctx, h.database, siteID, name)
	if err != nil {
		log.Printf("notifications: subscription lookup failed for site %s", siteID)
		return
	}
	if !yes {
		return
	}
	site, err := db.GetSiteByID(ctx, h.database, siteID)
	if err != nil {
		return
	}
	visitor, _ := data["visitor"].(string)
	if visitor != "" && visitor == site.UserID {
		return
	}
	if dedupe > 0 && visitor != "" {
		recent, err := db.RecentSiteEvent(ctx, h.database, siteID, name, visitor, dedupe)
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
		stored, err = db.EmitSiteEventOnce(ctx, h.database, siteID, notifyEventID(), name, raw, visitor, dedupe)
	} else {
		err = db.EmitSiteEvent(ctx, h.database, siteID, notifyEventID(), name, "", raw)
	}
	if err != nil {
		log.Printf("notifications: emit failed for site %s", siteID)
		return
	}
	if stored {
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
func (t *storageTableCall) notifyRow(name, change string, id int64, values map[string]json.RawMessage) {
	if t.c.owner {
		return
	}
	data := map[string]any{"table": t.table, "row_id": id}
	if change != "" {
		data["change"] = change
	}
	if values != nil {
		fields := map[string]any{}
		for k, v := range values {
			if k != "visitor_id" {
				fields[k] = notifyValue(v)
			}
		}
		data["fields"] = fields
	}
	t.h.emitNotification(t.c, name, data)
}

// Convert at delivery so REST history and plain webhooks retain their format.
// Old queued events receive the same projection as newly saved events.
func (h *SiteHandler) notificationBody(n db.Notification, d db.NotificationDelivery, cursor int64) []byte {
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

func notifyBody(d db.NotificationDelivery, cursor int64, bodyMax int) []byte {
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
func (h *SiteHandler) StartNotifications(ctx context.Context) {
	e := h.notifyEngine()
	e.start.Do(func() {
		h.sweepNotifications(ctx)
		for i := 0; i < h.limits.NotifyWorkers; i++ {
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
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
			}()
		}
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
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
		}()
	})
}
func (h *SiteHandler) deliverNotification(ctx context.Context) bool {
	l := h.limits
	d, err := db.ClaimNotificationDelivery(ctx, h.database, l.NotifyTimeout+30*time.Second, l.NotifyAttempts, l.NotifyHistoryDays, l.NotifySuspendWindow)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			log.Print("notifications: claim failed")
		}
		return false
	}
	return h.processNotification(ctx, d)
}
func (h *SiteHandler) processNotification(ctx context.Context, d db.NotificationDelivery) bool {
	l := h.limits
	ctx, cancel := context.WithDeadline(ctx, d.LeaseUntil)
	defer cancel()
	n, err := db.GetNotification(ctx, h.database, d.SubscriptionID)
	if err != nil {
		return true
	}
	// A pause or expiry may have happened after the claim.
	if n.State != "active" || n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
		return true
	}
	start := time.Now()
	result := notifyResult{Result: "site_unavailable"}
	state := "skipped"
	site, err := db.GetSiteByID(ctx, h.database, n.SiteID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return true
	}
	allowed := site.UserID == n.OwnerID
	if err == nil && h.ed.Notifications().EventScoped {
		allowed, err = db.HackNotificationAllowed(ctx, h.database, n.SiteID, n.OwnerID, false)
		if err != nil {
			return true
		}
	}
	if err == nil && !site.Offline && !site.Suspended() && !site.Deleted && allowed {
		cursor, e := db.NotificationCursor(ctx, h.database, n.ID, n.SiteID)
		if e != nil {
			return true
		}
		held, err := db.NotificationLeaseHeld(ctx, h.database, d)
		if err != nil || !held {
			return true
		}
		engine := h.notifyEngine()
		acquired := engine.acquireOwner(n.OwnerID)
		if acquired {
			defer engine.releaseOwner(n.OwnerID)
		}
		if !acquired || !engine.perURL.allow(n.URL) {
			if err := db.DeferNotificationDelivery(ctx, h.database, d); err != nil {
				log.Printf("notifications: deferred claim update failed for subscription %s", n.ID)
			}
			return true
		}
		body := h.notificationBody(n, d, cursor)
		if body == nil {
			return true
		}
		result = engine.client.send(ctx, n, d.EventID, body)
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
	suspended, err := db.FinishNotificationDelivery(ctx, h.database, d, state, result.Result, result.Status, int(time.Since(start).Milliseconds()), next, l.NotifySuspendWindow, l.NotifySuspendAttempts, l.NotifySuspendFailurePercent, l.NotifySuspendAfter)
	if err != nil {
		log.Printf("notifications: result update failed for subscription %s", n.ID)
	}
	if suspended {
		h.notifySuspension(ctx, n)
	}
	return true
}
func (h *SiteHandler) notifySuspension(ctx context.Context, n db.Notification) {
	mailer, ok := h.mailer.(noticeSender)
	if !ok {
		return
	}
	to, err := db.GetSiteOwnerEmail(ctx, h.database, n.SiteID)
	if err != nil || !strings.Contains(to, "@") {
		return
	}
	claimed, err := db.ClaimNotificationEmail(ctx, h.database, n.ID)
	if err != nil || !claimed {
		return
	}
	if err = mailer.SendNotice(to, "Simple Host notification paused", "Notifications for "+n.Site+" have been suspended after repeated delivery failures. Check the receiver, then resume the subscription in Notifications."); err != nil {
		log.Printf("notifications: suspension email failed for subscription %s", n.ID)
		retryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := h.database.ExecContext(retryCtx, `UPDATE site_notifications SET suspend_emailed_at=NULL WHERE id=$1`, n.ID); err != nil {
			log.Printf("notifications: suspension email retry update failed for subscription %s", n.ID)
		}
	}
}

func (h *SiteHandler) sweepNotifications(ctx context.Context) {
	if err := db.SweepNotifications(ctx, h.database, h.limits.NotifyHistoryDays); err != nil {
		log.Print("notifications: history sweep failed")
	}
	rows, err := h.database.QueryContext(ctx, `SELECT id FROM site_notifications WHERE state='suspended' AND suspend_emailed_at IS NULL`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if n, err := db.GetNotification(ctx, h.database, id); err == nil {
			h.notifySuspension(ctx, n)
		}
	}
}

// Hack's organiser website uses an internal UUID for storage; delivery payloads
// use the event slug and public address, as the website tools do.
func (h *SiteHandler) notificationSiteInfo(ctx context.Context, site db.Site) (string, string, error) {
	if !h.ed.Notifications().EventScoped {
		return site.Name, h.siteURLFor(site), nil
	}
	ev, err := db.GetEventByAccount(ctx, h.database, site.UserID)
	if err != nil {
		return "", "", err
	}
	if site.Name == ev.ID {
		return ev.Slug, "https://" + ev.Slug + "." + h.siteDomain + "/", nil
	}
	return site.Name, h.TeamSiteURL(ev.Slug, site.Name), nil
}
