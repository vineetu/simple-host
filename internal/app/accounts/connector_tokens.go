package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	authrules "github.com/vsriram/simple-host/internal/rules/auth"
	"log"
	"strings"
	"time"
)

type Connector struct {
	database              *sql.DB
	Now                   func() time.Time
	NormalizeResource     func(string) (string, bool)
	HackTeams             bool
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
func (h *Connector) RedeemCode(ctx context.Context, in ConnectorTokenInput, client model.OAuthClient) (ConnectorTokens, error) {
	code := in.Code
	if code == "" || !strings.HasPrefix(code, prefixCode) {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "unknown code", Internal: false}
	}
	codeHash := hashSecret(code)
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Description: "", Internal: true}
	}
	defer tx.Rollback()

	stored, err := repo.ConsumeOAuthCode(ctx, tx, codeHash)
	if errors.Is(err, model.ErrOAuthCodeUsed) {
		// A code presented twice was intercepted or replayed. Whatever the
		// first redemption issued is revoked (RFC 6749 §4.1.2).
		_ = tx.Rollback()
		if stored.GrantID.Valid {
			if derr := repo.DeleteOAuthGrant(ctx, h.database, stored.GrantID.String); derr != nil {
				log.Printf("connector: revoke on code reuse: %v", derr)
			}
		}
		log.Printf("connector: authorization code reused client_id=%s", stored.ClientID)
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "code already used", Internal: false}
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("connector: consume code: %v", err)
			return ConnectorTokens{}, &ConnectorError{Code: "server_error", Description: "", Internal: true}
		}
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "unknown code", Internal: false}
	}
	// The code is spent from here on, whatever happens next: every failure
	// below commits the spend, so a failed guess cannot be retried.
	fail := func(desc string) error {
		_ = tx.Commit()
		return &ConnectorError{Code: "invalid_grant", Description: desc}
	}
	if stored.ClientID != client.ClientID {
		return ConnectorTokens{}, fail("code was issued to another client")
	}
	if h.Now().After(stored.ExpiresAt) {
		return ConnectorTokens{}, fail("code expired")
	}
	if in.RedirectURI != stored.RedirectURI {
		return ConnectorTokens{}, fail("redirect_uri does not match the authorization request")
	}
	verifier := in.Verifier
	if stored.CodeChallenge != "" {
		if !validPKCEValue(verifier) || !pkceS256(verifier, stored.CodeChallenge) {
			return ConnectorTokens{}, fail("PKCE verification failed")
		}
	} else if verifier != "" {
		// A verifier for a code issued without a challenge is a downgrade
		// attempt (RFC 9700 §2.1.1).
		return ConnectorTokens{}, fail("code_verifier sent for a code issued without code_challenge")
	}
	if res := in.Resource; res != "" {
		if norm, ok := h.NormalizeResource(res); !ok || norm != stored.Resource {
			_ = tx.Commit()
			return ConnectorTokens{}, &ConnectorError{Code: "invalid_target", Description: "resource does not match the authorization request", Internal: false}
		}
	}
	user, err := repo.GetUserByID(ctx, tx, stored.UserID)
	if err != nil || user.Username == "admin" || user.Suspended {
		return ConnectorTokens{}, fail("account unavailable")
	}
	grantID, err := repo.InsertOAuthGrant(ctx, tx, stored.UserID, client.ClientID, stored.Scope, stored.Resource, stored.Device)
	if err != nil {
		log.Printf("connector: insert grant: %v", err)
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Description: "", Internal: true}
	}
	if err := repo.SetOAuthCodeGrant(ctx, tx, codeHash, grantID); err != nil {
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Description: "", Internal: true}
	}
	return h.issueTokens(ctx, tx, grantID, stored.Scope)
}
func (h *Connector) Refresh(ctx context.Context, in ConnectorTokenInput, client model.OAuthClient) (ConnectorTokens, error) {
	presented := in.RefreshToken
	if !strings.HasPrefix(presented, prefixRefresh) {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "unknown refresh token", Internal: false}
	}
	tokenHash := hashSecret(presented)
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Description: "", Internal: true}
	}
	defer tx.Rollback()
	tok, err := repo.GetOAuthToken(ctx, tx, tokenHash)
	if err != nil || tok.Kind != "refresh" {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "unknown refresh token", Internal: false}
	}
	if tok.ClientID != client.ClientID {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "refresh token was issued to another client", Internal: false}
	}
	if h.Now().After(tok.ExpiresAt) {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "refresh token expired", Internal: false}
	}
	if h.HackTeams {
		if _, _, ok := authrules.GrantKind(tok.Scope); !ok {
			// Not spent: the transaction rolls back. The person reconnects.
			return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "this connection cannot be refreshed; reconnect and choose a team or Manage my events", Internal: false}
		}
	}
	fresh, err := repo.MarkRefreshTokenUsed(ctx, tx, tokenHash)
	if err != nil {
		return ConnectorTokens{}, &ConnectorError{Code: "server_error", Description: "", Internal: true}
	}
	if !fresh {
		// A rotated-out refresh token came back: either the client or a thief
		// holds a copy. The whole family goes, so the thief's copy dies too
		// (OAuth 2.1 §4.3.1). The person reconnects once.
		_ = tx.Rollback()
		if derr := repo.DeleteOAuthGrant(ctx, h.database, tok.GrantID); derr != nil {
			log.Printf("connector: revoke on refresh reuse: %v", derr)
		}
		log.Printf("connector: refresh token reused client_id=%s user_id=%s; connection revoked", tok.ClientID, tok.UserID)
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "refresh token already used; the connection was revoked", Internal: false}
	}
	if scope := in.Scope; scope != "" {
		granted := map[string]bool{}
		for _, g := range strings.Fields(tok.Scope) {
			granted[g] = true
		}
		for _, s := range strings.Fields(scope) {
			if !granted[s] {
				return ConnectorTokens{}, &ConnectorError{Code: "invalid_scope", Description: "scope exceeds the original grant", Internal: false}
			}
		}
	}
	if res := in.Resource; res != "" {
		if norm, ok := h.NormalizeResource(res); !ok || norm != tok.Resource {
			return ConnectorTokens{}, &ConnectorError{Code: "invalid_target", Description: "resource does not match the original grant", Internal: false}
		}
	}
	// A suspended account's refresh is refused without being spent (the
	// transaction rolls back), so it works again once the account is re-enabled.
	user, err := repo.GetUserByID(ctx, tx, tok.UserID)
	if err != nil || user.Username == "admin" || user.Suspended {
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_grant", Description: "account unavailable", Internal: false}
	}
	_ = repo.TouchOAuthGrant(ctx, tx, tok.GrantID)
	return h.issueTokens(ctx, tx, tok.GrantID, tok.Scope)
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
