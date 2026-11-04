package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
)

// OAuthIdentity is a provider account linked to a users row. The durable key
// is (provider, provider_user_id); email is a snapshot, not a uniqueness key.
type OAuthIdentity = model.OAuthIdentity

// OAuthState is one in-flight authorization-code flow.
type OAuthState = model.OAuthState

// VisitorSession is the server-side row behind the site-scoped cookie.
// UserID is the one principal; the cookie is not an API key.
type VisitorSession = model.VisitorSession

// EstablishToken is the one-time bounce from the apex callback onto the host
// that will Set-Cookie.
type EstablishToken = model.EstablishToken

// InsertOAuthIdentity links a provider account to a users row.
func InsertOAuthIdentity(ctx context.Context, q Querier, userID, provider, providerUserID, email string, emailVerified bool) (OAuthIdentity, error) {
	return accounts.InsertOAuthIdentity(ctx, q, userID, provider, providerUserID, email, emailVerified)
}

// InsertVisitorSession stores a new site-scoped session. id is 32 raw bytes.
// It reports whether this is the person's first sign-in on the site (a new
// site_visitor_signins row), which the visitor.signed_up notification keys on.
func InsertVisitorSession(ctx context.Context, q Querier, id []byte, userID, siteID, host string, expiresAt, idleExpiresAt time.Time) (bool, error) {
	return accounts.InsertVisitorSession(ctx, q, id, userID, siteID, host, expiresAt, idleExpiresAt)
}

// GetVisitorSession loads a session by raw id. When the session has a
// parent SSO pass, it answers only while that pass's parent key is still
// live (keyIdle, KEY_IDLE_EXPIRY_DAYS).
func GetVisitorSession(ctx context.Context, q Querier, id []byte, keyIdle time.Duration) (VisitorSession, error) {
	return accounts.GetVisitorSession(ctx, q, id, keyIdle)
}

// DeleteVisitorSession removes one session row. Missing rows are not an error.
func DeleteVisitorSession(ctx context.Context, q Querier, id []byte) error {
	return accounts.DeleteVisitorSession(ctx, q, id)
}

// InsertEstablishToken stores the one-time bounce token for Set-Cookie,
// bound to the nonce hash of the browser that started the sign-in.
func InsertEstablishToken(ctx context.Context, q Querier, once string, sessionID []byte, host, returnTo string, nonceHash sql.NullString, expiresAt time.Time) error {
	return accounts.InsertEstablishToken(ctx, q, once, sessionID, host, returnTo, nonceHash, expiresAt)
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
