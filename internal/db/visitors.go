package db

import (
	"context"
	"database/sql"
	"time"
)

// Visitor is a durable identity from Google or GitHub. Not a users row.
type Visitor struct {
	ID             string
	Provider       string
	ProviderUserID string
	CreatedAt      time.Time
	LastLoginAt    time.Time
}

// OAuthState is one in-flight authorization-code flow.
type OAuthState struct {
	State        string
	Provider     string
	CodeVerifier string
	ReturnTo     string
	Host         string
	SiteID       string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	UsedAt       sql.NullTime
}

// VisitorSession is the server-side row behind the visitor cookie.
type VisitorSession struct {
	ID            []byte
	VisitorID     string
	SiteID        string
	Host          string
	CreatedAt     time.Time
	LastSeenAt    time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

// EstablishToken is the one-time bounce from the apex callback onto the host
// that will Set-Cookie.
type EstablishToken struct {
	Once      string
	SessionID []byte
	Host      string
	ReturnTo  string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    sql.NullTime
}

// UpsertVisitor inserts a visitor or bumps last_login_at on (provider, provider_user_id).
func UpsertVisitor(ctx context.Context, q Querier, provider, providerUserID string) (Visitor, error) {
	const query = `
		INSERT INTO visitors (provider, provider_user_id)
		VALUES ($1, $2)
		ON CONFLICT (provider, provider_user_id)
		DO UPDATE SET last_login_at = now()
		RETURNING id::text, provider, provider_user_id, created_at, last_login_at`
	var v Visitor
	err := q.QueryRowContext(ctx, query, provider, providerUserID).Scan(
		&v.ID, &v.Provider, &v.ProviderUserID, &v.CreatedAt, &v.LastLoginAt,
	)
	return v, err
}

// InsertOAuthState stores a new authorization-code flow.
func InsertOAuthState(ctx context.Context, q Querier, state, provider, verifier, returnTo, host, siteID string, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO oauth_states (state, provider, code_verifier, return_to, host, site_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		state, provider, verifier, returnTo, host, siteID, expiresAt)
	return err
}

// ConsumeOAuthState marks a state used iff it is unused and unexpired.
// Returns sql.ErrNoRows when the state is missing, used, or expired.
func ConsumeOAuthState(ctx context.Context, q Querier, state string) (OAuthState, error) {
	const query = `
		UPDATE oauth_states
		SET used_at = now()
		WHERE state = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING state, provider, code_verifier, return_to, host, site_id::text,
		          created_at, expires_at, used_at`
	var s OAuthState
	err := q.QueryRowContext(ctx, query, state).Scan(
		&s.State, &s.Provider, &s.CodeVerifier, &s.ReturnTo, &s.Host, &s.SiteID,
		&s.CreatedAt, &s.ExpiresAt, &s.UsedAt,
	)
	return s, err
}

// PruneExpiredOAuthStates deletes expired authorization-code rows.
func PruneExpiredOAuthStates(ctx context.Context, q Querier) error {
	_, err := q.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at < now()`)
	return err
}

// InsertVisitorSession stores a new site-scoped session. id is 32 raw bytes.
func InsertVisitorSession(ctx context.Context, q Querier, id []byte, visitorID, siteID, host string, expiresAt, idleExpiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO visitor_sessions (id, visitor_id, site_id, host, expires_at, idle_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, visitorID, siteID, host, expiresAt, idleExpiresAt)
	return err
}

// GetVisitorSession loads a session by raw id.
func GetVisitorSession(ctx context.Context, q Querier, id []byte) (VisitorSession, error) {
	const query = `
		SELECT id, visitor_id::text, site_id::text, host, created_at, last_seen_at, expires_at, idle_expires_at
		FROM visitor_sessions
		WHERE id = $1`
	var s VisitorSession
	err := q.QueryRowContext(ctx, query, id).Scan(
		&s.ID, &s.VisitorID, &s.SiteID, &s.Host,
		&s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.IdleExpiresAt,
	)
	return s, err
}

// TouchVisitorSession slides idle_expires_at to min(now+14d, expires_at).
func TouchVisitorSession(ctx context.Context, q Querier, id []byte) error {
	_, err := q.ExecContext(ctx, `
		UPDATE visitor_sessions
		SET last_seen_at = now(),
		    idle_expires_at = LEAST(now() + interval '14 days', expires_at)
		WHERE id = $1
		  AND expires_at > now()
		  AND idle_expires_at > now()`, id)
	return err
}

// DeleteVisitorSession removes one session row. Missing rows are not an error.
func DeleteVisitorSession(ctx context.Context, q Querier, id []byte) error {
	_, err := q.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE id = $1`, id)
	return err
}

// InsertEstablishToken stores the one-time bounce token for Set-Cookie.
func InsertEstablishToken(ctx context.Context, q Querier, once string, sessionID []byte, host, returnTo string, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO visitor_establish_tokens (once, session_id, host, return_to, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		once, sessionID, host, returnTo, expiresAt)
	return err
}

// ConsumeEstablishToken marks a token used iff it is unused, unexpired, and
// bound to host. Returns sql.ErrNoRows otherwise.
func ConsumeEstablishToken(ctx context.Context, q Querier, once, host string) (EstablishToken, error) {
	const query = `
		UPDATE visitor_establish_tokens
		SET used_at = now()
		WHERE once = $1
		  AND used_at IS NULL
		  AND expires_at > now()
		  AND lower(host) = lower($2)
		RETURNING once, session_id, host, return_to, created_at, expires_at, used_at`
	var t EstablishToken
	err := q.QueryRowContext(ctx, query, once, host).Scan(
		&t.Once, &t.SessionID, &t.Host, &t.ReturnTo, &t.CreatedAt, &t.ExpiresAt, &t.UsedAt,
	)
	return t, err
}

// GetSiteWriteGate returns the owning user_id and the admin override flag
// used by visitorWriteOK.
func GetSiteWriteGate(ctx context.Context, q Querier, siteID string) (userID string, allowAnonymous bool, err error) {
	err = q.QueryRowContext(ctx,
		`SELECT user_id::text, allow_anonymous_writes FROM sites WHERE id = $1`,
		siteID,
	).Scan(&userID, &allowAnonymous)
	return
}

// SetAllowAnonymousWrites is the admin-only escape hatch on a site.
func SetAllowAnonymousWrites(ctx context.Context, q Querier, siteID string, allow bool) error {
	_, err := q.ExecContext(ctx,
		`UPDATE sites SET allow_anonymous_writes = $1 WHERE id = $2`,
		allow, siteID)
	return err
}

// SweepVisitorAuth deletes expired OAuth states, establish tokens, and sessions.
func SweepVisitorAuth(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at < now()`); err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM visitor_establish_tokens WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := database.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE expires_at < now() OR idle_expires_at < now()`)
	return err
}
