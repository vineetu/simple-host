package db

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

// OAuthIdentity is a provider account linked to a users row. The durable key
// is (provider, provider_user_id); email is a snapshot, not a uniqueness key.
type OAuthIdentity = model.OAuthIdentity

// OAuthState is one in-flight authorization-code flow.
type OAuthState struct {
	State        string
	Provider     string
	CodeVerifier string
	ReturnTo     string
	Host         string
	SiteID       sql.NullString
	Purpose      string
	NonceHash    sql.NullString
	CreatedAt    time.Time
	ExpiresAt    time.Time
	UsedAt       sql.NullTime
}

// VisitorSession is the server-side row behind the site-scoped cookie.
// UserID is the one principal; the cookie is not an API key.
type VisitorSession = model.VisitorSession

// EstablishToken is the one-time bounce from the apex callback onto the host
// that will Set-Cookie.
type EstablishToken struct {
	Once      string
	SessionID []byte
	Host      string
	ReturnTo  string
	NonceHash sql.NullString
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    sql.NullTime
}

// GetOAuthIdentity looks up a linked provider account.
// Returns sql.ErrNoRows when none exists.
func GetOAuthIdentity(ctx context.Context, q Querier, provider, providerUserID string) (OAuthIdentity, error) {
	const query = `
		SELECT id::text, user_id::text, provider, provider_user_id,
		       email, email_verified, created_at, updated_at
		FROM oauth_identities
		WHERE provider = $1 AND provider_user_id = $2`
	var id OAuthIdentity
	err := q.QueryRowContext(ctx, query, provider, providerUserID).Scan(
		&id.ID, &id.UserID, &id.Provider, &id.ProviderUserID,
		&id.Email, &id.EmailVerified, &id.CreatedAt, &id.UpdatedAt,
	)
	return id, err
}

// GetLatestOAuthIdentity returns the most recently created identity with a
// provider-verified email for a user.
func GetLatestOAuthIdentity(ctx context.Context, q Querier, userID string) (OAuthIdentity, error) {
	return accounts.GetLatestOAuthIdentity(ctx, q, userID)
}

// InsertOAuthIdentity links a provider account to a users row.
func InsertOAuthIdentity(ctx context.Context, q Querier, userID, provider, providerUserID, email string, emailVerified bool) (OAuthIdentity, error) {
	const query = `
		INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, user_id::text, provider, provider_user_id,
		          email, email_verified, created_at, updated_at`
	var emailArg any
	if email != "" {
		emailArg = email
	}
	var id OAuthIdentity
	err := q.QueryRowContext(ctx, query, userID, provider, providerUserID, emailArg, emailVerified).Scan(
		&id.ID, &id.UserID, &id.Provider, &id.ProviderUserID,
		&id.Email, &id.EmailVerified, &id.CreatedAt, &id.UpdatedAt,
	)
	return id, err
}

// TouchOAuthIdentity updates the email snapshot on a successful re-identify.
func TouchOAuthIdentity(ctx context.Context, q Querier, id, email string, emailVerified bool) error {
	var emailArg any
	if email != "" {
		emailArg = email
	}
	_, err := q.ExecContext(ctx, `
		UPDATE oauth_identities
		SET email = $2, email_verified = $3, updated_at = now()
		WHERE id = $1`, id, emailArg, emailVerified)
	return err
}

// InsertOAuthState stores a new authorization-code flow.
// purpose is "site" (siteID valid, host non-empty) or "owner" (siteID null).
// nonceHash binds a site sign-in to the browser that started it.
func InsertOAuthState(ctx context.Context, q Querier, state, provider, verifier, returnTo, host string, siteID sql.NullString, purpose string, nonceHash sql.NullString, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO oauth_states (state, provider, code_verifier, return_to, host, site_id, purpose, nonce_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		state, provider, verifier, returnTo, host, siteID, purpose, nonceHash, expiresAt)
	return err
}

// ConsumeOAuthState marks a state used iff it is unused and unexpired.
// Returns sql.ErrNoRows when the state is missing, used, or expired.
func ConsumeOAuthState(ctx context.Context, q Querier, state string) (OAuthState, error) {
	const query = `
		UPDATE oauth_states
		SET used_at = now()
		WHERE state = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING state, provider, code_verifier, return_to, host, site_id,
		          purpose, nonce_hash, created_at, expires_at, used_at`
	var s OAuthState
	err := q.QueryRowContext(ctx, query, state).Scan(
		&s.State, &s.Provider, &s.CodeVerifier, &s.ReturnTo, &s.Host, &s.SiteID,
		&s.Purpose, &s.NonceHash, &s.CreatedAt, &s.ExpiresAt, &s.UsedAt,
	)
	return s, err
}

// PruneExpiredOAuthStates deletes expired authorization-code rows.
func PruneExpiredOAuthStates(ctx context.Context, q Querier) error {
	_, err := q.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at < now()`)
	return err
}

// InsertVisitorSession stores a new site-scoped session. id is 32 raw bytes.
// It reports whether this is the person's first sign-in on the site (a new
// site_visitor_signins row), which the visitor.signed_up notification keys on.
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

// Pending handoffs have no durable identity record and expire with their code.
func InsertPendingVisitorSession(ctx context.Context, q Querier, id []byte, userID, siteID, host string, expires time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO visitor_sessions(id,user_id,site_id,host,expires_at,idle_expires_at) VALUES($1,$2,$3,$4,$5,$5)`, id, userID, siteID, host, expires)
	return err
}

// ActivateVisitorSession turns a pending handoff into a live session and
// reports whether this is the person's first sign-in on the site. A missing
// or expired handoff is sql.ErrNoRows.
func ActivateVisitorSession(ctx context.Context, q Querier, id []byte, ttl, idle time.Duration) (bool, error) {
	var first bool
	err := q.QueryRowContext(ctx, `WITH s AS (
 UPDATE visitor_sessions SET expires_at=now()+make_interval(secs=>$2),idle_expires_at=now()+make_interval(secs=>$3)
 WHERE id=$1 AND expires_at>now() AND idle_expires_at>now() RETURNING site_id,user_id),
 recorded AS (INSERT INTO site_visitor_signins(site_id,user_id) SELECT site_id,user_id FROM s ON CONFLICT DO NOTHING RETURNING site_id)
 SELECT EXISTS(SELECT 1 FROM recorded) FROM s`, id, ttl.Seconds(), idle.Seconds()).Scan(&first)
	return first, err
}

// GetVisitorSession loads a session by raw id. When the session has a
// parent SSO pass, it answers only while that pass's parent key is still
// live (keyIdle, KEY_IDLE_EXPIRY_DAYS).
func GetVisitorSession(ctx context.Context, q Querier, id []byte, keyIdle time.Duration) (VisitorSession, error) {
	return accounts.GetVisitorSession(ctx, q, id, keyIdle)
}

// DeleteVisitorSession removes one session row. Missing rows are not an error.
func DeleteVisitorSession(ctx context.Context, q Querier, id []byte) error {
	_, err := q.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE id = $1`, id)
	return err
}

// InsertEstablishToken stores the one-time bounce token for Set-Cookie,
// bound to the nonce hash of the browser that started the sign-in.
func InsertEstablishToken(ctx context.Context, q Querier, once string, sessionID []byte, host, returnTo string, nonceHash sql.NullString, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO visitor_establish_tokens (once, session_id, host, return_to, nonce_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		once, sessionID, host, returnTo, nonceHash, expiresAt)
	return err
}

// ConsumeEstablishToken marks a token used iff it is unused and unexpired,
// whatever host it is presented on, then checks the bound host. A token
// presented on the wrong host is burned and rejected, so it cannot be replayed
// on the right one. Returns sql.ErrNoRows on any failure.
func ConsumeEstablishToken(ctx context.Context, q Querier, once, host string) (EstablishToken, error) {
	const query = `
		UPDATE visitor_establish_tokens
		SET used_at = now()
		WHERE once = $1
		  AND used_at IS NULL
		  AND expires_at > now()
		RETURNING once, session_id, host, return_to, nonce_hash, created_at, expires_at, used_at`
	var t EstablishToken
	err := q.QueryRowContext(ctx, query, once).Scan(
		&t.Once, &t.SessionID, &t.Host, &t.ReturnTo, &t.NonceHash, &t.CreatedAt, &t.ExpiresAt, &t.UsedAt,
	)
	if err != nil {
		return EstablishToken{}, err
	}
	if !strings.EqualFold(t.Host, host) {
		_ = DeleteVisitorSession(ctx, q, t.SessionID)
		return EstablishToken{}, sql.ErrNoRows
	}
	return t, nil
}

// GetSiteWriteGate returns the owning user_id and the admin override flag
// used by visitorWriteOK.
func GetSiteWriteGate(ctx context.Context, q Querier, siteID string) (userID string, allowAnonymous bool, err error) {
	return sites.GetSiteWriteGate(ctx, q, siteID)
}

// SetAllowAnonymousWrites is the admin-only escape hatch on a site.
func SetAllowAnonymousWrites(ctx context.Context, q Querier, siteID string, allow bool) error {
	return sites.SetAllowAnonymousWrites(ctx, q, siteID, allow)
}

// SweepVisitorAuth deletes expired OAuth states, establish tokens, sessions,
// and email sign-in codes (a day past expiry). oauth_identities is durable and
// is not swept.
func SweepVisitorAuth(ctx context.Context, database *sql.DB) error {
	_, err := SweepVisitorAuthCount(ctx, database)
	return err
}

// SweepVisitorAuthCount reports completed deletes, including work before a later error.
func SweepVisitorAuthCount(ctx context.Context, database *sql.DB) (int64, error) {
	return accounts.SweepVisitorAuthCount(ctx, database)
}
