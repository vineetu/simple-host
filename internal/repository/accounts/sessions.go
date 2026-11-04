package accounts

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

type SSOPass struct{ Hash, UserID, ParentKeyHash string }

func ReplaceSSOPass(ctx context.Context, database *sql.DB, hash, keyHash, oldHash string, ttl, idle, keyIdle time.Duration) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT k.key_hash FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key_hash=$1 AND `+liveSSOParent+` FOR UPDATE OF k`, keyHash, keyIdle.Seconds()).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE key_hash IN (SELECT parent_key_hash FROM sso_passes WHERE id_hash=$1) AND key_hash<>$2`, oldHash, keyHash); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sso_passes WHERE parent_key_hash=$1`, keyHash); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO sso_passes(id_hash,user_id,parent_key_hash,expires_at,idle_expires_at)
 SELECT $3,k.user_id,k.key_hash,LEAST(now()+make_interval(secs=>$4),k.expires_at), LEAST(now()+make_interval(secs=>$5),k.expires_at)
 FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key_hash=$1 AND `+liveSSOParent+` RETURNING id_hash`, keyHash, keyIdle.Seconds(), hash, ttl.Seconds(), idle.Seconds()).Scan(&id); err != nil {
		return err
	}
	return tx.Commit()
}

func GetSSOPass(ctx context.Context, q Querier, hash string, keyIdle time.Duration) (SSOPass, error) {
	var p SSOPass
	err := q.QueryRowContext(ctx, `SELECT p.id_hash,p.user_id::text,p.parent_key_hash FROM sso_passes p JOIN api_keys k ON k.key_hash=p.parent_key_hash JOIN users u ON u.id=p.user_id WHERE p.id_hash=$1 AND `+liveSSOPass, hash, keyIdle.Seconds()).Scan(&p.Hash, &p.UserID, &p.ParentKeyHash)
	return p, err
}

func RememberSSOConsent(ctx context.Context, q Querier, userID, siteID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO sso_consents(user_id,site_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, siteID)
	return err
}

func SweepSSO(ctx context.Context, q Querier) error {
	_, err := sweepSSOCount(ctx, q)
	return err
}

func InsertOAuthIdentity(ctx context.Context, q Querier, userID, provider, providerUserID, email string, emailVerified bool) (model.OAuthIdentity, error) {
	const query = `
		INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, user_id::text, provider, provider_user_id,
		          email, email_verified, created_at, updated_at`
	var emailArg any
	if email != "" {
		emailArg = email
	}
	var id model.OAuthIdentity
	err := q.QueryRowContext(ctx, query, userID, provider, providerUserID, emailArg, emailVerified).Scan(
		&id.ID, &id.UserID, &id.Provider, &id.ProviderUserID,
		&id.Email, &id.EmailVerified, &id.CreatedAt, &id.UpdatedAt,
	)
	return id, err
}

func InsertVisitorSession(ctx context.Context, q Querier, id []byte, userID, siteID, host string, expiresAt, idleExpiresAt time.Time) (bool, error) {
	// The same statement records that this person signed in on this site
	// (site_visitor_signins, kept after the session ends): the owner's
	// visitor_id-to-email lookup answers only for such people.
	var first bool
	err := q.QueryRowContext(ctx, `
		WITH s AS (
			INSERT INTO visitor_sessions (id, user_id, site_id, host, expires_at, idle_expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING site_id, user_id),
		recorded AS (
			INSERT INTO site_visitor_signins (site_id, user_id) SELECT site_id, user_id FROM s
			ON CONFLICT DO NOTHING RETURNING site_id)
		SELECT EXISTS(SELECT 1 FROM recorded)`,
		id, userID, siteID, host, expiresAt, idleExpiresAt).Scan(&first)
	return first, err
}

func DeleteVisitorSession(ctx context.Context, q Querier, id []byte) error {
	_, err := q.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE id = $1`, id)
	return err
}

func InsertEstablishToken(ctx context.Context, q Querier, once string, sessionID []byte, host, returnTo string, nonceHash sql.NullString, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO visitor_establish_tokens (once, session_id, host, return_to, nonce_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		once, sessionID, host, returnTo, nonceHash, expiresAt)
	return err
}

func ReplaceAPIKeys(ctx context.Context, q Querier, userID, newKey, name string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if err := EndVisitorSessions(ctx, q, userID); err != nil {
		return err
	}
	return AddAPIKey(ctx, q, userID, newKey, name)
}
