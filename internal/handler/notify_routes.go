package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

func (h *SiteHandler) registerNotifications(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.notifyEngine().rest
	mux.Handle("GET /v1/notifications", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications", authMiddleware(rateLimitByIP(limiter, h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.notificationRoute))))))
	mux.Handle("PATCH /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/test", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/rotate-secret", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications/{id}/deliveries", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("GET /v1/hack/events/{slug}/website/notifications", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/hack/events/{slug}/website/notifications", authMiddleware(rateLimitByIP(limiter, h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.notificationRoute))))))
	mux.Handle("PATCH /v1/hack/events/{slug}/website/notifications/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("DELETE /v1/hack/events/{slug}/website/notifications/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/hack/events/{slug}/website/notifications/{id}/test", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("POST /v1/hack/events/{slug}/website/notifications/{id}/rotate-secret", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
	mux.Handle("GET /v1/hack/events/{slug}/website/notifications/{id}/deliveries", noticeMiddleware(authMiddleware(rateLimitByIP(limiter, http.HandlerFunc(h.notificationRoute)))))
}

func (h *SiteHandler) notifyLimits() map[string]any {
	l := config.Active()
	return map[string]any{"enabled": h.NotificationsReady(), "per_site": l.NotifyMaxPerSite, "per_minute": l.NotifyPerMinute, "per_day": l.NotifyPerDay, "history_days": l.NotifyHistoryDays}
}
func notificationView(n db.Notification) map[string]any {
	state := n.State
	if n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
		state = "expired"
	} else if n.Source == "mcp" && n.VerifiedAt == nil {
		state = "pending"
	}
	out := map[string]any{"id": n.ID, "site": n.Site, "url": n.URL, "events": n.Events, "storage": n.Storage, "label": n.Label, "source": n.Source, "app_name": n.AppName, "state": state, "created_at": n.CreatedAt, "expires_at": n.ExpiresAt, "last_attempt_at": n.LastAttemptAt, "last_success_at": n.LastSuccessAt, "last_result": n.LastResult, "failing_since": n.FailingSince, "auth_header_name": n.AuthHeaderName}
	if n.Event != "" {
		out["event"] = n.Event
		out["site"] = n.Event
	}
	return out
}
func notifyError(w http.ResponseWriter, status int, code string) {
	storageError(w, status, code, strings.ReplaceAll(code, "_", " "))
}

var notifyHeaderRE = regexp.MustCompile(`^[Xx]-[A-Za-z0-9-]{1,62}$`)

func validNotifyHeader(name, value string) bool {
	if name == "" {
		return value == ""
	}
	if (!strings.EqualFold(name, "Authorization") && !notifyHeaderRE.MatchString(name)) || len(value) > 1024 {
		return false
	}
	lower := strings.ToLower(name)
	for _, prefix := range []string{"x-forwarded-", "x-mcp-"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	switch lower {
	case "x-real-ip", "x-client-ip", "x-originating-ip":
		return false
	}
	for _, c := range value {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
func validNotifyEvents(events []string) bool {
	if len(events) < 1 || len(events) > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, e := range events {
		if e != "record.added" && e != "record.changed" && e != "file.uploaded" || seen[e] {
			return false
		}
		seen[e] = true
	}
	return true
}
func (h *SiteHandler) notificationRoute(w http.ResponseWriter, r *http.Request) {
	if !hackMode && r.PathValue("slug") != "" {
		notifyError(w, 404, "not_offered")
		return
	}
	user := auth.GetUser(r.Context())
	if user == nil || user.Suspended || user.Team != nil || user.EventWebsite != nil || user.KeyScope == db.KeyScopeDeploy {
		notifyError(w, 403, "forbidden")
		return
	}
	ctx := r.Context()
	eventAccount, err := db.EventAccountFlag(ctx, h.database, user.ID)
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	if eventAccount {
		notifyError(w, 404, "not_offered")
		return
	}
	var site db.Site
	if name := r.PathValue("sitename"); name != "" || r.PathValue("slug") != "" {
		if hackMode {
			site, err = db.HackNotificationSite(ctx, h.database, user.ID, name, r.PathValue("slug"), r.URL.Query().Get("event"))
		} else {
			site, err = db.GetSiteByUser(ctx, h.database, user.ID, name)
		}
		if err != nil {
			notifyError(w, 404, "not_found")
			return
		}
	}
	id := r.PathValue("id")
	if id == "" && r.Method == http.MethodGet {
		ns, err := db.ListNotifications(ctx, h.database, user.ID, site.ID)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		out := []map[string]any{}
		for _, n := range ns {
			out = append(out, notificationView(n))
		}
		limits := h.notifyLimits()
		if hackMode && site.ID != "" {
			canCreate, e := db.HackNotificationAllowed(ctx, h.database, site.ID, user.ID, true)
			if e != nil {
				notifyError(w, 500, "internal_error")
				return
			}
			limits["can_create"] = canCreate && h.NotificationsReady()
			canManage, e := db.HackNotificationAllowed(ctx, h.database, site.ID, user.ID, false)
			if e != nil {
				notifyError(w, 500, "internal_error")
				return
			}
			limits["can_manage"] = canManage
		}
		writeJSON(w, 200, map[string]any{"notifications": out, "limits": limits})
		return
	}
	if id == "" {
		h.createNotification(w, r, site)
		return
	}
	n, err := db.GetNotification(ctx, h.database, id)
	if err != nil || n.OwnerID != user.ID || n.SiteID != site.ID {
		notifyError(w, 404, "not_found")
		return
	}
	if hackMode && r.Method != http.MethodGet && r.Method != http.MethodDelete {
		allowed, e := db.HackNotificationAllowed(ctx, h.database, site.ID, user.ID, false)
		if e != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		if !allowed {
			notifyError(w, 403, "notification_access_ended")
			return
		}
	}
	switch {
	case r.Method == http.MethodDelete:
		if _, err = db.DeleteNotification(ctx, h.database, id); err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	case r.Method == http.MethodPatch:
		var req struct {
			Paused *bool `json:"paused"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Paused == nil {
			notifyError(w, 400, "invalid_request")
			return
		}
		if err = db.PauseNotification(ctx, h.database, id, *req.Paused); err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		n, err = db.GetNotification(ctx, h.database, id)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		h.notifyEngine().signal()
		writeJSON(w, 200, map[string]any{"notification": notificationView(n)})
	case strings.HasSuffix(r.URL.Path, "/rotate-secret"):
		if !h.requireNotifications(w) {
			return
		}
		if n.Source == "mcp" {
			notifyError(w, 409, "client_secret")
			return
		}
		secret, err := newNotifySecret()
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		until, err := db.RotateNotification(ctx, h.database, id, secret, h.notificationSecrets())
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		writeJSON(w, 200, map[string]any{"secret": secret, "previous_valid_until": until})
	case strings.HasSuffix(r.URL.Path, "/deliveries"):
		deliveries, err := db.NotificationHistory(ctx, h.database, id, config.Active().NotifyHistoryDays)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		writeJSON(w, 200, map[string]any{"deliveries": deliveries})
	case strings.HasSuffix(r.URL.Path, "/test"):
		if n.Source == "mcp" || n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
			notifyError(w, 409, "client_subscription")
			return
		}
		select {
		case h.notifyEngine().verifying <- struct{}{}:
			defer func() { <-h.notifyEngine().verifying }()
		default:
			notifyError(w, 503, "busy")
			return
		}
		ctx, cancel := context.WithTimeout(ctx, config.Active().NotifyTimeout)
		defer cancel()
		client, err := h.notifyChallengeClient(ctx, user.ID, n.URL)
		if errors.Is(err, notifyFailure("rate_limited")) {
			tooManyRequests(w)
			return
		}
		if err != nil {
			writeJSON(w, 200, notifyResult{Result: notifyCategory(err)})
			return
		}
		if site.Offline || site.Suspended() {
			writeJSON(w, 200, notifyResult{Result: "site_unavailable"})
			return
		}
		name, siteURL, err := h.notificationSiteInfo(ctx, site)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		event := db.NotificationDelivery{EventID: notifyRandom("evt_test_", 16), Name: "notification.test", CreatedAt: time.Now(), Data: storageJSON(map[string]any{"v": 1, "site": name, "site_url": siteURL, "test": true})}
		cursor, err := db.NotificationCursor(ctx, h.database, n.ID, n.SiteID)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		writeJSON(w, 200, client.send(ctx, n, event.EventID, notifyBody(event, cursor)))
	}
}
func (h *SiteHandler) requireNotifications(w http.ResponseWriter) bool {
	if h.NotificationsReady() {
		return true
	}
	storageError(w, http.StatusConflict, "notifications_not_enabled", "The server has no PASSCODE_ENC_KEY; new notifications are unavailable.")
	return false
}
func (h *SiteHandler) createNotification(w http.ResponseWriter, r *http.Request, site db.Site) {
	if !h.requireNotifications(w) {
		return
	}
	if hackMode {
		allowed, err := db.HackNotificationAllowed(r.Context(), h.database, site.ID, auth.GetUser(r.Context()).ID, true)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		if !allowed {
			notifyError(w, 403, "notification_access_ended")
			return
		}
	}
	var req struct {
		URL        string   `json:"url"`
		Events     []string `json:"events"`
		Storage    string   `json:"storage"`
		Secret     string   `json:"secret"`
		Label      string   `json:"label"`
		AuthHeader struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"auth_header"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil {
		notifyError(w, 400, "invalid_request")
		return
	}
	if !validNotifyEvents(req.Events) {
		notifyError(w, 400, "invalid_events")
		return
	}
	if !validNotifyHeader(req.AuthHeader.Name, req.AuthHeader.Value) {
		storageError(w, 400, "invalid_header", "Use Authorization or X- followed by 1 to 62 letters, digits, or hyphens; X-Forwarded-*, X-Real-IP, X-MCP-*, X-Client-IP, and X-Originating-IP are reserved. Values must be printable ASCII, at most 1024 bytes.")
		return
	}
	if req.Storage != "" {
		if _, err := h.loadStorageResource(r.Context(), site.ID, req.Storage); err != nil {
			notifyError(w, 404, "storage_not_found")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), config.Active().NotifyTimeout)
	defer cancel()
	if _, err := h.notifyEngine().client.validate(ctx, req.URL); err != nil {
		code := "invalid_url"
		if errors.Is(err, notifyFailure("refused_address")) {
			code = "refused_address"
		}
		notifyError(w, 400, code)
		return
	}
	if req.Secret == "" {
		var err error
		req.Secret, err = newNotifySecret()
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
	} else if _, err := notifySecretKey(req.Secret); err != nil {
		notifyError(w, 400, "invalid_secret")
		return
	}
	now := time.Now()
	n := db.Notification{ID: notifyRandom("sub_", 12), SiteID: site.ID, OwnerID: auth.GetUser(r.Context()).ID, Hack: hackMode, Source: "owner", URL: req.URL, Events: req.Events, Storage: req.Storage, Secret: req.Secret, Label: req.Label, AuthHeaderName: req.AuthHeader.Name, AuthHeaderValue: req.AuthHeader.Value, VerifiedAt: &now}
	_, _, err := db.SaveNotification(ctx, h.database, n, config.Active().NotifyMaxPerSite, nil, config.Active().NotifyHistoryDays, h.notificationSecrets())
	if errors.Is(err, db.ErrNotificationAccess) {
		notifyError(w, 403, "notification_access_ended")
		return
	}
	if errors.Is(err, db.ErrNotificationCap) {
		notifyError(w, 409, "too_many_notifications")
		return
	}
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	n, err = db.GetNotification(ctx, h.database, n.ID)
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	writeJSON(w, 201, map[string]any{"notification": notificationView(n), "secret": req.Secret})
}
