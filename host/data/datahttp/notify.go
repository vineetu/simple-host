package datahttp

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/workers"
)

// Notifier is the handler's notification engine, built on first use from
// the handler's limits.
func (h *DataHandler) Notifier() *notifications.Engine {
	h.notifyOnce.Do(func() {
		if h.Notifications == nil {
			h.Notifications = notifications.NewEngine(h.Database, h.Ed, h.Limits, h.Hosts.Addressing().SiteDomain)
		}
	})
	return h.Notifications
}

// NotifyEnv is the install's notification keys and mailer as they are now:
// PASSCODE_ENC_KEY is installed after the engine is built.
func (h *DataHandler) NotifyEnv() notifications.Env {
	mailer, _ := h.Mailer.(email.NoticeSender)
	if h.PasscodeKeys == nil {
		return notifications.Env{Mailer: mailer}
	}
	return notifications.Env{AEAD: h.PasscodeKeys.AEAD, VisitorRef: h.PasscodeKeys.VisitorRef(h.ExportKey), Mailer: mailer}
}

func (h *DataHandler) NotificationsReady() bool { return h.NotifyEnv().Ready() }

// SetWebPushAccounts installs WEB_PUSH_ACCOUNTS (cfg.WebPushAccounts,
// cfg.WebPushAccountsAll). Called once at startup.
func (h *DataHandler) SetWebPushAccounts(accounts map[string]bool, all bool) {
	h.Notifier().SetPushAccounts(accounts, all)
}

// SealNotificationSecrets rewrites legacy data only; startup never migrates schema.
func (h *DataHandler) SealNotificationSecrets(ctx context.Context, database *sql.DB) (int64, error) {
	env := h.NotifyEnv()
	if !env.Ready() {
		return 0, nil
	}
	return notifications.New(database).SealNotificationSecrets(ctx, env.SealSecret)
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
func writeNotifyError(w http.ResponseWriter, err error) {
	var e *notifications.Error
	switch {
	case !errors.As(err, &e):
		notifyError(w, 500, "internal_error")
	case e.Code == "rate_limited":
		httpx.TooManyRequests(w)
	default:
		httpx.WriteError(w, notifyStatus[e.Code], e.Code, e.Error())
	}
}

func notifyError(w http.ResponseWriter, status int, code string) {
	httpx.WriteError(w, status, code, strings.ReplaceAll(code, "_", " "))
}

// notifyCut shortens s to at most max bytes on a UTF-8 boundary.
func notifyCut(s string, max int) string { return notifications.Cut(s, max) }

// EmitNotification queues a committed storage write's event. Called only
// after the write commits: notification trouble never changes the answer.
// An owner write stays silent unless it is to a visitor's row (so that
// visitor's own push devices hear of it).
func (h *DataHandler) EmitNotification(c StorageCall, name string, data map[string]any) {
	if _, ok := data["row_visitor_id"]; c.Owner && !ok {
		return
	}
	h.Notifier().Emit(func(ctx context.Context) { h.storeNotificationContext(ctx, c, name, data) })
}

func (h *DataHandler) StoreNotification(c StorageCall, name string, data map[string]any) {
	h.storeNotificationContext(context.Background(), c, name, data)
}

func (h *DataHandler) storeNotificationContext(parent context.Context, c StorageCall, name string, data map[string]any) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	e := h.Notifier()
	site, ok := e.EventSite(ctx, c.SiteID, name, "", 0)
	if !ok {
		return
	}
	siteName, siteURL, err := h.Hosts.NotificationSite(ctx, site)
	if err != nil {
		return
	}
	e.StoreRecordEvent(ctx, notifications.RecordEvent{SiteID: c.SiteID, Name: name, SiteName: siteName, SiteURL: siteURL, Resource: c.ResourceName, Kind: c.Resource.Kind, Writer: appstorage.StampID(c.Owner, c.VisitorID), Data: data})
}

// EmitSiteNotification queues a site event (SIM-356): a new version live,
// a custom domain's status, a refused named-viewers sign-in, a first
// visitor sign-in, after the caller has answered. data["visitor"], when
// set, is who it is about; dedupe > 0 drops a repeat for them within that
// window. Simple Host only: Simple Hack follows later (INTENT 2026-11-02).
func (h *DataHandler) EmitSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	h.Notifier().EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: siteID, Name: name, Data: data, Dedupe: dedupe})
}

func (h *DataHandler) StoreSiteNotification(siteID, name string, data map[string]any, dedupe time.Duration) {
	h.storeSiteNotificationContext(context.Background(), siteID, name, data, dedupe)
}

func (h *DataHandler) storeSiteNotificationContext(parent context.Context, siteID, name string, data map[string]any, dedupe time.Duration) {
	h.Notifier().StoreSiteEventNow(parent, h.Hosts, sites.SiteEvent{SiteID: siteID, Name: name, Data: data, Dedupe: dedupe})
}

// notificationOwner runs the engine's workers with the handler's current
// keys and mailer.
type notificationOwner struct{ h *DataHandler }

func (o notificationOwner) Start(ctx context.Context) { o.h.Notifier().Start(ctx, o.h.NotifyEnv) }
func (o notificationOwner) Stop()                     { o.h.Notifier().Stop() }
func (o notificationOwner) Wait()                     { o.h.Notifier().Wait() }
func (o notificationOwner) Status() []workers.Status  { return o.h.Notifier().Status() }

func (h *DataHandler) NotificationOwner() workers.Owner { return notificationOwner{h} }

func (h *DataHandler) StartNotifications(ctx context.Context) { h.NotificationOwner().Start(ctx) }

// WaitNotifications blocks until every notification goroutine has
// returned: the workers and sweeper (which stop when the context passed to
// StartNotifications is cancelled) and any emit still storing an event.
// Tests call it before closing the database; the server does not.
func (h *DataHandler) WaitNotifications() { h.Notifier().Wait() }

func (h *DataHandler) DeliverNotification(ctx context.Context) bool {
	return h.Notifier().Deliver(ctx, h.NotifyEnv())
}

func (h *DataHandler) SweepNotifications(ctx context.Context) {
	h.Notifier().Sweep(ctx, h.NotifyEnv().Mailer)
}

// SetNotifyClientForTest makes webhook delivery trust roots (a local
// httptest TLS server's pool) and loopback receivers, without a per-URL
// rate limit. Test hook for products' servertest.
func (h *DataHandler) SetNotifyClientForTest(roots *x509.CertPool) {
	h.Notifier().UseTestClient(roots, true)
}

// PendingNotificationsForTest is how many events are still being stored.
// Test hook.
func (h *DataHandler) PendingNotificationsForTest() int { return h.Notifier().PendingForTest() }

// DeliverNotificationForTest drains one delivery. Test hook.
func (h *DataHandler) DeliverNotificationForTest(ctx context.Context) bool {
	return h.DeliverNotification(ctx)
}

// NotificationSecretsForTest seals and opens subscription secrets with the
// install's key. Test hook.
func (h *DataHandler) NotificationSecretsForTest() model.NotificationSecrets {
	return h.NotifyEnv().Secrets()
}

// notificationBackend is MCP Events' backend: the engine with the
// handler's current keys.
type notificationBackend struct{ h *DataHandler }

func (h *DataHandler) EventsBackend() mcp.EventsBackend { return notificationBackend{h} }

func (b notificationBackend) EventsAvailability() error {
	return notifications.EventsAvailability(b.h.NotifyEnv())
}

func (b notificationBackend) Subscribe(ctx context.Context, c mcp.Caller, req mcp.EventSubscribeRequest) (mcp.EventSubscribeResult, error) {
	return b.h.Notifier().Subscribe(ctx, b.h.NotifyEnv(), eventSubscriber(c), req)
}

func (b notificationBackend) Unsubscribe(ctx context.Context, c mcp.Caller, req mcp.EventUnsubscribeRequest) error {
	return b.h.Notifier().Unsubscribe(ctx, eventSubscriber(c), req)
}

func eventSubscriber(c mcp.Caller) notifications.Subscriber {
	return notifications.Subscriber{Owner: c.Owner, GrantID: c.OAuthGrantID, AppName: c.AppName, DeployOnly: c.DeployOnly}
}
