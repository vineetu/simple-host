package notify

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
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// MCP Events (the connector's events/subscribe): an app subscribes a
// callback URL to one event on one of the caller's sites, proving it owns
// the URL by echoing a challenge at least every 30 days.

func eventError(code int, kind, value string) error {
	return &EventError{Code: code, Message: strings.ReplaceAll(value, "_", " "), Data: map[string]any{kind: value}}
}

var errSiteForbidden = eventError(EventCodeForbidden, "kind", "site")

// EventsAvailability hides discovery while new notifications are
// unavailable, keeping a clear subscribe error.
func EventsAvailability(env Env) error {
	if env.Ready() {
		return nil
	}
	return &EventError{Code: EventCodeForbidden, Message: errNotEnabled.Message, Data: map[string]any{"reason": errNotEnabled.Code}}
}

// mcpID is the subscription id of one app's subscription: the same caller,
// grant, URL, event and arguments always name the same row.
func mcpID(owner, grant, url, name, args string) string {
	// Length-delimited JSON avoids ambiguous keys when URLs contain separators.
	raw, _ := json.Marshal([]string{owner, grant, url, name, args})
	sum := sha256.Sum256(raw)
	return "sub_" + hex.EncodeToString(sum[:12])
}

// mcpSite is the caller's site the request names: on their own account, or
// with an "event" owner on an account they act for (a team they belong to,
// or a site of it they edit).
func (e *Engine) mcpSite(ctx context.Context, c Subscriber, req EventSubscribeRequest) (model.Site, error) {
	if err := e.refuse(ctx, c.Owner, c.DeployOnly); err != nil {
		var refused *Error
		if errors.As(err, &refused) {
			return model.Site{}, errSiteForbidden
		}
		return model.Site{}, err
	}
	accountID := c.Owner
	if req.Owner != "" {
		acct, err := access.New(e.svc.database).AccountByHandle(ctx, req.Owner)
		if err != nil {
			return model.Site{}, errSiteForbidden
		}
		if ok, err := access.ActsAsOwner(ctx, e.svc.database, c.Owner, acct.ID, req.Site); err != nil || !ok {
			return model.Site{}, errSiteForbidden
		}
		accountID = acct.ID
	}
	site, err := sitesrepo.GetSiteByUser(ctx, e.svc.database, accountID, req.Site)
	if err != nil {
		return model.Site{}, errSiteForbidden
	}
	if !e.resourceExists(ctx, site.ID, req.Storage) {
		return model.Site{}, eventError(EventCodeNotFound, "kind", "storage")
	}
	return site, nil
}

// checkSubscription validates the event, secret and replay cursor.
func (e *Engine) checkSubscription(req EventSubscribeRequest) (*int64, error) {
	if !e.validEvents([]string{req.Name}) {
		return nil, eventError(EventCodeNotFound, "kind", "event")
	}
	if _, err := SecretKey(req.Secret); err != nil {
		return nil, eventError(-32602, "reason", "invalid_secret")
	}
	if req.Cursor == nil {
		return nil, nil
	}
	raw, ok := strings.CutPrefix(*req.Cursor, "c_")
	seq, err := strconv.ParseInt(raw, 10, 64)
	if !ok || err != nil || seq < 0 {
		return nil, eventError(-32602, "reason", "invalid_cursor")
	}
	return &seq, nil
}

// mcpTTL is the subscription's lifetime (0: none): the requested one, at
// least five minutes, but never past the default without an OAuth grant.
func (e *Engine) mcpTTL(c Subscriber, req EventSubscribeRequest) time.Duration {
	ttl := e.limits.NotifyDefaultTTL
	if req.TTLSet {
		ttl = 0
		if req.TTL != nil {
			ttl = max(*req.TTL, 5*time.Minute)
		}
	}
	if c.GrantID == "" && (ttl == 0 || ttl > e.limits.NotifyDefaultTTL) {
		ttl = e.limits.NotifyDefaultTTL
	}
	return ttl
}

// mcpNotification is the subscription the request asks for.
func (e *Engine) mcpNotification(c Subscriber, req EventSubscribeRequest, site model.Site) model.Notification {
	now := time.Now()
	n := model.Notification{ID: mcpID(c.Owner, c.GrantID, req.URL, req.Name, req.CanonicalArgs), SiteID: site.ID, OwnerID: c.Owner, Product: site.UserID != c.Owner, Source: "mcp", URL: req.URL, Events: []string{req.Name}, Storage: req.Storage, ArgsCanonical: req.CanonicalArgs, Secret: req.Secret, AppName: c.AppName, VerifiedAt: &now}
	if c.GrantID != "" {
		n.GrantID = &c.GrantID
	}
	if ttl := e.mcpTTL(c, req); ttl != 0 {
		expiry := now.Add(ttl)
		n.ExpiresAt = &expiry
	}
	return n
}

// prior is the existing subscription with n's id; one on another site is
// deleted (and siteChanged), so the move is proved afresh.
func (e *Engine) prior(ctx context.Context, n model.Notification) (old model.Notification, siteChanged bool, err error) {
	old, err = e.svc.GetNotification(ctx, n.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Notification{}, false, nil
	}
	if err != nil || old.SiteID == n.SiteID {
		return old, false, err
	}
	_, err = e.svc.DeleteNotification(ctx, n.ID)
	return model.Notification{}, true, err
}

// prove sets n's proof of URL ownership: a fresh challenge when the URL has
// no recent proof or the subscription moved site, otherwise the existing
// proof's time (a refresh never extends its 30-day lifetime).
func (e *Engine) prove(ctx context.Context, env Env, n *model.Notification, siteChanged bool) error {
	verified, err := e.svc.RecentlyVerifiedNotification(ctx, n.OwnerID, n.URL)
	if err != nil {
		return err
	}
	if !verified || siteChanged {
		return e.challengeURL(ctx, env, *n)
	}
	at, err := e.svc.VerifiedAt(ctx, n.OwnerID, n.URL)
	n.VerifiedAt = &at
	return err
}

// challengeURL posts a random challenge to n's URL, which must echo it.
func (e *Engine) challengeURL(ctx context.Context, env Env, n model.Notification) error {
	release, ok := e.verifySlot()
	if !ok {
		return eventError(EventCodeCallbackEndpoint, "reason", "timeout")
	}
	defer release()
	c, err := e.challengeClient(ctx, n.OwnerID, n.URL)
	if errors.Is(err, Failure("rate_limited")) {
		return &EventError{Code: EventCodeResourceExhausted, Message: "verification rate limit reached", Data: map[string]any{"limit": "challenges", "max": 10}}
	}
	if err != nil {
		return eventError(EventCodeCallbackEndpoint, "reason", Category(err))
	}
	random := make([]byte, 24)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	challenge := base64.RawURLEncoding.EncodeToString(random)
	body, _ := json.Marshal(map[string]string{"type": "verification", "challenge": challenge})
	result := c.send(ctx, env, n, RandomID("msg_verification_", 16), body)
	if !result.Delivered {
		return eventError(EventCodeCallbackEndpoint, "reason", result.Result)
	}
	var echo struct {
		Challenge string          `json:"challenge"`
		Type      json.RawMessage `json:"type"`
	}
	if json.Unmarshal(result.body, &echo) != nil || echo.Type != nil || subtle.ConstantTimeCompare([]byte(echo.Challenge), []byte(challenge)) != 1 {
		return eventError(EventCodeCallbackEndpoint, "reason", "challenge_failed")
	}
	return nil
}

// Subscribe creates or refreshes an app's MCP Events subscription.
func (e *Engine) Subscribe(ctx context.Context, env Env, c Subscriber, req EventSubscribeRequest) (EventSubscribeResult, error) {
	out := EventSubscribeResult{}
	if err := EventsAvailability(env); err != nil {
		return out, err
	}
	site, err := e.mcpSite(ctx, c, req)
	if err != nil {
		return out, err
	}
	cursor, err := e.checkSubscription(req)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, e.limits.NotifyTimeout)
	defer cancel()
	if _, err = e.client.validate(ctx, req.URL); err != nil {
		code := EventCodeCallbackEndpoint
		if errors.Is(err, Failure("invalid_url")) {
			code = -32602
		}
		return out, eventError(code, "reason", Category(err))
	}
	n := e.mcpNotification(c, req, site)
	old, siteChanged, err := e.prior(ctx, n)
	if err != nil {
		return out, err
	}
	if err = e.prove(ctx, env, &n, siteChanged); err != nil {
		return out, err
	}
	return e.saveMCP(ctx, env, n, cursor, old)
}

// saveMCP saves the proved subscription and answers with its replay
// watermark; a previous "ok" is no error to report.
func (e *Engine) saveMCP(ctx context.Context, env Env, n model.Notification, cursor *int64, old model.Notification) (EventSubscribeResult, error) {
	refreshed, truncated, err := e.svc.SaveNotification(ctx, n, e.limits.NotifyMaxPerSite, cursor, e.limits.NotifyHistoryDays, env.Secrets())
	switch {
	case errors.Is(err, model.ErrNotificationAccess):
		return EventSubscribeResult{}, eventError(EventCodeForbidden, "reason", "notification_access_ended")
	case errors.Is(err, model.ErrNotificationCap):
		return EventSubscribeResult{}, &EventError{Code: EventCodeResourceExhausted, Message: "too many subscriptions", Data: map[string]any{"limit": "subscriptions", "max": e.limits.NotifyMaxPerSite}}
	case err != nil:
		return EventSubscribeResult{}, err
	}
	seq, err := e.svc.NotificationCursor(ctx, n.ID, n.SiteID)
	if err != nil {
		return EventSubscribeResult{}, err
	}
	watermark := fmt.Sprintf("c_%d", seq)
	e.signal()
	lastError := old.LastResult
	if lastError == "ok" {
		lastError = ""
	}
	return EventSubscribeResult{ID: n.ID, RefreshBefore: n.ExpiresAt, Cursor: &watermark, Truncated: truncated, Refreshed: refreshed, Active: true, LastError: lastError}, nil
}

// Unsubscribe removes an app's subscription; idempotent, as ChatGPT
// requires. The id derives from the caller's account and grant, so it can
// only name that app's own subscription.
func (e *Engine) Unsubscribe(ctx context.Context, c Subscriber, req EventUnsubscribeRequest) error {
	if c.Owner == "" || c.DeployOnly {
		return eventError(EventCodeForbidden, "reason", "owner_required")
	}
	_, err := e.svc.DeleteNotification(ctx, mcpID(c.Owner, c.GrantID, req.URL, req.Name, req.CanonicalArgs))
	return err
}
