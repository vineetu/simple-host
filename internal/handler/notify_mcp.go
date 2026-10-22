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

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

type notificationBackend struct{ h *SiteHandler }

func (h *SiteHandler) EventsBackend() mcp.EventsBackend {
	if hackMode {
		return nil
	}
	return notificationBackend{h}
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
func (b notificationBackend) site(ctx context.Context, c mcp.Caller, name, storage string) (db.Site, error) {
	if hackMode || c.DeployOnly || c.Owner == "" {
		return db.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	user, err := db.GetUserByID(ctx, b.h.database, c.Owner)
	if err != nil || user.Suspended {
		return db.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	event, err := db.EventAccountFlag(ctx, b.h.database, c.Owner)
	if err != nil {
		return db.Site{}, err
	}
	if event {
		return db.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	site, err := db.GetSiteByUser(ctx, b.h.database, c.Owner, name)
	if err != nil {
		return db.Site{}, notifyMCPError(mcp.EventCodeForbidden, "kind", "site")
	}
	if storage != "" {
		if _, err = b.h.loadStorageResource(ctx, site.ID, storage); err != nil {
			return db.Site{}, notifyMCPError(mcp.EventCodeNotFound, "kind", "storage")
		}
	}
	return site, nil
}
func (b notificationBackend) Subscribe(ctx context.Context, c mcp.Caller, req mcp.EventSubscribeRequest) (mcp.EventSubscribeResult, error) {
	out := mcp.EventSubscribeResult{}
	site, err := b.site(ctx, c, req.Site, req.Storage)
	if err != nil {
		return out, err
	}
	if !validNotifyEvents([]string{req.Name}) {
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
	ctx, cancel := context.WithTimeout(ctx, config.Active().NotifyTimeout)
	defer cancel()
	if _, err = b.h.notifyEngine().client.validate(ctx, req.URL); err != nil {
		code := mcp.EventCodeCallbackEndpoint
		if errors.Is(err, notifyFailure("invalid_url")) {
			code = -32602
		}
		return out, notifyMCPError(code, "reason", notifyCategory(err))
	}
	now := time.Now()
	n := db.Notification{ID: notifyMCPID(c.Owner, c.OAuthGrantID, req.URL, req.Name, req.CanonicalArgs), SiteID: site.ID, OwnerID: c.Owner, Source: "mcp", URL: req.URL, Events: []string{req.Name}, Storage: req.Storage, ArgsCanonical: req.CanonicalArgs, Secret: req.Secret, AppName: c.AppName, VerifiedAt: &now}
	if c.OAuthGrantID != "" {
		n.GrantID = &c.OAuthGrantID
	}
	ttl := config.Active().NotifyDefaultTTL
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
	if c.OAuthGrantID == "" && (ttl == 0 || ttl > config.Active().NotifyDefaultTTL) {
		ttl = config.Active().NotifyDefaultTTL
	}
	if ttl != 0 {
		expiry := now.Add(ttl)
		n.ExpiresAt = &expiry
	}
	old, e := db.GetNotification(ctx, b.h.database, n.ID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	siteChanged := e == nil && old.SiteID != n.SiteID
	if siteChanged {
		if _, err = db.DeleteNotification(ctx, b.h.database, n.ID); err != nil {
			return out, err
		}
		old = db.Notification{}
	}
	verified, err := db.RecentlyVerifiedNotification(ctx, b.h.database, c.Owner, req.URL)
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
		var verifiedAt time.Time
		if err = b.h.database.QueryRowContext(ctx, `SELECT max(verified_at) FROM site_notifications WHERE owner_id=$1 AND url=$2 AND source='mcp'`, c.Owner, req.URL).Scan(&verifiedAt); err != nil {
			return out, err
		}
		n.VerifiedAt = &verifiedAt
	}
	refreshed, truncated, err := db.SaveNotification(ctx, b.h.database, n, config.Active().NotifyMaxPerSite, cursor, config.Active().NotifyHistoryDays)
	if errors.Is(err, db.ErrNotificationCap) {
		return out, &mcp.EventError{Code: mcp.EventCodeResourceExhausted, Message: "too many subscriptions", Data: map[string]any{"limit": "subscriptions", "max": config.Active().NotifyMaxPerSite}}
	}
	if err != nil {
		return out, err
	}
	seq, err := db.NotificationCursor(ctx, b.h.database, n.ID, n.SiteID)
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
	_, err := db.DeleteNotification(ctx, b.h.database, notifyMCPID(c.Owner, c.OAuthGrantID, req.URL, req.Name, req.CanonicalArgs))
	return err
}
