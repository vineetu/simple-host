package data

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/workers"
)

// Notifier is the handler's notification engine, built on first use from
// the handler's limits.
func (h *Handler) Notifier() *notifications.Engine {
	h.notifyOnce.Do(func() {
		if h.Notifications == nil {
			h.Notifications = notifications.NewEngine(h.Database, h.Ed, h.Limits, h.Hosts.Addressing().SiteDomain)
		}
	})
	return h.Notifications
}

// NotifyEnv is the install's notification keys and mailer as they are now:
// PASSCODE_ENC_KEY is installed after the engine is built.
func (h *Handler) NotifyEnv() notifications.Env {
	mailer, _ := h.Mailer.(email.NoticeSender)
	if h.PasscodeKeys == nil {
		return notifications.Env{Mailer: mailer}
	}
	return notifications.Env{AEAD: h.PasscodeKeys.AEAD, VisitorRef: h.PasscodeKeys.VisitorRef(h.ExportKey), Mailer: mailer}
}

func (h *Handler) NotificationsReady() bool { return h.NotifyEnv().Ready() }

// SetWebPushAccounts installs WEB_PUSH_ACCOUNTS (cfg.WebPushAccounts,
// cfg.WebPushAccountsAll). Called once at startup.
func (h *Handler) SetWebPushAccounts(accounts map[string]bool, all bool) {
	h.Notifier().SetPushAccounts(accounts, all)
}

// SealNotificationSecrets rewrites legacy data only; startup never migrates schema.
func (h *Handler) SealNotificationSecrets(ctx context.Context, database *sql.DB) (int64, error) {
	env := h.NotifyEnv()
	if !env.Ready() {
		return 0, nil
	}
	return notifications.SealNotificationSecrets(ctx, database, env)
}

// notifyStatus is the HTTP status of each notification refusal code.
var notifyStatus = map[string]int{
	"invalid_request": 400, "invalid_events": 400, "invalid_header": 400, "invalid_url": 400, "refused_address": 400,
	"invalid_secret": 400, "invalid_keys": 400, "invalid_recipients": 400, "invalid_body_column": 400, "too_many_resources": 400,
	"forbidden": 403, "csrf_required": 403, "notification_access_ended": 403,
	"not_found": 404, "not_offered": 404, "storage_not_found": 404, "site_not_found": 404,
	"client_secret": 409, "client_subscription": 409, "too_many_notifications": 409, "notifications_not_enabled": 409,
	"internal_error": 500, "busy": 503,
}

// writeNotifyError is the one notification refusal writer: a refusal at
// its code's status, anything else the generic 500.
func WriteNotifyError(w http.ResponseWriter, err error) {
	var e *notifications.Error
	switch {
	case !errors.As(err, &e):
		NotifyError(w, 500, "internal_error")
	case e.Code == "rate_limited":
		httpx.TooManyRequests(w)
	default:
		httpx.WriteError(w, notifyStatus[e.Code], e.Code, e.Error())
	}
}

func NotifyError(w http.ResponseWriter, status int, code string) {
	httpx.WriteError(w, status, code, strings.ReplaceAll(code, "_", " "))
}

// EmitNotification queues a committed storage write's event
// (EmitEvent decides who hears of it).
func (h *Handler) EmitNotification(c Call, name string, data map[string]any) {
	EmitEvent(h.Notifier(), h.Hosts, Event{Call: c, Name: name, Data: data})
}

// StoreNotification stores a record event synchronously.
func (h *Handler) StoreNotification(c Call, name string, data map[string]any) {
	StoreEvent(context.Background(), h.Notifier(), h.Hosts, Event{Call: c, Name: name, Data: data})
}

// EmitSiteNotification queues a site event: a new version live, a custom
// domain's status, a refused named-viewers sign-in, a first visitor sign-in,
// after the caller has answered. data["visitor"], when set, is who it is
// about; dedupe > 0 drops a repeat for them within that window.
func (h *Handler) EmitSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	h.Notifier().EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: siteID, Name: name, Data: data, Dedupe: dedupe})
}

// StoreSiteNotification stores a site event synchronously.
func (h *Handler) StoreSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	h.Notifier().StoreSiteEventNow(context.Background(), h.Hosts, sites.SiteEvent{SiteID: siteID, Name: name, Data: data, Dedupe: dedupe})
}

// notificationOwner runs the engine's workers with the handler's current
// keys and mailer.
type notificationOwner struct{ h *Handler }

func (o notificationOwner) Start(ctx context.Context) { o.h.Notifier().Start(ctx, o.h.NotifyEnv) }
func (o notificationOwner) Stop()                     { o.h.Notifier().Stop() }
func (o notificationOwner) Wait()                     { o.h.Notifier().Wait() }
func (o notificationOwner) Status() []workers.Status  { return o.h.Notifier().Status() }

func (h *Handler) NotificationOwner() workers.Owner { return notificationOwner{h} }

func (h *Handler) StartNotifications(ctx context.Context) { h.NotificationOwner().Start(ctx) }

// WaitNotifications blocks until every notification goroutine has
// returned: the workers and sweeper (which stop when the context passed to
// StartNotifications is cancelled) and any emit still storing an event.
// Tests call it before closing the database; the server does not.
func (h *Handler) WaitNotifications() { h.Notifier().Wait() }

func (h *Handler) DeliverNotification(ctx context.Context) bool {
	return h.Notifier().Deliver(ctx, h.NotifyEnv())
}

func (h *Handler) SweepNotifications(ctx context.Context) {
	h.Notifier().Sweep(ctx, h.NotifyEnv().Mailer)
}

// SetNotifyClientForTest makes webhook delivery trust roots (a local
// httptest TLS server's pool) and loopback receivers, without a per-URL
// rate limit. Test hook for products' servertest.
func (h *Handler) SetNotifyClientForTest(roots *x509.CertPool) {
	h.Notifier().UseTestClient(roots, true)
}

// PendingNotificationsForTest is how many events are still being stored.
// Test hook.
func (h *Handler) PendingNotificationsForTest() int { return h.Notifier().PendingForTest() }

// DeliverNotificationForTest drains one delivery. Test hook.
func (h *Handler) DeliverNotificationForTest(ctx context.Context) bool {
	return h.DeliverNotification(ctx)
}
