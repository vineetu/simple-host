package handler

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/vsriram/simple-host/internal/app/notifications"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/webassets"
)

// pushSWModTime is used only for the service worker's conditional-GET
// headers (ServeContent); the process start time is a safe, stable stand-in
// for the embedded file's own mtime, which a Go embed does not retain.
var pushSWModTime = time.Now()

// SIM-360: Web Push is a new delivery kind in the existing site_notifications
// engine (webhook | mcp-event | web-push), gated by WEB_PUSH_ACCOUNTS. A
// push subscription is a row there, exactly like a webhook or MCP Events
// subscription: same table, same event filter, same outbox, retries,
// budgets, rate limits and dashboard list (owner's product decision). The
// only new rule is who may create one: a signed-in visitor may subscribe
// their own device, scoped to events about their own data; the owner
// subscribes their own devices for events they choose. Never a broadcast to
// every visitor, and never events about another visitor's data -- enforced
// in the shared event fan-out (repository/notifications EmitSiteEventFor),
// not here.

// ---------------------------------------------------------------- the flag

// SetWebPushAccounts installs WEB_PUSH_ACCOUNTS (cfg.WebPushAccounts,
// cfg.WebPushAccountsAll): the accounts allowed to use web push, or "every
// account" when all is true. Called once at startup, like
// UserHandler.SetPreviewAccounts.
func (h *SiteHandler) SetWebPushAccounts(accounts map[string]bool, all bool) {
	h.webPushAccounts = accounts
	h.webPushAccountsAll = all
}

// webPushEnabledForHandle reports whether handle's sites may use web push.
func (h *SiteHandler) webPushEnabledForHandle(handle string) bool {
	if h.webPushAccountsAll {
		return true
	}
	return h.webPushAccounts[strings.ToLower(strings.TrimSpace(handle))]
}

// webPushAllowed reports whether siteID's own account may use web push: the
// gate every push route and auth.js's SH.push.* must pass, regardless of
// who (owner or visitor) is calling. The error is a real lookup failure
// (the site row could not be read), never "the flag is off" -- callers on
// the delivery path (sendPush) must tell the two apart, since a transient
// DB hiccup must retry, not finalize the delivery as cleanly skipped
// (FIX 13: a lookup failure used to collapse to false, indistinguishable
// from a conclusive "off").
func (h *SiteHandler) webPushAllowed(ctx context.Context, siteID string) (bool, error) {
	// Simple Host only for this first pass (FEATURES.md §19, PARITY.md):
	// Simple Hack and Enterprise have not ported web push. Not a lookup
	// failure: the edition is known at startup, not looked up per call.
	if h.ed.Notifications().EventScoped {
		return false, nil
	}
	handle, _, _, err := h.sitesSvc().GetSiteOwner(ctx, siteID)
	if err != nil {
		return false, err
	}
	return h.webPushEnabledForHandle(handle), nil
}

func webPushNotOffered(w http.ResponseWriter) {
	storageError(w, 404, "not_offered", "web push is not enabled for this site")
}

// -------------------------------------------------------------- VAPID keys

const (
	vapidPublicConfigKey  = "web_push_vapid_public"
	vapidPrivateConfigKey = "web_push_vapid_private"
)

// vapidKeys returns the one P-256 VAPID keypair for this install, generating
// and persisting it on first use (sealed with PASSCODE_ENC_KEY, the same key
// that already protects notification signing secrets). The private key is
// never logged; the public key is served to pages so the browser's
// PushManager.subscribe() can use it as applicationServerKey.
func (h *SiteHandler) vapidKeys(ctx context.Context) (pub, priv string, err error) {
	h.webPushVapidMu.Lock()
	defer h.webPushVapidMu.Unlock()
	if h.webPushVapidReady {
		return h.webPushVapidPub, h.webPushVapidPriv, nil
	}
	pub, priv, err = h.loadOrGenerateVAPIDKeys(ctx)
	if err != nil {
		return "", "", err
	}
	h.webPushVapidPub, h.webPushVapidPriv, h.webPushVapidReady = pub, priv, true
	return pub, priv, nil
}

func (h *SiteHandler) loadOrGenerateVAPIDKeys(ctx context.Context) (string, string, error) {
	if !h.NotificationsReady() {
		return "", "", errors.New("PASSCODE_ENC_KEY not configured; web push needs it to protect the VAPID private key")
	}
	pub, sealed, err := vapidConfigRead(ctx, h.database)
	if err != nil {
		return "", "", err
	}
	if pub == "" || sealed == "" {
		newPriv, newPub, genErr := webpush.GenerateVAPIDKeys()
		if genErr != nil {
			return "", "", genErr
		}
		encBytes, sealErr := h.passcode.sealWithAD("vapid", newPriv)
		if sealErr != nil {
			return "", "", sealErr
		}
		sealedNew := "enc1:" + base64.StdEncoding.EncodeToString(encBytes)
		// This one-time, install-wide write must outlive whatever
		// triggered it (an HTTP request whose own context may cancel):
		// its own bounded timeout, never the caller's ctx, so a canceled
		// request can never abort a write every future caller depends on.
		writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		insertErr := vapidConfigInsertIfAbsent(writeCtx, h.database, newPub, sealedNew)
		cancel()
		if insertErr != nil {
			return "", "", insertErr
		}
		// Re-read rather than trust our own candidate: a concurrent first
		// boot may have won the race, and the two rows must stay paired.
		pub, sealed, err = vapidConfigRead(ctx, h.database)
		if err != nil {
			return "", "", err
		}
		log.Printf("web push: generated the install's VAPID keypair")
	}
	encoded, ok := strings.CutPrefix(sealed, "enc1:")
	if !ok {
		return "", "", errors.New("vapid private key unreadable")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", errors.New("vapid private key unreadable")
	}
	priv, err := h.passcode.openWithAD("vapid", raw)
	if err != nil {
		return "", "", errors.New("vapid private key unreadable (did PASSCODE_ENC_KEY change?)")
	}
	return pub, priv, nil
}

func vapidConfigRead(ctx context.Context, db *sql.DB) (pub, sealed string, err error) {
	rows, err := db.QueryContext(ctx, `SELECT key,value FROM instance_config WHERE key IN ($1,$2)`, vapidPublicConfigKey, vapidPrivateConfigKey)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return "", "", err
		}
		if k == vapidPublicConfigKey {
			pub = v
		} else {
			sealed = v
		}
	}
	return pub, sealed, rows.Err()
}

func vapidConfigInsertIfAbsent(ctx context.Context, db *sql.DB, pub, sealed string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance_config(key,value) VALUES($1,$2) ON CONFLICT (key) DO NOTHING`, vapidPublicConfigKey, pub); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO instance_config(key,value) VALUES($1,$2) ON CONFLICT (key) DO NOTHING`, vapidPrivateConfigKey, sealed); err != nil {
		return err
	}
	return tx.Commit()
}

// ------------------------------------------------------------- the payload

// pushPushSkip is the sentinel Body (deps.Body) returns for a delivery that
// must not actually be sent: muted by this device, or ruled out by the
// owner's recipients control for this resource. sendPush recognises it and
// reports a clean, non-retried, non-failing outcome.
var pushPushSkip = []byte("null")

// pushPayload builds the small, generic-by-default payload a push service
// and a lock screen show -- never the row's own content unless the owner
// named one column for this resource, and the dashboard says so. It also
// carries the mute and recipients gates, since both need the event's own
// resource (data["storage"]) together with n, which only this function
// (not sendPush, which only sees the rendered body) has at hand.
// pushPayloadDefer is the sentinel Body (deps.Body) returns on a genuine
// failure (decode error, a push-rule lookup that could not be answered):
// the delivery must be retried, never silently delivered with recipients
// wider than configured. nil, not pushPushSkip, so ProcessDelivery leaves
// the claim to expire and be retried rather than finishing it as handled.
var pushPayloadDefer []byte

func (h *SiteHandler) pushPayload(ctx context.Context, n model.Notification, d model.NotificationDelivery) []byte {
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(d.Data))
	dec.UseNumber()
	if dec.Decode(&data) != nil {
		return pushPushSkip
	}
	resource, _ := data["storage"].(string)
	siteName, _ := data["site"].(string)
	siteURL, _ := data["site_url"].(string)
	// A mute applies to the event's own resource regardless of what this
	// device's own subscription filter is scoped to: a site-wide device
	// can mute one table without unsubscribing from the rest.
	for _, muted := range n.MutedResources {
		if muted == resource {
			return pushPushSkip
		}
	}
	rule, err := h.notificationsSvc().GetPushRule(ctx, n.SiteID, resource)
	if err != nil {
		// A real lookup failure must defer, never silently fall back to
		// "both" -- that would widen recipients past whatever the owner
		// configured (possibly "none") for as long as the failure lasts.
		return pushPayloadDefer
	}
	switch rule.Recipients {
	case "none":
		return pushPushSkip
	case "owner":
		if n.SubscriberRole == "visitor" {
			return pushPushSkip
		}
	case "row_owner":
		if n.SubscriberRole != "visitor" {
			return pushPushSkip
		}
	}
	title := strings.TrimSpace(rule.Title)
	if title == "" {
		title = "New activity"
		if siteName != "" {
			title = siteName + " has an update"
		}
	}
	body := "Something was added or changed."
	if rule.BodyColumn != "" && pushBodyColumnAllowed(rule.BodyColumn) {
		if fields, ok := data["fields"].(map[string]any); ok {
			// Even a permitted column's actual value falls back to
			// generic text if it looks like an email or IP address
			// (adversary review, item A, binding): the owner chose the
			// column, but a user-typed "notes" or "message" field can
			// still happen to contain one, and the blacklist above only
			// ever looks at the column's name, never its content.
			if s, ok := pushScalarString(fields[rule.BodyColumn]); ok && s != "" && !pushValueLooksSensitive(s) {
				body = notifyCut(s, 200)
			}
		}
	}
	payload := map[string]any{"title": notifyCut(title, 120), "body": body, "url": siteURL, "tag": d.Name}
	raw, err := json.Marshal(payload)
	if err != nil {
		return pushPushSkip
	}
	return raw
}

// pushForbiddenBodyColumns are exact (case-insensitive) column names a
// push rule's body_column may never name: identity and network fields
// that must never reach a lock screen, even though the owner's own
// dashboard only offers columns that exist on the resource (defense in
// depth: this is re-checked here, at render time, not only at save time).
// Kept for names pushSplitColumnWords would not otherwise catch (no
// separator at all, e.g. "ipaddress"); pushForbiddenBodyColumnWords
// below is what actually catches most real-world variants.
var pushForbiddenBodyColumns = map[string]bool{
	"email": true, "emails": true, "visitor_email": true, "visitor_emails": true,
	"contact_email": true, "owner_email": true,
	"ip": true, "ip_address": true, "ipaddress": true, "remote_ip": true, "client_ip": true,
	"visitor_id": true, "visitorid": true, "owner_id": true, "user_id": true, "userid": true,
	"password": true, "passcode": true, "secret": true, "token": true, "api_key": true, "apikey": true,
	"auth": true, "session": true, "session_id": true, "cookie": true,
	"ssn": true, "phone": true, "phone_number": true, "address": true,
}

// pushForbiddenBodyColumnWords blocks a column name that contains any of
// these as a whole underscore/hyphen/camelCase-separated word, not only
// an exact match (adversary review, item A: "email_address",
// "customerEmail" and "ip_addr" all slipped past the exact-name
// blacklist above, since none of them equal "email" or "ip" exactly).
// Harmless names like "description" or "note" are unaffected: they
// contain none of these words.
var pushForbiddenBodyColumnWords = map[string]bool{
	"email": true, "mail": true, "ip": true, "phone": true, "tel": true,
	"telephone": true, "address": true, "ssn": true,
	"password": true, "passcode": true, "secret": true, "token": true,
	"apikey": true, "api": true, "auth": true, "session": true, "cookie": true,
}

// pushForbiddenBodyColumnIDPhrases: paired with a following "id" word
// (visitor_id, ownerID, user-id, ...), these name a specific person, not
// a harmless reference like "order_id" or "team_id". "id" alone is too
// common a column name to block outright.
var pushForbiddenBodyColumnIDPhrases = map[string]bool{
	"visitor": true, "owner": true, "user": true, "client": true,
	"account": true, "customer": true, "person": true,
}

// pushSplitColumnWords splits a column name into lowercase words on
// underscores, hyphens, spaces and camelCase boundaries: "email_address",
// "emailAddress" and "EmailAddress" all split to ["email", "address"].
func pushSplitColumnWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case unicode.IsUpper(r) && i > 0 && !unicode.IsUpper(runes[i-1]):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

func pushBodyColumnAllowed(name string) bool {
	trimmed := strings.TrimSpace(name)
	if pushForbiddenBodyColumns[strings.ToLower(trimmed)] {
		return false
	}
	// pushSplitColumnWords needs the original case to find camelCase
	// boundaries ("customerEmail"); lowercasing first (as the exact-name
	// check above does) would destroy exactly the signal it looks for.
	words := pushSplitColumnWords(trimmed)
	for i, w := range words {
		if pushForbiddenBodyColumnWords[w] {
			return false
		}
		if w == "id" && i > 0 && pushForbiddenBodyColumnIDPhrases[words[i-1]] {
			return false
		}
	}
	return true
}

// pushEmailLikePattern/pushIPv4LikePattern/pushIPv6LikePattern are cheap,
// deliberately loose checks (adversary review, item A, binding rule): a
// permitted column's actual value still falls back to generic text if it
// looks like an email address or an IP, regardless of what the column is
// named -- a free-text "notes"/"message" field can contain one even when
// its own name is perfectly innocuous.
var (
	pushEmailLikePattern = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
	pushIPv4LikePattern  = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	pushIPv6LikePattern  = regexp.MustCompile(`[0-9a-fA-F]{1,4}:[0-9a-fA-F:]{2,}[0-9a-fA-F]`)
)

func pushValueLooksSensitive(s string) bool {
	return pushEmailLikePattern.MatchString(s) || pushIPv4LikePattern.MatchString(s) || pushIPv6LikePattern.MatchString(s)
}

// pushScalarString returns v's displayable text, ok only for a JSON
// scalar (string, number, bool); an object or array is refused rather
// than stringified, since a structured value could carry fields the
// owner never intended to show (SIM-360 privacy guardrail: the push
// payload never carries more than the one named column's plain value).
func pushScalarString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
}

// ---------------------------------------------------------------- sending

// sendPush is the web-push dispatch inside notificationDeliveryDeps().Send.
// It reuses the webhook delivery client's SSRF-guarded dial and validate
// (DNS-rebinding-safe, public-IP-only, same test hook as webhooks) for the
// push endpoint too, since it is just another attacker-influenced HTTPS URL
// fetched server-side. Only the encrypted payload (RFC 8291 aes128gcm,
// github.com/SherClockHolmes/webpush-go) and the RFC 8292 VAPID JWT ever
// leave this process; the subscription row itself (endpoint, keys) never
// does. A 404 or 410 from the push service means the browser dropped the
// subscription: delete it so nothing retries against a dead endpoint.
func (h *SiteHandler) sendPush(ctx context.Context, n model.Notification, body []byte) notifications.DeliveryResult {
	if bytes.Equal(body, pushPushSkip) {
		return notifications.DeliveryResult{Delivered: true, Result: "filtered"}
	}
	// The flag is re-checked at send time, not only at subscribe time: an
	// account removed from WEB_PUSH_ACCOUNTS (or Simple Hack, which is
	// never allowed) must stop receiving pushes immediately, without
	// waiting for every existing device to be individually unsubscribed.
	// Marked delivered (not retried): an owner-controlled, expected state,
	// not a failure, and unsubscribe/mute/sign-out/deletion -- never this
	// check -- are what actually remove the subscription. A genuine lookup
	// failure is different from a conclusive "off": Delivered stays false
	// so the existing retry/backoff handles it, instead of finalizing a
	// delivery nobody ever actually decided to disable (FIX 13).
	allowed, err := h.webPushAllowed(ctx, n.SiteID)
	if err != nil {
		return notifications.DeliveryResult{Result: "flag_lookup_failed"}
	}
	if !allowed {
		return notifications.DeliveryResult{Delivered: true, Result: "flag_disabled"}
	}
	// NOTIFY_PUSH_PER_SITE_PER_DAY / NOTIFY_PUSH_PER_PERSON_PER_DAY: push's
	// own budgets on top of the general event cap. Neither token is spent
	// by a muted/filtered delivery above, only a real send attempt;
	// exhausted budgets leave the delivery "pending" so the existing
	// retry/backoff/suspend machinery, not a bespoke one, is what eventually
	// gives up (NotifyAttempts) if the day never clears. The two buckets
	// are reserved together, not independently (FIX 7, Codex round 2):
	// checking site-then-person used to spend the site's shared token on
	// every attempt from an already-exhausted person, even though nothing
	// was ever sent, starving every other recipient on that site. Taking
	// the site token first and refunding it when the person's own is
	// exhausted charges neither when the pair as a whole is refused.
	// (These remain refillable, process-local token buckets rather than
	// durable daily counters -- a restart resets them, and a full burst
	// plus refill can exceed the advertised daily figure over a long
	// enough window; documented as a rolling limit, not a hard quota, in
	// FEATURES.md.)
	e := h.notifyEngine()
	if !e.pushSiteDay.allow(n.SiteID) {
		return notifications.DeliveryResult{Result: "push_rate_limited"}
	}
	if !e.pushPersonDay.allow(n.OwnerID) {
		e.pushSiteDay.refund(n.SiteID)
		return notifications.DeliveryResult{Result: "push_rate_limited"}
	}
	pub, priv, err := h.vapidKeys(ctx)
	if err != nil || pub == "" || priv == "" {
		return notifications.DeliveryResult{Result: "push_not_configured"}
	}
	client := &e.client
	if _, verr := client.validate(ctx, n.URL); verr != nil {
		return notifications.DeliveryResult{Result: notifyCategory(verr)}
	}
	ctx, cancel := context.WithTimeout(ctx, client.notifyTimeout())
	defer cancel()
	sub := &webpush.Subscription{Endpoint: n.URL, Keys: webpush.Keys{P256dh: n.PushP256dh, Auth: n.PushAuth}}
	httpClient := &http.Client{
		Timeout:   client.notifyTimeout(),
		Transport: &http.Transport{Proxy: nil, DialContext: client.dial, TLSClientConfig: &tls.Config{RootCAs: client.roots, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true},
		// Same as the webhook client: a public, HTTPS-only destination
		// must never be allowed to redirect somewhere else (another
		// origin, or back down to plain HTTP) and have us follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := webpush.SendNotificationWithContext(ctx, body, sub, &webpush.Options{
		HTTPClient: httpClient, Subscriber: "https://" + h.siteDomain + "/",
		TTL: 300, Urgency: webpush.UrgencyNormal,
		VAPIDPublicKey: pub, VAPIDPrivateKey: priv,
	})
	if err != nil {
		return notifications.DeliveryResult{Result: notifyCategory(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if _, derr := h.notificationsSvc().DeleteNotification(ctx, n.ID); derr != nil {
			log.Printf("web push: delete unregistered subscription %s: %v", n.ID, derr)
		}
		return notifications.DeliveryResult{Status: resp.StatusCode, Result: "unregistered"}
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return notifications.DeliveryResult{Delivered: true, Status: resp.StatusCode, Result: "ok"}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// CheckRedirect above stops the client from following it; the
		// redirect itself is never treated as delivery.
		return notifications.DeliveryResult{Status: resp.StatusCode, Result: "redirect"}
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return notifications.DeliveryResult{Status: resp.StatusCode, Result: "http_4xx"}
	default:
		return notifications.DeliveryResult{Status: resp.StatusCode, Result: "http_5xx"}
	}
}

// decodePushKey decodes a browser-supplied subscription key, tolerating
// every base64 flavour a PushSubscription.getKey() value might arrive in
// (the same tolerance github.com/SherClockHolmes/webpush-go's own decoder
// has), since nothing here controls how the browser encoded it.
func decodePushKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

// validPushKeys rejects a malformed subscription before it is ever saved:
// p256dh must decode to a point actually on the P-256 curve (RFC 8291),
// and auth must decode to exactly the 16-byte authentication secret the
// spec defines. A subscription that fails this can never be sent to
// anyway, so saving it would only consume the site's subscription cap and
// repeatedly fail with a misleading network-looking result.
func validPushKeys(p256dh, auth string) bool {
	point, err := decodePushKey(p256dh)
	if err != nil {
		return false
	}
	if _, err := ecdh.P256().NewPublicKey(point); err != nil {
		return false
	}
	authBytes, err := decodePushKey(auth)
	return err == nil && len(authBytes) == 16
}

// pushSWBytes is the service worker's content, read once from the same
// embedded tree the platform's own static pipeline serves it from
// (internal/webassets/static/push-sw.js) -- one source of truth, read
// directly rather than through that pipeline's host-specific dispatch.
var pushSWBytes = func() []byte {
	b, err := webassets.Static.ReadFile("static/push-sw.js")
	if err != nil {
		log.Printf("web push: embedded push-sw.js missing: %v", err)
		return nil
	}
	return b
}()

// pushSWRoute answers GET/HEAD /v1/push/sw.js. FIX 2+11, Codex round 2:
// this used to be ServePushSW, a middleware wrapping the entire outer
// handler chain to intercept a root path (/push-sw.js) before any host's
// own dispatch ran -- which (a) needed its own nginx location added to
// every vhost template (custom domains, family hosts) that serves static
// files directly instead of reaching Go at all, since nginx, not Go,
// answers most requests on those, and (b) unconditionally shadowed any
// site that happened to have its own file at that exact root path, flag
// on or off. Both problems disappear by moving the worker under /v1/,
// a path nginx already proxies to Go unconditionally on every vhost
// template (`location ^~ /v1/`) and no site's own files are ever served
// under at all -- an ordinary mux route here, like every other push
// route, nothing extra to add anywhere. auth.js registers it with an
// explicit {scope: '/v1/push/'}, not root: a push event and a
// notification click both work from any scope (neither is about
// controlling a page), so the worker never needs, and never takes, the
// wider scope a site's own root-scoped service worker (if it has one)
// still exclusively owns.
func (h *SiteHandler) pushSWRoute(w http.ResponseWriter, r *http.Request) {
	if len(pushSWBytes) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "sw.js", pushSWModTime, bytes.NewReader(pushSWBytes))
}

// ----------------------------------------------------------------- routes

func (h *SiteHandler) registerWebPush(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
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
	mux.Handle("GET /v1/sites/{sitename}/push/vapid-key", h.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushVAPIDKeyRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/subscribe", h.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushSubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/unsubscribe", h.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushUnsubscribeRoute))))
	mux.Handle("POST /v1/sites/{sitename}/push/mute", h.passcodeGate(rateLimitByIP(limiter, http.HandlerFunc(h.pushMuteRoute))))
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

// pushVAPIDKeyRoute hands the page the one public key every subscribe()
// call needs for PushManager.subscribe({applicationServerKey}). Flag-gated:
// 404 off, like every push route, so a page can tell push is unavailable
// rather than silently failing.
func (h *SiteHandler) pushVAPIDKeyRoute(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	allowed, err := h.webPushAllowed(r.Context(), c.siteID)
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	if !allowed {
		webPushNotOffered(w)
		return
	}
	if !h.requireNotifications(w) {
		return
	}
	pub, _, err := h.vapidKeys(r.Context())
	if err != nil || pub == "" {
		notifyError(w, 500, "internal_error")
		return
	}
	writeJSON(w, 200, map[string]any{"public_key": pub})
}

// pushCookieRequestOK is the CSRF/same-origin check every visitor-cookie
// write route elsewhere in this codebase makes (logoutVisitor's
// hasVisitorCSRF+sameOriginRequest pair; storage writes' call.CSRFOK) --
// missing here until now (adversary review, item C). storageSite only
// ever sets c.owner via a true X-API-Key credential (never a cookie), so
// a true owner call carries no ambient browser credential to forge and
// skips this; every cookie-based caller (an ordinary visitor, or the
// owner signed in on their own site as a visitor, D1) must pass it,
// before pushCallerIdentity ever resolves which of those two it is.
func (h *SiteHandler) pushCookieRequestOK(c storageCall, r *http.Request, w http.ResponseWriter) bool {
	if c.owner {
		return true
	}
	// The literal header, like storage writes' call.CSRFOK -- not
	// hasVisitorCSRF's looser Content-Type fallback (logoutVisitor),
	// which a plain JSON POST already satisfies without the header at
	// all and so would not actually add anything here.
	if r.Header.Get("X-SH-CSRF") != "1" {
		notifyError(w, 403, "csrf_required")
		return false
	}
	if !sameOriginRequest(r) {
		notifyError(w, 403, "forbidden")
		return false
	}
	return true
}

// pushCallerIdentity resolves who is calling a visitor-facing push route
// (subscribe/unsubscribe/mute): the owner via API key or account cookie
// (storageSite's c.owner), the owner signed in on their own site as a
// visitor (D1's rule, reimplemented here since these routes resolve no
// single storage resource the way storageOwnerOnSite expects), or an
// ordinary signed-in visitor. Writes a 403 and returns ok=false when
// nobody is signed in at all.
// family is this session's own push-cleanup scoping key (FIX 10): see
// pushFamilyFor. "" for a true owner API key, which is not a browser
// session at all and so is never reached by a sign-out's family-scoped
// delete (consistent with every push row an API key creates today).
func (h *SiteHandler) pushCallerIdentity(w http.ResponseWriter, r *http.Request, c storageCall) (role, ownerID, family string, ok bool) {
	if c.owner {
		return "owner", c.ownerID, "", true
	}
	sess, signedIn := h.strictVisitorSession(r, c.siteID)
	if !signedIn || sess.UserID == "" {
		storageError(w, 403, "forbidden", "sign in on this site first")
		return "", "", "", false
	}
	family = pushFamilyFor(sess)
	if sess.UserID == c.ownerID && r.Header.Get("X-SH-Framed") == "" && !h.previewRefererFor(r, c.siteID) && h.storageOwnHost(r, c.siteID) {
		// D1: the site's owner, signed in on their own site's own address
		// (never framed, never a preview), gets owner rights here too,
		// the same as for storage data -- a site-wide push device, not a
		// resource- and event-forced visitor one.
		return "owner", c.ownerID, family, true
	}
	return "visitor", sess.UserID, family, true
}

// pushFamilyFor is the session's own push-cleanup scoping key (FIX 10,
// Codex round 2): the session's SSO pass hash when it is part of a
// signed-in-everywhere family (so one sign-out reaches every site that
// family spans), else the session's own id (so an ordinary single-site
// session's sign-out reaches only its own device, never a different
// browser's subscription for the same person on the same site -- see
// DeletePushSubscriptionsForFamily).
func pushFamilyFor(sess model.VisitorSession) string {
	if sess.SSOPassHash.Valid && sess.SSOPassHash.String != "" {
		return sess.SSOPassHash.String
	}
	return hex.EncodeToString(sess.ID)
}

type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Resource string   `json:"resource"`
	Events   []string `json:"events"`
	Label    string   `json:"label"`
}

// pushSubscribeRoute registers one device for the owner or a signed-in
// visitor. It reuses SaveNotification exactly as the owner's webhook
// create flow does (owner decision: push is a kind of this engine, not a
// parallel one), so the push subscription gets the same cap, outbox and
// dashboard listing for free. A visitor's device is for the whole site,
// same as the owner's, since the privacy boundary that keeps a visitor's
// device from ever reaching another visitor's data is enforced per event
// in the fan-out (EmitSiteEventFor), not by naming a resource up front; a
// visitor may still optionally name one resource to scope a device to it
// (the ticket's "mute per resource" covers the rest). A visitor's events
// are always forced to exactly the record/file events, never a site-level
// one, by construction, regardless of what the request body asks for.
func (h *SiteHandler) pushSubscribeRoute(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	if !h.pushCookieRequestOK(c, r, w) {
		return
	}
	allowed, err := h.webPushAllowed(r.Context(), c.siteID)
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	if !allowed {
		webPushNotOffered(w)
		return
	}
	if !h.requireNotifications(w) {
		return
	}
	var req pushSubscribeRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil {
		notifyError(w, 400, "invalid_request")
		return
	}
	role, ownerID, family, ok := h.pushCallerIdentity(w, r, c)
	if !ok {
		return
	}
	resource := strings.TrimSpace(req.Resource)
	events := req.Events
	if role == "visitor" {
		// Always exactly the record/file events, never a site-level one:
		// the privacy boundary ("scoped to events about their own data")
		// is the fan-out's own visitor_id match (EmitSiteEventFor), which
		// applies whatever resource (or none, site-wide) this device
		// names; naming one here only narrows which table's events this
		// device's filter even considers, same as a webhook subscription's
		// optional storage filter always has.
		events = append([]string{}, notifyRecordEvents...)
	} else if !h.validNotifyEvents(events) {
		notifyError(w, 400, "invalid_events")
		return
	}
	if resource != "" {
		if _, err := h.loadStorageResource(r.Context(), c.siteID, resource); err != nil {
			notifyError(w, 404, "storage_not_found")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.limits.NotifyTimeout)
	defer cancel()
	if _, err := h.notifyEngine().client.validate(ctx, req.Endpoint); err != nil {
		code := "invalid_url"
		if errors.Is(err, notifyFailure("refused_address")) {
			code = "refused_address"
		}
		notifyError(w, 400, code)
		return
	}
	if !validPushKeys(req.Keys.P256dh, req.Keys.Auth) {
		notifyError(w, 400, "invalid_keys")
		return
	}
	now := time.Now()
	n := model.Notification{
		SiteID: c.siteID, OwnerID: ownerID, Source: "owner",
		URL: req.Endpoint, Events: events, Storage: resource, Label: req.Label,
		Kind: "web-push", SubscriberRole: role, PushP256dh: req.Keys.P256dh, PushAuth: req.Keys.Auth,
		DeviceLabel: req.Label, VerifiedAt: &now, PushFamily: family,
	}
	// Upsert by (site, subscriber, endpoint, resource), under the site's
	// own write lock -- a repeated subscribe() call for the exact same
	// browser subscription and resource (a page reload, the permission
	// prompt granted again after getSubscription() already returned the
	// existing one) must update that same row, never add a duplicate
	// that would double-deliver, spend the site's cap twice, and strand
	// the first row's id outside what the device itself tracks. FIX 8,
	// Codex round 2: the lookup and the insert/update now happen in one
	// transaction (UpsertPushSubscription), not as two separate calls a
	// concurrent second subscribe() could race between.
	id, _, err := h.notificationsSvc().UpsertPushSubscription(ctx, n, notifyRandom("push_", 12), h.limits.NotifyMaxPerSite)
	if errors.Is(err, model.ErrNotificationCap) {
		notifyError(w, 409, "too_many_notifications")
		return
	}
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	n.ID = id
	pub, _, err := h.vapidKeys(ctx)
	if err != nil {
		pub = ""
	}
	writeJSON(w, 201, map[string]any{"id": n.ID, "public_key": pub})
}

type pushIDRequest struct {
	ID string `json:"id"`
}

// pushUnsubscribeRoute ends push for one device: a visitor may only ever
// name their own (DeleteNotificationForOwner scopes by site+visitor id), so
// one person can never unsubscribe another's device by guessing an id. The
// owner may remove any device on their own site.
// pushUnsubscribeRoute and pushMuteRoute are intentionally reachable even
// when the account is not (or no longer) in WEB_PUSH_ACCOUNTS: revocation
// -- a person asking to stop being pushed to, or an owner who turned the
// flag off wanting existing devices cleared -- stays available while
// subscribing does not, the same asymmetry a removed consent must always
// have. They still 404 the same way when the site itself cannot be
// resolved at all.
func (h *SiteHandler) pushUnsubscribeRoute(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	if !h.pushCookieRequestOK(c, r, w) {
		return
	}
	var req pushIDRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req) != nil || req.ID == "" {
		notifyError(w, 400, "invalid_request")
		return
	}
	role, callerID, _, ok := h.pushCallerIdentity(w, r, c)
	if !ok {
		return
	}
	visitorID := ""
	if role == "visitor" {
		visitorID = callerID
	}
	deleted, err := h.notificationsSvc().DeleteNotificationForOwner(r.Context(), c.siteID, req.ID, visitorID)
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": deleted})
}

type pushMuteRequest struct {
	ID        string   `json:"id"`
	Resources []string `json:"resources"`
}

// pushMuteRoute replaces one device's per-resource mute list; ownership is
// checked the same way unsubscribe is.
func (h *SiteHandler) pushMuteRoute(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	if !h.pushCookieRequestOK(c, r, w) {
		return
	}
	var req pushMuteRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.ID == "" {
		notifyError(w, 400, "invalid_request")
		return
	}
	role, callerID, _, ok := h.pushCallerIdentity(w, r, c)
	if !ok {
		return
	}
	n, err := h.notificationsSvc().GetNotification(r.Context(), req.ID)
	if err != nil || n.SiteID != c.siteID || n.Kind != "web-push" || (role == "visitor" && (n.SubscriberRole != "visitor" || n.OwnerID != callerID)) {
		notifyError(w, 404, "not_found")
		return
	}
	if req.Resources == nil {
		req.Resources = []string{}
	}
	if len(req.Resources) > 50 {
		notifyError(w, 400, "too_many_resources")
		return
	}
	if err := h.notificationsSvc().SetMutedResources(r.Context(), req.ID, req.Resources); err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// pushDevicesRoute lists every device subscribed on a site -- the owner's
// device-management view ("Dashboard: devices subscribed per site").
// Owner-only (authMiddleware): never discloses one visitor's device to
// another, and the visitor's own device list is reached by the visitor's
// own subscribe response/local storage, not this route.
func (h *SiteHandler) pushDevicesRoute(w http.ResponseWriter, r *http.Request) {
	site, ok := h.ownerSiteOr404(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		// "Turn push off" for this site: every device, owner's and every
		// visitor's, at once. The per-device Unsubscribe route (reused by
		// the owner too, via pushCallerIdentity) removes just one; this
		// is the dashboard's single bulk action, not a new revocation
		// rule of its own.
		n, err := h.notificationsSvc().DeletePushSubscriptionsForSite(r.Context(), site.ID)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": n})
		return
	}
	devices, err := h.notificationsSvc().ListPushDevices(r.Context(), site.ID)
	if err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	out := make([]map[string]any, 0, len(devices))
	for _, d := range devices {
		// Field names match notificationView's own (subscriber_role,
		// storage), not a push-specific shorthand (role, resource): the
		// dashboard's merged notifications+devices list (FIX 12, Codex
		// round 2) reads one shape regardless of which endpoint a row
		// came from, and a mismatch here silently dropped every visitor
		// device from that list (dv.subscriber_role === 'visitor' never
		// matched dv.role).
		out = append(out, map[string]any{"id": d.ID, "subscriber_role": d.SubscriberRole, "label": d.DeviceLabel, "device_label": d.DeviceLabel, "storage": d.Storage, "events": d.Events, "muted_resources": d.MutedResources, "state": d.State, "created_at": d.CreatedAt, "last_success_at": d.LastSuccessAt, "last_result": d.LastResult})
	}
	writeJSON(w, 200, map[string]any{"devices": out})
}

// pushDeviceDeleteRoute removes one push device on the owner's site,
// owner's or any visitor's: see the route registration comment above for
// why the generic DELETE /notifications/{id} cannot reach a visitor-role
// row even for the site's own owner. Scoped to kind='web-push' (never a
// webhook/MCP-event row) and to this site, the same two checks
// DeleteNotificationForOwner's own site+visitor scoping makes for the
// visitor-facing unsubscribe route.
func (h *SiteHandler) pushDeviceDeleteRoute(w http.ResponseWriter, r *http.Request) {
	site, ok := h.ownerSiteOr404(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	n, err := h.notificationsSvc().GetNotification(r.Context(), id)
	if err != nil || n.SiteID != site.ID || n.Kind != "web-push" {
		notifyError(w, 404, "not_found")
		return
	}
	if _, err := h.notificationsSvc().DeleteNotification(r.Context(), id); err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// pushRulesRoute lists (GET) or upserts (PUT) the owner's per-resource push
// routing and payload controls (which events push and to whom, and the
// optional named body column).
func (h *SiteHandler) pushRulesRoute(w http.ResponseWriter, r *http.Request) {
	site, ok := h.ownerSiteOr404(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rules, err := h.notificationsSvc().ListPushRules(r.Context(), site.ID)
		if err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
		writeJSON(w, 200, map[string]any{"rules": rules})
		return
	}
	var req model.PushRule
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		notifyError(w, 400, "invalid_request")
		return
	}
	req.Resource = strings.TrimSpace(req.Resource)
	var resource storageResource
	if req.Resource != "" {
		var err error
		resource, err = h.loadStorageResource(r.Context(), site.ID, req.Resource)
		if err != nil {
			notifyError(w, 404, "storage_not_found")
			return
		}
	}
	switch req.Recipients {
	case "owner", "row_owner", "both", "none":
	default:
		notifyError(w, 400, "invalid_recipients")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.BodyColumn = strings.TrimSpace(req.BodyColumn)
	if len(req.Title) > 120 || len(req.BodyColumn) > 64 || !pushPlainText(req.Title) {
		notifyError(w, 400, "invalid_request")
		return
	}
	if !h.pushRuleBodyColumnValid(r, site, resource, req.BodyColumn) {
		notifyError(w, 400, "invalid_body_column")
		return
	}
	req.SiteID = site.ID
	if err := h.notificationsSvc().SetPushRule(r.Context(), req); err != nil {
		notifyError(w, 500, "internal_error")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// pushPlainText rejects control characters (newlines, tabs and the like)
// in a push rule's title: it is shown verbatim on a lock screen, so it
// must be exactly the one line the owner typed, never a multi-line or
// control-sequence payload. Length is checked separately by the caller.
func pushPlainText(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// pushRuleBodyColumnValid validates a push rule's body_column at save
// time, not only at render time (FIX 5, Codex round 2: the save route
// "does not validate column existence or even invoke the blacklist,
// contrary to the documentation"). It never reads row content -- the
// owner names the column and the dashboard warns it shows on the lock
// screen -- only whether the name itself is permitted (the same
// blacklist pushPayload checks at render time) and, for a SQLite
// resource, whether it actually exists as a column on at least one of
// the resource's tables. KV and files resources have no fixed column
// schema to check existence against (a KV value is arbitrary JSON, a
// file write has no "fields" at all); the blacklist alone applies there,
// and a column that happens not to be present yet simply falls back to
// the generic payload at render time, exactly like an owner-named column
// on an empty SQLite table already does.
func (h *SiteHandler) pushRuleBodyColumnValid(r *http.Request, site model.Site, resource storageResource, column string) bool {
	if column == "" {
		return true
	}
	if !pushBodyColumnAllowed(column) {
		return false
	}
	if resource.Name == "" || resource.Kind != "sqlite" {
		return true
	}
	call := storageCall{siteID: site.ID, ownerID: site.UserID, siteName: site.Name, resourceName: resource.Name, resource: resource, owner: true}
	svc := h.storageRequestSvc(r, call)
	insp, done, err := svc.OpenInspection(r.Context(), h.storageServiceCall(call), false)
	if err != nil {
		// Best-effort: a storage-layer hiccup (the resource busy, a
		// transient open failure) never blocks saving the rule itself;
		// the blacklist above already ran, and a nonexistent column
		// still only ever falls back to generic text at render time.
		return true
	}
	defer done()
	tables, err := insp.Tables()
	if err != nil {
		return true
	}
	for table := range tables {
		cols, _, err := insp.TableInfo(table)
		if err != nil {
			continue
		}
		if _, ok := cols[column]; ok {
			return true
		}
	}
	return false
}

// ownerSiteOr404 resolves {sitename} for an authMiddleware-wrapped owner
// route (X-API-Key or account cookie) and answers like a missing route
// when the flag is off, same as every other push route.
func (h *SiteHandler) ownerSiteOr404(w http.ResponseWriter, r *http.Request) (model.Site, bool) {
	user := auth.GetUser(r.Context())
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	site, err := h.sitesSvc().GetSiteByUser(r.Context(), user.ID, name)
	if err != nil {
		storageError(w, 404, "site_not_found", "site not found")
		return model.Site{}, false
	}
	allowed, err := h.webPushAllowed(r.Context(), site.ID)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return model.Site{}, false
	}
	if !allowed {
		webPushNotOffered(w)
		return model.Site{}, false
	}
	return site, true
}

// deleteVisitorPushOnSignOut is the server-side half of "the one Sign out
// ends push on that device" (SIM-360 privacy guardrail): resolves the
// visitor session /v1/visitor/logout is about to end (the same lookup
// endSiteSSO does, read before it deletes the row) and removes that
// person's push subscriptions for that one site, covering both an
// ordinary visitor's device and the site owner's own (D1: the owner
// signed in on their own site as a visitor can hold a push row stored
// under either role; this deletes by person, not by role). Best-effort
// and silent: a resolution failure, an expired session, or no push
// subscriptions at all are all fine outcomes, and none of them may block
// or fail sign-out.
func (h *SiteHandler) deleteVisitorPushOnSignOut(r *http.Request) {
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return
	}
	sess, err := h.storeAccounts().GetVisitorSession(r.Context(), id, h.limits.KeyIdleExpiry)
	if err != nil || sess.UserID == "" || sess.SiteID == "" || !strings.EqualFold(sess.Host, requestHostName(r)) {
		return
	}
	// FIX 10, Codex round 2: scoped to this one session's own family
	// (pushFamilyFor), not every push row this person has on this site
	// -- a different browser's own subscription, for the same person on
	// the same site, is a different family and survives. An SSO
	// signed-in-everywhere session's family reaches every site it
	// spans, not only this one, via the global (no site_id) delete.
	if _, err := h.notificationsSvc().DeletePushSubscriptionsForFamily(r.Context(), pushFamilyFor(sess)); err != nil {
		log.Printf("web push: delete on sign-out for site %s: %v", sess.SiteID, err)
	}
}

// deletePushForEmail removes a person's push subscriptions for one site by
// their sign-in email: used when a named viewer is removed, since
// site_viewers (and the removal route) key by email, not by user id, and
// push subscriptions key by owner_id (the person's own users.id). Scans
// this site's own push devices (bounded by NotifyMaxPerSite, normally a
// handful) and resolves each visitor-role one's email the same way the
// owner's visitor_id-to-email lookup already does, rather than adding a
// second, parallel email-resolution path. Wired as appsites.Deps.
// DeleteViewerPush (FIX 9, Codex round 2), so app/sites.RemoveSiteViewer
// runs this for every caller of that service method, not only the HTTP
// handler that used to call it separately; the returned error is logged
// by that caller, never surfaced as a reason the removal itself failed.
func (h *SiteHandler) deletePushForEmail(ctx context.Context, siteID, email string) error {
	devices, err := h.notificationsSvc().ListPushDevices(ctx, siteID)
	if err != nil {
		return err
	}
	var lastErr error
	for _, d := range devices {
		if d.SubscriberRole != "visitor" {
			continue
		}
		got, err := visitorEmail(ctx, h.database, d.OwnerID)
		if err != nil || !strings.EqualFold(got, email) {
			continue
		}
		if _, err := h.notificationsSvc().DeleteNotification(ctx, d.ID); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
