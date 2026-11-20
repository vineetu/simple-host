package handler

import "net/http"

func (h *DataHandler) registerStorageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/resources", h.listStorageResources)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/usage", h.getStorageUsage)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/visitors", h.storageVisitors)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/people", h.storagePeople)
	mux.HandleFunc("PUT /v1/sites/{sitename}/storage/resources/{name}", h.putStorageResource)
	mux.HandleFunc("DELETE /v1/sites/{sitename}/storage/resources/{name}", h.deleteStorageResource)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/kv/{name}/keys", h.storageKV)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", h.storageKV)
	mux.HandleFunc("PUT /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", h.storageKV)
	mux.HandleFunc("DELETE /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", h.storageKV)
	mux.HandleFunc("POST /v1/sites/{sitename}/storage/sqlite/{name}/query", h.storageSQL)
	mux.HandleFunc("POST /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows", h.storageRows)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows", h.storageRows)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", h.storageRows)
	mux.HandleFunc("PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", h.storageRows)
	mux.HandleFunc("DELETE /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", h.storageRows)
	mux.HandleFunc("POST /v1/sites/{sitename}/storage/sqlite/{name}/execute", h.storageSQL)
	mux.HandleFunc("POST /v1/sites/{sitename}/storage/sqlite/{name}/schema", h.storageSQL)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/files/{name}/objects", h.storageFiles)
	mux.HandleFunc("GET /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", h.storageFiles)
	mux.HandleFunc("PUT /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", h.storageFiles)
	mux.HandleFunc("DELETE /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", h.storageFiles)
	mux.HandleFunc("POST /v1/sites/{sitename}/storage/files/{name}/download-link", h.createStorageFileLink)
	mux.HandleFunc("GET /v1/storage-download", h.downloadStorageFile)
}

func (h *DataHandler) registerNotifications(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.notifyEngine().rest
	mux.Handle("GET /v1/notifications", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications", authMiddleware(rateLimitByIP(limiter, h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.notificationRoute))))))
	mux.Handle("PATCH /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/test", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/rotate-secret", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications/{id}/deliveries", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
}

func (h *DataHandler) registerWebPush(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.notifyEngine().rest
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
	mux.Handle("GET /v1/sites/{sitename}/push/vapid-key", h.access.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushVAPIDKeyRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/subscribe", h.access.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushSubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/unsubscribe", h.access.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushUnsubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/mute", h.access.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushMuteRoute))))
	// Owner-only device/rule management: account auth, not the passcode.
	mux.Handle("GET /v1/sites/{sitename}/push/devices", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.pushDevicesRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/push/devices", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.pushDevicesRoute)))))
	// One device, including a visitor's own: the generic DELETE
	// /notifications/{id} requires n.OwnerID==the caller's own account
	// id, which a visitor-role push row never satisfies even for the
	// site's owner (FIX 12/adversary review, Codex round 2) -- this is
	// the owner-authorized path the dashboard's per-row Remove uses for
	// any push device on their site.
	mux.Handle("DELETE /v1/sites/{sitename}/push/devices/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.pushDeviceDeleteRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/push/rules", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.pushRulesRoute)))))
	mux.Handle("PUT /v1/sites/{sitename}/push/rules", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.pushRulesRoute)))))
}
