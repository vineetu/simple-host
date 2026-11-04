package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/model"
)

type notificationBackend struct{ h *SiteHandler }

func (h *SiteHandler) EventsBackend() mcp.EventsBackend {
	return notificationBackend{h}
}

// EventsAvailability hides discovery while retaining a clear subscribe error.
func (b notificationBackend) EventsAvailability() error {
	if b.h.NotificationsReady() {
		return nil
	}
	return &mcp.EventError{Code: mcp.EventCodeForbidden, Message: "The server has no PASSCODE_ENC_KEY; new notifications are unavailable.", Data: map[string]any{"reason": "notifications_not_enabled"}}
}
func notifyMCPError(code int, kind, value string) error {
	return &mcp.EventError{Code: code, Message: strings.ReplaceAll(value, "_", " "), Data: map[string]any{kind: value}}
}
func notifyMCPID(owner, grant, url, name, args string) string {
	// Length-delimited JSON avoids ambiguous keys when URLs contain separators.
	raw, _ := json.Marshal([]string{owner, grant, url, name, args})
	sum := sha256.Sum256(raw)
	return "sub_" + hex.EncodeToString(sum[:12])
}
func (h *SiteHandler) notifyChallengeClient(ctx context.Context, owner, raw string) (notifyClient, error) {
	e := h.notifyEngine()
	c := e.client
	u, err := url.Parse(raw)
	if err != nil {
		return c, notifyFailure("invalid_url")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if !e.challenge.allow("owner:"+owner) || !e.challenge.allow("host:"+host) {
		return c, notifyFailure("rate_limited")
	}
	addrs, err := c.addresses(ctx, host)
	if err != nil {
		return c, err
	}
	for _, addr := range addrs {
		if !e.challenge.allow("ip:" + addr.IP.String()) {
			return c, notifyFailure("rate_limited")
		}
	}
	// Dial exactly the addresses whose shared destination budgets we checked.
	c.resolver = func(context.Context, string) ([]net.IPAddr, error) { return addrs, nil }
	return c, nil
}
func (b notificationBackend) site(ctx context.Context, c mcp.Caller, name, storage, eventSlug string) (model.Site, error) {
	if c.DeployOnly || c.Owner == "" {
		return model.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	user, err := b.h.opsAccountsSvc().GetUserByID(ctx, c.Owner)
	if err != nil || user.Suspended {
		return model.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	event, err := b.h.notificationsSvc().EventAccountFlag(ctx, c.Owner)
	if err != nil {
		return model.Site{}, err
	}
	if event {
		return model.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	var site model.Site
	if b.h.ed.Notifications().EventScoped {
		if c.Mode != mcp.CallerModeEvents {
			return site, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
		}
		event := eventSlug
		if event == "" {
			grant, e := b.h.opsAccountsSvc().GetOAuthGrantByID(ctx, c.OAuthGrantID)
			if e != nil || grant.UserID != c.Owner || !grant.SelectedTeamID.Valid {
				return site, notifyMCPError(mcp.EventCodeForbidden, "reason", "select_team")
			}
			team, e := b.h.opsAccountsSvc().ResolveTeamIdentity(ctx, c.Owner, grant.SelectedTeamID.String)
			if e != nil || team.TeamSlug != name {
				return site, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
			}
			site, err = b.h.sitesSvc().GetSiteByUser(ctx, team.AccountID, name)
		} else {
			site, err = b.h.notificationsSvc().HackNotificationSite(ctx, c.Owner, name, event)
		}
	} else {
		if eventSlug != "" {
			return site, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
		}
		site, err = b.h.sitesSvc().GetSiteByUser(ctx, c.Owner, name)
	}
	if err != nil {
		return model.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	if storage != "" {
		if _, err = b.h.loadStorageResource(ctx, site.ID, storage); err != nil {
			return model.Site{}, notifyMCPError(mcp.EventCodeNotFound, "kind", "storage")
		}
	}
	return site, nil
}
func (b notificationBackend) Subscribe(ctx context.Context, c mcp.Caller, req mcp.EventSubscribeRequest) (mcp.EventSubscribeResult, error) {
	out := mcp.EventSubscribeResult{}
	if err := b.EventsAvailability(); err != nil {
		return out, err
	}
	site, err := b.site(ctx, c, req.Site, req.Storage, req.Event)
	if err != nil {
		return out, err
	}
	if b.h.ed.Notifications().EventScoped {
		old, e := b.h.notificationsSvc().GetNotification(ctx, notifyMCPID(c.Owner, c.OAuthGrantID, req.URL, req.Name, req.CanonicalArgs))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		allowed, e := b.h.notificationsSvc().HackNotificationAllowed(ctx, site.ID, c.Owner, old.SiteID != site.ID)
		if e != nil {
			return out, e
		}
		if !allowed {
			return out, notifyMCPError(mcp.EventCodeForbidden, "reason", "notification_access_ended")
		}
	}
	if !b.h.validNotifyEvents([]string{req.Name}) {
		return out, notifyMCPError(mcp.EventCodeNotFound, "kind", "event")
	}
	if _, err = notifySecretKey(req.Secret); err != nil {
		return out, notifyMCPError(-32602, "reason", "invalid_secret")
	}
	var cursor *int64
	if req.Cursor != nil {
		raw, ok := strings.CutPrefix(*req.Cursor, "c_")
		seq, e := strconv.ParseInt(raw, 10, 64)
		if !ok || e != nil || seq < 0 {
			return out, notifyMCPError(-32602, "reason", "invalid_cursor")
		}
		cursor = &seq
	}
	ctx, cancel := context.WithTimeout(ctx, b.h.limits.NotifyTimeout)
	defer cancel()
	if _, err = b.h.notifyEngine().client.validate(ctx, req.URL); err != nil {
		code := mcp.EventCodeCallbackEndpoint
		if errors.Is(err, notifyFailure("invalid_url")) {
			code = -32602
		}
		return out, notifyMCPError(code, "reason", notifyCategory(err))
	}
	now := time.Now()
	n := model.Notification{ID: notifyMCPID(c.Owner, c.OAuthGrantID, req.URL, req.Name, req.CanonicalArgs), SiteID: site.ID, OwnerID: c.Owner, Hack: b.h.ed.Notifications().EventScoped, Source: "mcp", URL: req.URL, Events: []string{req.Name}, Storage: req.Storage, ArgsCanonical: req.CanonicalArgs, Secret: req.Secret, AppName: c.AppName, VerifiedAt: &now}
	if c.OAuthGrantID != "" {
		n.GrantID = &c.OAuthGrantID
	}
	ttl := b.h.limits.NotifyDefaultTTL
	if req.TTLSet {
		if req.TTL == nil {
			ttl = 0
		} else {
			ttl = *req.TTL
			if ttl < 5*time.Minute {
				ttl = 5 * time.Minute
			}
		}
	}
	if c.OAuthGrantID == "" && (ttl == 0 || ttl > b.h.limits.NotifyDefaultTTL) {
		ttl = b.h.limits.NotifyDefaultTTL
	}
	if ttl != 0 {
		expiry := now.Add(ttl)
		n.ExpiresAt = &expiry
	}
	old, e := b.h.notificationsSvc().GetNotification(ctx, n.ID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	siteChanged := e == nil && old.SiteID != n.SiteID
	if siteChanged {
		if _, err = b.h.notificationsSvc().DeleteNotification(ctx, n.ID); err != nil {
			return out, err
		}
		old = model.Notification{}
	}
	verified, err := b.h.notificationsSvc().RecentlyVerifiedNotification(ctx, c.Owner, req.URL)
	if err != nil {
		return out, err
	}
	if !verified || siteChanged {
		select {
		case b.h.notifyEngine().verifying <- struct{}{}:
			defer func() { <-b.h.notifyEngine().verifying }()
		default:
			return out, notifyMCPError(mcp.EventCodeCallbackEndpoint, "reason", "timeout")
		}
		client, err := b.h.notifyChallengeClient(ctx, c.Owner, req.URL)
		if errors.Is(err, notifyFailure("rate_limited")) {
			return out, &mcp.EventError{Code: mcp.EventCodeResourceExhausted, Message: "verification rate limit reached", Data: map[string]any{"limit": "challenges", "max": 10}}
		}
		if err != nil {
			return out, notifyMCPError(mcp.EventCodeCallbackEndpoint, "reason", notifyCategory(err))
		}
		random := make([]byte, 24)
		if _, err = rand.Read(random); err != nil {
			return out, err
		}
		challenge := base64.RawURLEncoding.EncodeToString(random)
		body, _ := json.Marshal(map[string]string{"type": "verification", "challenge": challenge})
		result := client.send(ctx, n, notifyRandom("msg_verification_", 16), body)
		if !result.Delivered {
			return out, notifyMCPError(mcp.EventCodeCallbackEndpoint, "reason", result.Result)
		}
		var echo struct {
			Challenge string          `json:"challenge"`
			Type      json.RawMessage `json:"type"`
		}
		if json.Unmarshal(result.body, &echo) != nil || echo.Type != nil || subtle.ConstantTimeCompare([]byte(echo.Challenge), []byte(challenge)) != 1 {
			return out, notifyMCPError(mcp.EventCodeCallbackEndpoint, "reason", "challenge_failed")
		}
	} else {
		// A refresh does not extend the proof's 30-day lifetime without a challenge.
		verifiedAt, verifyErr := b.h.notificationsSvc().VerifiedAt(ctx, c.Owner, req.URL)
		if err = verifyErr; err != nil {
			return out, err
		}
		n.VerifiedAt = &verifiedAt
	}
	if b.h.ed.Notifications().EventScoped {
		current, e := b.site(ctx, c, req.Site, req.Storage, req.Event)
		if e != nil {
			return out, e
		}
		if current.ID != site.ID {
			return out, notifyMCPError(mcp.EventCodeForbidden, "reason", "site_changed")
		}
	}
	refreshed, truncated, err := b.h.notificationsSvc().SaveNotification(ctx, n, b.h.limits.NotifyMaxPerSite, cursor, b.h.limits.NotifyHistoryDays, b.h.notificationSecrets())
	if errors.Is(err, model.ErrNotificationAccess) {
		return out, notifyMCPError(mcp.EventCodeForbidden, "reason", "notification_access_ended")
	}
	if errors.Is(err, model.ErrNotificationCap) {
		return out, &mcp.EventError{Code: mcp.EventCodeResourceExhausted, Message: "too many subscriptions", Data: map[string]any{"limit": "subscriptions", "max": b.h.limits.NotifyMaxPerSite}}
	}
	if err != nil {
		return out, err
	}
	seq, err := b.h.notificationsSvc().NotificationCursor(ctx, n.ID, n.SiteID)
	if err != nil {
		return out, err
	}
	watermark := fmt.Sprintf("c_%d", seq)
	b.h.notifyEngine().signal()
	if old.LastResult == "ok" {
		old.LastResult = ""
	}
	return mcp.EventSubscribeResult{ID: n.ID, RefreshBefore: n.ExpiresAt, Cursor: &watermark, Truncated: truncated, Refreshed: refreshed, Active: true, LastError: old.LastResult}, nil
}

// Unsubscribe is idempotent, as ChatGPT requires: the id is derived from the
// caller's account and grant, so it can only name that app's subscription,
// and one already gone (or on a deleted site) is answered as removed.
func (b notificationBackend) Unsubscribe(ctx context.Context, c mcp.Caller, req mcp.EventUnsubscribeRequest) error {
	if c.Owner == "" || c.DeployOnly {
		return notifyMCPError(mcp.EventCodeForbidden, "reason", "owner_required")
	}
	_, err := b.h.notificationsSvc().DeleteNotification(ctx, notifyMCPID(c.Owner, c.OAuthGrantID, req.URL, req.Name, req.CanonicalArgs))
	return err
}

func (b notificationBackend) NotificationSiteEvent(ctx context.Context, c mcp.Caller, name, event string) (string, error) {
	site, err := b.site(ctx, c, name, "", event)
	if err != nil {
		return "", err
	}
	return b.h.notificationsSvc().SiteEventSlug(ctx, site.UserID)
}
