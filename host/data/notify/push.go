package notify

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	repo "github.com/vsriram/simple-host/host/data/repo/notify"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// Web Push is a delivery kind of this engine (webhook | mcp-event
// | web-push), gated by WEB_PUSH_ACCOUNTS. A push subscription is a row in
// site_notifications like any other: same event filter, outbox, retries,
// budgets and dashboard list. A signed-in visitor may subscribe their own
// device, scoped to events about their own data -- enforced in the event
// fan-out (EmitSiteEventFor), not here.

// pushState is the WEB_PUSH_ACCOUNTS gate and the install's VAPID keypair,
// cached once loaded. Only a success is cached: a transient failure must
// not poison the process the way sync.Once would.
type pushState struct {
	accounts map[string]bool
	all      bool

	mu        sync.Mutex
	ready     bool
	pub, priv string
}

// SetPushAccounts installs WEB_PUSH_ACCOUNTS: the account handles allowed
// to use web push, or every account when all is true. Called once at
// startup.
func (e *Engine) SetPushAccounts(accounts map[string]bool, all bool) {
	e.push.accounts, e.push.all = accounts, all
}

// PushAllowed reports whether siteID's account may use web push. The error
// is a failed lookup, never "off": the delivery path retries on it rather
// than finishing the delivery as cleanly skipped. Simple Hack has not
// ported web push.
func (e *Engine) PushAllowed(ctx context.Context, siteID string) (bool, error) {
	if e.ed.Notifications().EventScoped {
		return false, nil
	}
	handle, _, _, err := sitesrepo.GetSiteOwner(ctx, e.db, siteID)
	if err != nil {
		return false, err
	}
	return e.push.all || e.push.accounts[strings.ToLower(strings.TrimSpace(handle))], nil
}

const (
	vapidPublicConfigKey  = "web_push_vapid_public"
	vapidPrivateConfigKey = "web_push_vapid_private"
)

// VAPIDKeys is the install's one P-256 VAPID keypair, generated and stored
// (the private key sealed with PASSCODE_ENC_KEY) on first use. The private
// key is never logged; pages get the public key for PushManager.subscribe.
func (e *Engine) VAPIDKeys(ctx context.Context, env Env) (pub, priv string, err error) {
	e.push.mu.Lock()
	defer e.push.mu.Unlock()
	if e.push.ready {
		return e.push.pub, e.push.priv, nil
	}
	if !env.Ready() {
		return "", "", errors.New("PASSCODE_ENC_KEY not configured; web push needs it to protect the VAPID private key")
	}
	pub, sealed, err := e.storedVAPIDKeys(ctx, env)
	if err != nil {
		return "", "", err
	}
	if priv, err = env.open("vapid", sealed); err != nil {
		return "", "", errors.New("vapid private key unreadable (did PASSCODE_ENC_KEY change?)")
	}
	e.push.pub, e.push.priv, e.push.ready = pub, priv, true
	return pub, priv, nil
}

// storedVAPIDKeys reads the stored pair, generating it first when absent.
// The one-time write has its own timeout, never the caller's: a cancelled
// request must not abort a write every later caller depends on. The pair
// is read back, since a concurrent first boot may have won the race.
func (e *Engine) storedVAPIDKeys(ctx context.Context, env Env) (pub, sealed string, err error) {
	pub, sealed, err = vapidConfigRead(ctx, e.db)
	if err != nil || pub != "" && sealed != "" {
		return pub, sealed, err
	}
	newPriv, newPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}
	sealedNew, err := env.seal("vapid", newPriv)
	if err != nil {
		return "", "", err
	}
	writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := vapidConfigInsertIfAbsent(writeCtx, e.db, newPub, sealedNew); err != nil {
		return "", "", err
	}
	log.Printf("web push: generated the install's VAPID keypair")
	return vapidConfigRead(ctx, e.db)
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
	for _, kv := range [][2]string{{vapidPublicConfigKey, pub}, {vapidPrivateConfigKey, sealed}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_config(key,value) VALUES($1,$2) ON CONFLICT (key) DO NOTHING`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pushBudget spends one token from the site's and one from the
// recipient's daily push budget, or neither: the site's token is refunded
// when the recipient's is exhausted, so one exhausted person cannot starve
// everyone else on the site. These are refillable, process-local buckets,
// a rolling limit rather than a hard daily quota.
func (e *Engine) pushBudget(n model.Notification) bool {
	if !e.pushSiteDay.Allow(n.SiteID) {
		return false
	}
	if !e.pushPersonDay.Allow(n.OwnerID) {
		e.pushSiteDay.Refund(n.SiteID)
		return false
	}
	return true
}

// sendPush delivers body to one browser push subscription through the
// webhook client's SSRF-guarded dial; only the RFC 8291 encrypted payload
// and the RFC 8292 VAPID JWT leave the process. A muted or filtered body,
// or an account no longer allowed push, counts as delivered (an expected
// state, not a failure); a failed flag lookup or an exhausted budget is
// retried. A 404 or 410 means the browser dropped the subscription: it is
// deleted so nothing retries against a dead endpoint.
func (e *Engine) sendPush(ctx context.Context, env Env, n model.Notification, body []byte) Result {
	if bytes.Equal(body, pushSkip) {
		return Result{Delivered: true, Result: "filtered"}
	}
	allowed, err := e.PushAllowed(ctx, n.SiteID)
	switch {
	case err != nil:
		return Result{Result: "flag_lookup_failed"}
	case !allowed:
		return Result{Delivered: true, Result: "flag_disabled"}
	case !e.pushBudget(n):
		return Result{Result: "push_rate_limited"}
	}
	pub, priv, err := e.VAPIDKeys(ctx, env)
	if err != nil || pub == "" || priv == "" {
		return Result{Result: "push_not_configured"}
	}
	if _, err := e.client.validate(ctx, n.URL); err != nil {
		return Result{Result: Category(err)}
	}
	ctx, cancel := context.WithTimeout(ctx, e.client.deadline())
	defer cancel()
	httpClient, closeIdle := e.client.httpClient()
	defer closeIdle()
	sub := &webpush.Subscription{Endpoint: n.URL, Keys: webpush.Keys{P256dh: n.PushP256dh, Auth: n.PushAuth}}
	resp, err := webpush.SendNotificationWithContext(ctx, body, sub, &webpush.Options{
		HTTPClient: httpClient, Subscriber: "https://" + e.siteDomain + "/",
		TTL: 300, Urgency: webpush.UrgencyNormal, VAPIDPublicKey: pub, VAPIDPrivateKey: priv,
	})
	if err != nil {
		return Result{Result: Category(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if _, err := repo.DeleteNotification(ctx, e.db, n.ID); err != nil {
			log.Printf("web push: delete unregistered subscription %s: %v", n.ID, err)
		}
		return Result{Status: resp.StatusCode, Result: "unregistered"}
	}
	return statusResult(resp.StatusCode)
}

// decodePushKey decodes a browser-supplied subscription key in any base64
// flavour PushSubscription.getKey() might produce.
func decodePushKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

// validPushKeys: p256dh must be a point on P-256 (RFC 8291) and auth the
// 16-byte authentication secret. A subscription failing this could never
// be sent to, so saving it would only spend the site's cap.
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

// PushFamily is a visitor session's push-cleanup key: its SSO pass hash
// when it is part of a signed-in-everywhere family (one sign-out reaches
// every site the family spans), else the session's own id (a sign-out
// reaches only its own browser's devices).
func PushFamily(sess model.VisitorSession) string {
	if sess.SSOPassHash.Valid && sess.SSOPassHash.String != "" {
		return sess.SSOPassHash.String
	}
	return hex.EncodeToString(sess.ID)
}
