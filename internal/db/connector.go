package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// The connector's OAuth 2.1 authorization server keeps its state here. Every
// secret column holds a SHA-256 hex digest; the plaintext exists only in the
// response that hands it out. See db/migrations/oauth-connector.sql.

// OAuthClient is an app that registered itself (RFC 7591) or was inserted by
// the operator.
type OAuthClient struct {
	ClientID                string
	SecretHash              sql.NullString
	Name                    string
	RedirectURIs            []string
	TokenEndpointAuthMethod string
	CreatedAt               time.Time
}

// OAuthCode is one authorization code, as stored.
type OAuthCode struct {
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Scope         string
	Resource      string
	ExpiresAt     time.Time
	UsedAt        sql.NullTime
	GrantID       sql.NullString
}

// OAuthTokenGrant is a token row joined to the grant it belongs to.
type OAuthTokenGrant struct {
	Kind      string
	ExpiresAt time.Time
	UsedAt    sql.NullTime
	GrantID   string
	UserID    string
	ClientID  string
	Scope     string
	Resource  string
}

// OAuthConnection is one app a person has connected, for the "connected apps"
// list: every live grant for one client folded into a row.
type OAuthConnection struct {
	ClientID    string
	ClientName  string
	ConnectedAt time.Time
	LastUsedAt  time.Time
}

// ErrOAuthCodeUsed is returned when a code has already been redeemed. The
// caller revokes whatever the first redemption issued.
var ErrOAuthCodeUsed = errors.New("authorization code already used")

func InsertOAuthClient(ctx context.Context, q Querier, c OAuthClient) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO oauth_clients (client_id, client_secret_hash, client_name, redirect_uris, token_endpoint_auth_method)
		VALUES ($1, $2, $3, $4, $5)`,
		c.ClientID, c.SecretHash, c.Name, pq.Array(c.RedirectURIs), c.TokenEndpointAuthMethod)
	return err
}

func GetOAuthClient(ctx context.Context, q Querier, clientID string) (OAuthClient, error) {
	var c OAuthClient
	err := q.QueryRowContext(ctx, `
		SELECT client_id, client_secret_hash, client_name, redirect_uris, token_endpoint_auth_method, created_at
		  FROM oauth_clients WHERE client_id = $1`, clientID).
		Scan(&c.ClientID, &c.SecretHash, &c.Name, pq.Array(&c.RedirectURIs), &c.TokenEndpointAuthMethod, &c.CreatedAt)
	return c, err
}

// TouchOAuthClient records use, at most once a minute, so the sweep can tell
// an abandoned registration from a working one.
func TouchOAuthClient(ctx context.Context, q Querier, clientID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE oauth_clients SET last_used_at = now()
		 WHERE client_id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, clientID)
	return err
}

func InsertOAuthCode(ctx context.Context, q Querier, codeHash string, c OAuthCode) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		codeHash, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, c.Scope, c.Resource, c.ExpiresAt)
	return err
}

// ConsumeOAuthCode marks a code used and returns it, in one statement, so two
// concurrent redemptions cannot both succeed. A code that exists but was
// already used returns ErrOAuthCodeUsed together with the stored row (whose
// GrantID says what to revoke); an unknown code returns sql.ErrNoRows.
func ConsumeOAuthCode(ctx context.Context, q Querier, codeHash string) (OAuthCode, error) {
	var c OAuthCode
	err := q.QueryRowContext(ctx, `
		UPDATE oauth_codes SET used_at = now()
		 WHERE code_hash = $1 AND used_at IS NULL
		RETURNING client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at, used_at, grant_id`, codeHash).
		Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.Scope, &c.Resource, &c.ExpiresAt, &c.UsedAt, &c.GrantID)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	err = q.QueryRowContext(ctx, `
		SELECT client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at, used_at, grant_id
		  FROM oauth_codes WHERE code_hash = $1`, codeHash).
		Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.Scope, &c.Resource, &c.ExpiresAt, &c.UsedAt, &c.GrantID)
	if err != nil {
		return c, err
	}
	return c, ErrOAuthCodeUsed
}

// SetOAuthCodeGrant records which grant a redeemed code produced.
func SetOAuthCodeGrant(ctx context.Context, q Querier, codeHash, grantID string) error {
	_, err := q.ExecContext(ctx, `UPDATE oauth_codes SET grant_id = $2 WHERE code_hash = $1`, codeHash, grantID)
	return err
}

func InsertOAuthGrant(ctx context.Context, q Querier, userID, clientID, scope, resource string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		INSERT INTO oauth_grants (user_id, client_id, scope, resource)
		VALUES ($1, $2, $3, $4) RETURNING id`, userID, clientID, scope, resource).Scan(&id)
	return id, err
}

func InsertOAuthToken(ctx context.Context, q Querier, tokenHash, grantID, kind string, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, grantID, kind, expiresAt)
	return err
}

// GetOAuthToken looks a token up by its hash, joined to its grant. A token
// whose grant was deleted is gone with it (ON DELETE CASCADE), so a row found
// here always belongs to a live grant.
func GetOAuthToken(ctx context.Context, q Querier, tokenHash string) (OAuthTokenGrant, error) {
	var t OAuthTokenGrant
	err := q.QueryRowContext(ctx, `
		SELECT t.kind, t.expires_at, t.used_at, g.id, g.user_id, g.client_id, g.scope, g.resource
		  FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id
		 WHERE t.token_hash = $1`, tokenHash).
		Scan(&t.Kind, &t.ExpiresAt, &t.UsedAt, &t.GrantID, &t.UserID, &t.ClientID, &t.Scope, &t.Resource)
	return t, err
}

// MarkRefreshTokenUsed rotates a refresh token out. It reports false when the
// token had already been used, which is reuse: the caller revokes the family.
func MarkRefreshTokenUsed(ctx context.Context, q Querier, tokenHash string) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE oauth_tokens SET used_at = now()
		 WHERE token_hash = $1 AND kind = 'refresh' AND used_at IS NULL`, tokenHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// TouchOAuthGrant records that a grant was used, at most once a minute.
func TouchOAuthGrant(ctx context.Context, q Querier, grantID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE oauth_grants SET last_used_at = now()
		 WHERE id = $1 AND last_used_at < now() - interval '1 minute'`, grantID)
	return err
}

// DeleteOAuthGrant revokes one grant and, by cascade, every token it issued.
func DeleteOAuthGrant(ctx context.Context, q Querier, grantID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM oauth_grants WHERE id = $1`, grantID)
	return err
}

// DeleteOAuthToken revokes a single token.
func DeleteOAuthToken(ctx context.Context, q Querier, tokenHash string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteOAuthGrantsForClient disconnects one app from one person. It reports
// how many grants were removed, so "not connected" can be told from success.
func DeleteOAuthGrantsForClient(ctx context.Context, q Querier, userID, clientID string) (int64, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM oauth_grants WHERE user_id = $1 AND client_id = $2`, userID, clientID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteOAuthGrantsForUser disconnects every app a person connected. Used when
// they rotate their API key: whoever held the old key could have connected an
// app, and that connection must not outlive the key.
func DeleteOAuthGrantsForUser(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM oauth_grants WHERE user_id = $1`, userID)
	return err
}

// ListOAuthConnections returns the apps a person has connected, newest first.
func ListOAuthConnections(ctx context.Context, database *sql.DB, userID string) ([]OAuthConnection, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT g.client_id, c.client_name, MIN(g.created_at), MAX(g.last_used_at)
		  FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id
		 WHERE g.user_id = $1
		 GROUP BY g.client_id, c.client_name
		 ORDER BY MIN(g.created_at) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthConnection{}
	for rows.Next() {
		var c OAuthConnection
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.ConnectedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SweepOAuth removes what can no longer be used: expired codes (kept a day so
// a replay is still recognised as one), expired tokens, grants with no token
// left, and registrations that never led to a connection.
func SweepOAuth(ctx context.Context, database *sql.DB) error {
	for _, stmt := range []string{
		`DELETE FROM oauth_codes WHERE expires_at < now() - interval '1 day'`,
		`DELETE FROM oauth_tokens WHERE expires_at < now() - interval '1 day'`,
		`DELETE FROM oauth_grants g WHERE g.created_at < now() - interval '1 day'
		   AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id)`,
		`DELETE FROM oauth_clients c WHERE c.created_at < now() - interval '30 days'
		   AND COALESCE(c.last_used_at, c.created_at) < now() - interval '30 days'
		   AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.client_id)`,
	} {
		if _, err := database.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
