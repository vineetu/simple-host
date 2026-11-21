package datahttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
)

// RegisterStorageRoutes maps a site's saved-data routes. caller (Access's
// AccessHandler.StorageCaller) resolves the site and the caller in front of
// every one.
func (d *DataHandler) RegisterStorageRoutes(mux *http.ServeMux, caller func(http.Handler) http.Handler) {
	route := func(fn storageRoute) http.Handler { return d.StorageRouteFor(caller, fn) }
	mux.Handle("GET /v1/sites/{sitename}/storage/resources", route(d.ListStorageResources))
	mux.Handle("GET /v1/sites/{sitename}/storage/usage", route(d.getStorageUsage))
	mux.Handle("GET /v1/sites/{sitename}/storage/visitors", route(d.storageVisitors))
	mux.Handle("GET /v1/sites/{sitename}/storage/people", route(d.storagePeople))
	mux.Handle("PUT /v1/sites/{sitename}/storage/resources/{name}", route(d.putStorageResource))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/resources/{name}", route(d.deleteStorageResource))
	mux.Handle("GET /v1/sites/{sitename}/storage/kv/{name}/keys", route(d.StorageKV))
	mux.Handle("GET /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", route(d.StorageKV))
	mux.Handle("PUT /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", route(d.StorageKV))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", route(d.StorageKV))
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/query", route(d.storageSQL))
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows", route(d.StorageRows))
	mux.Handle("GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows", route(d.StorageRows))
	mux.Handle("GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", route(d.StorageRows))
	mux.Handle("PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", route(d.StorageRows))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", route(d.StorageRows))
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/execute", route(d.storageSQL))
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/schema", route(d.storageSQL))
	mux.Handle("GET /v1/sites/{sitename}/storage/files/{name}/objects", route(d.storageFiles))
	mux.Handle("GET /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", route(d.storageFiles))
	mux.Handle("PUT /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", route(d.storageFiles))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", route(d.storageFiles))
	mux.Handle("POST /v1/sites/{sitename}/storage/files/{name}/download-link", route(d.createStorageFileLink))
	mux.HandleFunc("GET /v1/storage-download", d.DownloadStorageFile)
	mux.HandleFunc("GET /v1/turnstile/frame", d.turnstileFrame)
}

func (h *DataHandler) RegisterNotifications(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.Notifier().RESTLimiter()
	mux.Handle("GET /v1/notifications", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.listNotificationsRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.listNotificationsRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications", authMiddleware(httpx.RateLimitByIP(limiter, h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.createNotificationRoute))))))
	mux.Handle("PATCH /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.pauseNotificationRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.deleteNotificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/test", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.testNotificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/rotate-secret", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.rotateNotificationRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications/{id}/deliveries", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.notificationDeliveriesRoute)))))
}

// RegisterWebPush maps the push routes. passcodeGate and caller are Access's
// AccessHandler.PasscodeGate and AccessHandler.StorageCaller.
func (h *DataHandler) RegisterWebPush(mux *http.ServeMux, passcodeGate, caller, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.Notifier().RESTLimiter()
	// Unauthenticated and ungated by WEB_PUSH_ACCOUNTS, like the file it
	// serves (see pushSWRoute): no site, host or /v1/sites/{sitename}
	// path segment at all, so there is nothing here for the flag to
	// gate in the first place, and registration/unregistration must
	// keep working regardless of the flag the same way unsubscribe/mute
	// already do.
	mux.HandleFunc("GET /v1/push/sw.js", h.pushSWRoute)
	mux.HandleFunc("HEAD /v1/push/sw.js", h.pushSWRoute)
	// The visitor-facing routes go through passcodeGate like every other
	// visitor read/write: a passcode-locked or named-viewer-only site
	// must refuse an uninvited visitor's push subscribe/unsubscribe/mute
	// and the VAPID key fetch exactly as it refuses any other visitor
	// route (TestPasscodeRouteTable enforces this for every
	// /v1/sites/{sitename}/... route). The owner's own calls (API key or
	// an owner cookie) pass the gate as the owner already does elsewhere.
	mux.Handle("GET /v1/sites/{sitename}/push/vapid-key", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, h.pushVAPIDKeyRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/subscribe", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, h.pushSubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/unsubscribe", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, h.pushUnsubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/mute", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, h.pushMuteRoute))))
	// Owner-only device/rule management: account auth, not the passcode.
	mux.Handle("GET /v1/sites/{sitename}/push/devices", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.pushDevicesRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/push/devices", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.pushDevicesDeleteRoute)))))
	// One device, including a visitor's own: the generic DELETE
	// /notifications/{id} requires n.OwnerID==the caller's own account
	// id, which a visitor-role push row never satisfies even for the
	// site's owner -- this is
	// the owner-authorized path the dashboard's per-row Remove uses for
	// any push device on their site.
	mux.Handle("DELETE /v1/sites/{sitename}/push/devices/{id}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.pushDeviceDeleteRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/push/rules", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.pushRulesRoute)))))
	mux.Handle("PUT /v1/sites/{sitename}/push/rules", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, http.HandlerFunc(h.pushRuleSaveRoute)))))
}
