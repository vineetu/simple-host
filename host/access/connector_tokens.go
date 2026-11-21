package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/vsriram/simple-host/host/access/repo"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
	"log"
	"strings"
	"time"
)

type Connector struct {
	database              *sql.DB
	Now                   func() time.Time
	NormalizeResource     func(string) (string, bool)
	TeamConnections       bool
	AccessTTL, RefreshTTL time.Duration
}

func NewConnector(database *sql.DB, now func() time.Time, normalize func(string) (string, bool), teams bool, access, refresh time.Duration) *Connector {
	return &Connector{database, now, normalize, teams, access, refresh}
}

type ConnectorTokenInput struct{ Code, RedirectURI, Verifier, Resource, RefreshToken, Scope string }
type ConnectorError struct {
	Code, Description string
	Internal          bool
}

func (e *ConnectorError) Error() string { return e.Code + ": " + e.Description }

type ConnectorTokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

const (
	prefixCode    = "shac_"
	prefixRefresh = "shrt_"
	prefixAccess  = "shat_"
)

func randomToken(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("connector: no randomness: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// hashSecret is how every connector secret is stored and looked up. The
// secrets are 256-bit random values, so a fast hash is the right one: there is
// nothing to brute-force.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
func validPKCEValue(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}

// pkceS256 reports whether verifier matches challenge, in constant time.
func pkceS256(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// RedeemCode exchanges an authorization code for tokens in a new grant. The
// code is spent once found, whatever happens next: a failed check commits
// the spend, so a failed guess cannot be retried.
func (h *Connector) RedeemCode(ctx context.Context, in ConnectorTokenInput, client model.OAuthClient) (ConnectorTokens, error) {
	if in.Code == "" || !strings.HasPrefix(in.Code, prefixCode) {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "unknown code", Internal: false}
	}
	codeHash := hashSecret(in.Code)
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return ConnectorTokens{}, connectorServerError()
	}
	defer tx.Rollback()
	stored, err := h.consumeCode(ctx, tx, codeHash)
	if err != nil {
		return ConnectorTokens{}, err
	}
	if err := h.checkRedemption(ctx, tx, stored, in, client); err != nil {
		_ = tx.Commit()
		return ConnectorTokens{}, err
	}
	grantID, err := repo.InsertOAuthGrant(ctx, tx, stored.UserID, client.ClientID, stored.Scope, stored.Resource, stored.Device)
	if err != nil {
		log.Printf("connector: insert grant: %v", err)
		return ConnectorTokens{}, connectorServerError()
	}
	if err := repo.SetOAuthCodeGrant(ctx, tx, codeHash, grantID); err != nil {
		return ConnectorTokens{}, connectorServerError()
	}
	return h.issueTokens(ctx, tx, grantID, stored.Scope)
}

func connectorServerError() error {
	return &ConnectorError{Code: "server_error", Description: "", Internal: true}
}

func invalidGrant(desc string) error {
	return &ConnectorError{Code: "invalid_grant", Description: desc, Internal: false}
}

// consumeCode spends the code. A code presented twice was intercepted or
// replayed: whatever the first redemption issued is revoked (RFC 6749
// §4.1.2).
func (h *Connector) consumeCode(ctx context.Context, tx *sql.Tx, codeHash string) (model.OAuthCode, error) {
	stored, err := repo.ConsumeOAuthCode(ctx, tx, codeHash)
	if errors.Is(err, model.ErrOAuthCodeUsed) {
		_ = tx.Rollback()
		if stored.GrantID.Valid {
			if derr := repo.DeleteOAuthGrant(ctx, h.database, stored.GrantID.String); derr != nil {
				log.Printf("connector: revoke on code reuse: %v", derr)
			}
		}
		log.Printf("connector: authorization code reused client_id=%s", stored.ClientID)
		return stored, invalidGrant("code already used")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("connector: consume code: %v", err)
		return stored, connectorServerError()
	}
	if err != nil {
		return stored, invalidGrant("unknown code")
	}
	return stored, nil
}

// checkRedemption: the code is this client's, unexpired, for this redirect
// URI, its PKCE verifier matches (and none is sent for a code issued
// without a challenge: a downgrade attempt, RFC 9700 §2.1.1), the resource
// is the one authorized, and the account may still connect.
func (h *Connector) checkRedemption(ctx context.Context, tx *sql.Tx, stored model.OAuthCode, in ConnectorTokenInput, client model.OAuthClient) error {
	switch {
	case stored.ClientID != client.ClientID:
		return invalidGrant("code was issued to another client")
	case h.Now().After(stored.ExpiresAt):
		return invalidGrant("code expired")
	case in.RedirectURI != stored.RedirectURI:
		return invalidGrant("redirect_uri does not match the authorization request")
	case stored.CodeChallenge != "" && (!validPKCEValue(in.Verifier) || !pkceS256(in.Verifier, stored.CodeChallenge)):
		return invalidGrant("PKCE verification failed")
	case stored.CodeChallenge == "" && in.Verifier != "":
		return invalidGrant("code_verifier sent for a code issued without code_challenge")
	}
	if res := in.Resource; res != "" {
		if norm, ok := h.NormalizeResource(res); !ok || norm != stored.Resource {
			return &ConnectorError{Code: "invalid_target", Description: "resource does not match the authorization request", Internal: false}
		}
	}
	user, err := repo.GetUserByID(ctx, tx, stored.UserID)
	if err != nil || user.Username == "admin" || user.Suspended {
		return invalidGrant("account unavailable")
	}
	return nil
}

// Refresh rotates a refresh token. A refusal before the token is spent
// rolls back, so it can be used again; a rotated-out token presented again
// revokes the whole connection.
func (h *Connector) Refresh(ctx context.Context, in ConnectorTokenInput, client model.OAuthClient) (ConnectorTokens, error) {
	if !strings.HasPrefix(in.RefreshToken, prefixRefresh) {
		return ConnectorTokens{}, invalidGrant("unknown refresh token")
	}
	tokenHash := hashSecret(in.RefreshToken)
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return ConnectorTokens{}, connectorServerError()
	}
	defer tx.Rollback()
	tok, err := repo.GetOAuthToken(ctx, tx, tokenHash)
	if err != nil || tok.Kind != "refresh" {
		return ConnectorTokens{}, invalidGrant("unknown refresh token")
	}
	if err := h.checkRefreshToken(tok, client); err != nil {
		return ConnectorTokens{}, err
	}
	if err := h.spendRefreshToken(ctx, tx, tok, tokenHash); err != nil {
		return ConnectorTokens{}, err
	}
	if err := h.checkRefreshRequest(ctx, tx, tok, in); err != nil {
		return ConnectorTokens{}, err
	}
	_ = repo.TouchOAuthGrant(ctx, tx, tok.GrantID)
	return h.issueTokens(ctx, tx, tok.GrantID, tok.Scope)
}

// checkRefreshToken: the token is this client's and unexpired, and on a
// team-connections install only a Manage-my-events grant refreshes (the
// person reconnects and chooses a team).
func (h *Connector) checkRefreshToken(tok model.OAuthTokenGrant, client model.OAuthClient) error {
	switch {
	case tok.ClientID != client.ClientID:
		return invalidGrant("refresh token was issued to another client")
	case h.Now().After(tok.ExpiresAt):
		return invalidGrant("refresh token expired")
	case h.TeamConnections && !authrules.EventsGrant(tok.Scope):
		return invalidGrant("this connection cannot be refreshed; reconnect and choose a team or Manage my events")
	}
	return nil
}

// spendRefreshToken marks the token used. A rotated-out refresh token came
// back: either the client or a thief holds a copy. The whole family goes,
// so the thief's copy dies too (OAuth 2.1 §4.3.1). The person reconnects
// once.
func (h *Connector) spendRefreshToken(ctx context.Context, tx *sql.Tx, tok model.OAuthTokenGrant, tokenHash string) error {
	fresh, err := repo.MarkRefreshTokenUsed(ctx, tx, tokenHash)
	if err != nil {
		return connectorServerError()
	}
	if fresh {
		return nil
	}
	_ = tx.Rollback()
	if derr := repo.DeleteOAuthGrant(ctx, h.database, tok.GrantID); derr != nil {
		log.Printf("connector: revoke on refresh reuse: %v", derr)
	}
	log.Printf("connector: refresh token reused client_id=%s user_id=%s; connection revoked", tok.ClientID, tok.UserID)
	return invalidGrant("refresh token already used; the connection was revoked")
}

// checkRefreshRequest: the refresh asks for no more scope and no other
// resource than the grant, and the account may still connect. A suspended
// account's refresh is refused without being spent (the transaction rolls
// back), so it works again once the account is re-enabled.
func (h *Connector) checkRefreshRequest(ctx context.Context, tx *sql.Tx, tok model.OAuthTokenGrant, in ConnectorTokenInput) error {
	if !scopeWithin(in.Scope, tok.Scope) {
		return &ConnectorError{Code: "invalid_scope", Description: "scope exceeds the original grant", Internal: false}
	}
	if res := in.Resource; res != "" {
		if norm, ok := h.NormalizeResource(res); !ok || norm != tok.Resource {
			return &ConnectorError{Code: "invalid_target", Description: "resource does not match the original grant", Internal: false}
		}
	}
	user, err := repo.GetUserByID(ctx, tx, tok.UserID)
	if err != nil || user.Username == "admin" || user.Suspended {
		return invalidGrant("account unavailable")
	}
	return nil
}

// scopeWithin: every scope asked for ("" asks for none) was granted.
func scopeWithin(asked, granted string) bool {
	have := map[string]bool{}
	for _, g := range strings.Fields(granted) {
		have[g] = true
	}
	for _, s := range strings.Fields(asked) {
		if !have[s] {
			return false
		}
	}
	return true
}

// issueTokens mints an access token and a refresh token in grantID, commits
// the transaction, and writes the token response.
func (h *Connector) issueTokens(ctx context.Context, tx *sql.Tx, grantID, scope string) (ConnectorTokens, error) {
	access := randomToken(prefixAccess, 32)
	refresh := randomToken(prefixRefresh, 32)
	now := h.Now()
	if err := repo.InsertOAuthToken(ctx, tx, hashSecret(access), grantID, "access", now.Add(h.AccessTTL)); err != nil {
		log.Printf("connector: insert access token: %v", err)
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Internal: true}
	}
	if err := repo.InsertOAuthToken(ctx, tx, hashSecret(refresh), grantID, "refresh", now.Add(h.RefreshTTL)); err != nil {
		log.Printf("connector: insert refresh token: %v", err)
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Internal: true}
	}
	if err := tx.Commit(); err != nil {
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Internal: true}
	}
	return ConnectorTokens{access, "Bearer", int(h.AccessTTL / time.Second), refresh, scope}, nil
}
