package push

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/webassets"
)

// Web push: the routes read the request, call the engine, and
// answer; the rules live in host/data/notify.

// pushSWModTime is used only for the service worker's conditional-GET
// headers (ServeContent); the process start time is a safe, stable stand-in
// for the embedded file's own mtime, which a Go embed does not retain.
var pushSWModTime = time.Now()

// pushSWBytes is the service worker, read once from the embedded tree the
// platform's static pipeline serves it from (static/push-sw.js).
var pushSWBytes = func() []byte {
	b, err := webassets.Static.ReadFile("static/push-sw.js")
	if err != nil {
		log.Printf("web push: embedded push-sw.js missing: %v", err)
		return nil
	}
	return b
}()

// pushSWRoute answers GET/HEAD /v1/push/sw.js. It lives under /v1/, which
// nginx proxies to Go on every vhost and no site's own files use, so it
// needs no vhost location of its own and never shadows a site's file.
// auth.js registers it with {scope: '/v1/push/'}: push and notification
// clicks work from any scope, so it never takes a site's root scope.
func pushSWRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	if len(pushSWBytes) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "sw.js", pushSWModTime, bytes.NewReader(pushSWBytes))
}

// pushCaller is the push engine's view of a visitor-facing call.
func pushCaller(c data.Call) notify.PushCaller {
	return notify.PushCaller{SiteID: c.SiteID, SiteName: c.SiteName, AccountID: c.OwnerID, Owner: c.Owner, VisitorID: c.VisitorID, VisitorFamily: c.VisitorFamily, OnOwnAddress: c.OnOwnAddress}
}

// pushCookieRequestOK is the CSRF and same-origin check of every
// visitor-cookie write: the literal X-SH-CSRF header (a plain JSON POST
// must not satisfy it), then the origin. An owner API key carries no
// ambient browser credential and skips it.
func pushCookieRequestOK(c data.Call, r *http.Request, w http.ResponseWriter) bool {
	switch {
	case c.Owner:
		return true
	case r.Header.Get("X-SH-CSRF") != "1":
		data.NotifyError(w, 403, "csrf_required")
	case !httpx.SameOrigin(r):
		data.NotifyError(w, 403, "forbidden")
	default:
		return true
	}
	return false
}

// GET /v1/sites/{sitename}/push/vapid-key: the key every subscribe() needs.
// 404 while web push is off, so a page can tell it is unavailable.
func pushVAPIDKeyRoute(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	pub, err := h.Notifier().VAPIDPublicKey(r.Context(), h.NotifyEnv(), c.SiteID)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"public_key": pub})
}

// POST /v1/sites/{sitename}/push/subscribe registers one device for the
// owner or a signed-in visitor.
func pushSubscribeRoute(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	if !pushCookieRequestOK(c, r, w) {
		return
	}
	env := h.NotifyEnv()
	if err := h.Notifier().PushSubscribable(r.Context(), env, c.SiteID, c.OwnerID); err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Resource string   `json:"resource"`
		Events   []string `json:"events"`
		Label    string   `json:"label"`
	}
	if !httpx.DecodeJSON(w, r, 8192, &req) {
		return
	}
	id, pub, err := h.Notifier().SubscribePush(r.Context(), env, pushCaller(c), notify.PushSubscription{Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth, Resource: req.Resource, Label: req.Label, Events: req.Events})
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"id": id, "public_key": pub})
}

// POST /v1/sites/{sitename}/push/unsubscribe ends push for one device.
// Unsubscribe and mute stay reachable when web push is not (or no longer)
// offered: revoking consent must always be possible.
func pushUnsubscribeRoute(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	if !pushCookieRequestOK(c, r, w) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req) != nil || req.ID == "" {
		data.NotifyError(w, 400, "invalid_request")
		return
	}
	deleted, err := h.Notifier().UnsubscribePush(r.Context(), pushCaller(c), req.ID)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": deleted})
}

// POST /v1/sites/{sitename}/push/mute replaces one device's mute list.
func pushMuteRoute(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	if !pushCookieRequestOK(c, r, w) {
		return
	}
	var req struct {
		ID        string   `json:"id"`
		Resources []string `json:"resources"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.ID == "" {
		data.NotifyError(w, 400, "invalid_request")
		return
	}
	if err := h.Notifier().MutePush(r.Context(), pushCaller(c), req.ID, req.Resources); err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

// pushOwner is the signed-in account and the site name of an owner push
// route (authMiddleware has signed them in).
func pushOwner(r *http.Request) (account, site string) {
	return auth.GetUser(r.Context()).ID, r.PathValue("sitename")
}

// GET /v1/sites/{sitename}/push/devices: every device on the owner's site.
// Owner-only: one visitor's device is never shown to another.
func pushDevicesRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	account, site := pushOwner(r)
	devices, err := h.Notifier().PushDevices(r.Context(), account, site)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"devices": devices})
}

// DELETE /v1/sites/{sitename}/push/devices turns push off for the site.
func pushDevicesDeleteRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	account, site := pushOwner(r)
	n, err := h.Notifier().DeleteSitePush(r.Context(), account, site)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": n})
}

// DELETE /v1/sites/{sitename}/push/devices/{id} removes one device, the
// owner's or a visitor's.
func pushDeviceDeleteRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	account, site := pushOwner(r)
	if err := h.Notifier().DeletePushDevice(r.Context(), account, site, r.PathValue("id")); err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
}

// GET /v1/sites/{sitename}/push/rules: the owner's per-resource rules.
func pushRulesRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	account, site := pushOwner(r)
	rules, err := h.Notifier().PushRules(r.Context(), account, site)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"rules": rules})
}

// PUT /v1/sites/{sitename}/push/rules saves one resource's rule.
func pushRuleSaveRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	account, name := pushOwner(r)
	site, err := h.Notifier().OwnerPushSite(r.Context(), account, name)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	var rule model.PushRule
	if !httpx.DecodeJSON(w, r, 4096, &rule) {
		return
	}
	exists := sqliteColumnMayExist(h, r.Context(), site, strings.TrimSpace(rule.Resource), strings.TrimSpace(rule.BodyColumn))
	if err := h.Notifier().SavePushRule(r.Context(), site, rule, exists); err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

// sqliteColumnMayExist reports whether column is a column of some table of
// the site's SQLite resource, or cannot be known to be absent: no column,
// a resource that is missing or not SQLite (no fixed schema), or one busy
// or briefly unreadable (the save stays best-effort).
func sqliteColumnMayExist(h *data.Handler, ctx context.Context, site model.Site, resourceName, column string) bool {
	if column == "" || resourceName == "" {
		return true
	}
	resource, err := h.LoadStorageResource(ctx, site.ID, resourceName)
	if err != nil || resource.Kind != "sqlite" {
		return true
	}
	call := data.Call{SiteID: site.ID, OwnerID: site.UserID, SiteName: site.Name, ResourceName: resource.Name, Resource: resource, Owner: true}
	call.RuntimeDir = h.StorageRuntimeDir(call)
	found := false
	err = h.StorageFor(call).Inspect(ctx, call, false, func(insp *data.Inspection) error {
		tables, err := insp.Tables()
		for table := range tables {
			if cols, _, err := insp.TableInfo(table); err == nil {
				_, ok := cols[column]
				found = found || ok
			}
		}
		return err
	})
	return err != nil || found
}

// DeletePushForEmail removes the push devices of a named viewer removed
// from siteID (the cleanup operations.RemoveSiteViewer does); its caller logs the error.
func DeletePushForEmail(h *data.Handler, ctx context.Context, siteID, email string) error {
	return h.Notifier().DeleteViewerPush(ctx, siteID, email)
}

// Register maps the push routes. passcodeGate and caller are Access's
// AccessHandler.PasscodeGate and AccessHandler.StorageCaller.
func Register(mux *http.ServeMux, h *data.Handler, passcodeGate, caller, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.Notifier().RESTLimiter()
	// Unauthenticated and ungated by WEB_PUSH_ACCOUNTS, like the file it
	// serves (see pushSWRoute): no site, host or /v1/sites/{sitename}
	// path segment at all, so there is nothing here for the flag to
	// gate in the first place, and registration/unregistration must
	// keep working regardless of the flag the same way unsubscribe/mute
	// already do.
	mux.Handle("GET /v1/push/sw.js", bind(h, pushSWRoute))
	mux.Handle("HEAD /v1/push/sw.js", bind(h, pushSWRoute))
	// The visitor-facing routes go through passcodeGate like every other
	// visitor read/write: a passcode-locked or named-viewer-only site
	// must refuse an uninvited visitor's push subscribe/unsubscribe/mute
	// and the VAPID key fetch exactly as it refuses any other visitor
	// route (TestPasscodeRouteTable enforces this for every
	// /v1/sites/{sitename}/... route). The owner's own calls (API key or
	// an owner cookie) pass the gate as the owner already does elsewhere.
	mux.Handle("GET /v1/sites/{sitename}/push/vapid-key", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, pushVAPIDKeyRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/subscribe", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, pushSubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/unsubscribe", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, pushUnsubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/mute", passcodeGate(httpx.RateLimitByIP(limiter, h.StorageRouteFor(caller, pushMuteRoute))))
	// Owner-only device/rule management: account auth, not the passcode.
	mux.Handle("GET /v1/sites/{sitename}/push/devices", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, pushDevicesRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/push/devices", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, pushDevicesDeleteRoute)))))
	// One device, including a visitor's own: the generic DELETE
	// /notifications/{id} requires n.OwnerID==the caller's own account
	// id, which a visitor-role push row never satisfies even for the
	// site's owner -- this is
	// the owner-authorized path the dashboard's per-row Remove uses for
	// any push device on their site.
	mux.Handle("DELETE /v1/sites/{sitename}/push/devices/{id}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, pushDeviceDeleteRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/push/rules", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, pushRulesRoute)))))
	mux.Handle("PUT /v1/sites/{sitename}/push/rules", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, pushRuleSaveRoute)))))
}

// bind is route as a handler of h's requests.
func bind(h *data.Handler, route func(*data.Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { route(h, w, r) }
}
