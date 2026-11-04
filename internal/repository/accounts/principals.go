package accounts

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	authrules "github.com/vsriram/simple-host/internal/rules/auth"
)

// GetLatestOAuthIdentity returns the most recently created identity with a
// provider-verified email for a user.
func GetLatestOAuthIdentity(ctx context.Context, q Querier, userID string) (model.OAuthIdentity, error) {
	const query = `SELECT provider, email FROM oauth_identities
 WHERE user_id = $1 AND email_verified ORDER BY created_at DESC LIMIT 1`
	var identity model.OAuthIdentity
	err := q.QueryRowContext(ctx, query, userID).Scan(&identity.Provider, &identity.Email)
	return identity, err
}

// GetVisitorSession loads a session by raw id. When the session has a
// parent SSO pass, it answers only while that pass's parent key is still
// live (keyIdle, KEY_IDLE_EXPIRY_DAYS).
func GetVisitorSession(ctx context.Context, q Querier, id []byte, keyIdle time.Duration) (model.VisitorSession, error) {
	const query = `
		SELECT v.id, v.user_id::text, v.site_id::text, v.host, v.created_at, v.last_seen_at, v.expires_at, v.idle_expires_at, v.sso_pass_hash
 FROM visitor_sessions v WHERE v.id = $1 AND (v.sso_pass_hash IS NULL OR EXISTS (
 SELECT 1 FROM sso_passes p JOIN api_keys k ON k.key_hash=p.parent_key_hash JOIN users u ON u.id=p.user_id
 WHERE p.id_hash=v.sso_pass_hash AND ` + liveSSOPass + `))`
	var s model.VisitorSession
	err := q.QueryRowContext(ctx, query, id, keyIdle.Seconds()).Scan(
		&s.ID, &s.UserID, &s.SiteID, &s.Host,
		&s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.IdleExpiresAt, &s.SSOPassHash,
	)
	return s, err
}

// APIKeyScope is the scope of a stored key that still works, or
// sql.ErrNoRows for a key that is not stored (unknown, the env admin key, an
// internal credential) or has expired: an expired key is passed on so the key
// middleware can say it expired rather than that it is deploy-only.
func APIKeyScope(ctx context.Context, q Querier, apiKey string, keyIdle time.Duration) (string, error) {
	var (
		scope     string
		expiresAt *time.Time
		idleFrom  time.Time
		lastUsed  *time.Time
		now       time.Time
	)
	err := q.QueryRowContext(ctx,
		`SELECT scope, expires_at, idle_from, last_used_at, now() FROM api_keys WHERE key_hash = $1`,
		HashAPIKey(apiKey)).Scan(&scope, &expiresAt, &idleFrom, &lastUsed, &now)
	if err != nil {
		return "", err
	}
	if authrules.KeyExpired(now, expiresAt, idleFrom, lastUsed, keyIdle) != "" {
		return "", sql.ErrNoRows
	}
	return scope, nil
}

func TouchAPIKey(ctx context.Context, q Querier, keyHash string) {
	if keyHash == "" {
		return
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE api_keys SET last_used_at = now()
		 WHERE key_hash = $1 AND (last_used_at IS NULL OR last_used_at < now() - make_interval(secs => $2))`,
		keyHash, touchAPIKeyInterval.Seconds()); err != nil {
		log.Printf("api key: touch last_used_at: %v", err)
	}
}

const touchAPIKeyInterval = 5 * time.Minute
const liveSSOParent = `k.scope = 'full' AND u.suspended_at IS NULL
 AND (k.expires_at IS NULL OR k.expires_at > now())
 AND ($2::float8 <= 0 OR GREATEST(k.idle_from, COALESCE(k.last_used_at, k.idle_from)) + make_interval(secs => $2::float8) > now())`
const liveSSOPass = `p.expires_at > now() AND p.idle_expires_at > now() AND ` + liveSSOParent

func KeyExpired(now time.Time, expiresAt *time.Time, idleFrom time.Time, lastUsed *time.Time, keyIdle time.Duration) string {
	return authrules.KeyExpired(now, expiresAt, idleFrom, lastUsed, keyIdle)
}
func IdleExpiresAt(idleFrom time.Time, lastUsed *time.Time, keyIdle time.Duration) *time.Time {
	return authrules.IdleExpiresAt(idleFrom, lastUsed, keyIdle)
}
