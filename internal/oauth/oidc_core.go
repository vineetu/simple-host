// Ported from simple-host-enterprise internal/oidc (same author; Enterprise
// is Apache-2.0, this repo is MIT), kept stdlib-only and otherwise
// unchanged. It lives in internal/oauth rather than its own internal/oidc
// package because this repo's one layering rule (scripts/check-layering.sh)
// lets only internal/handler depend on another internal package: every
// other package, oauth included, is a leaf. company.go (the oauth.Provider
// named "oidc") is the only caller, so the low-level OIDC pieces below are
// unexported (oidcProvider, oidcConfig, ...) rather than carrying the
// exported names this file had as its own package.
//
// This file speaks just enough of OpenID Connect to sign somebody in:
// discovery, the Authorization Code flow with PKCE, and id_token
// verification against the provider's published JWKS. It has no dependency
// beyond the standard library — Google and Dex (the two providers this
// code is proven against) both fit inside RFC 6749 + PKCE + RS256, and
// that is deliberately all it promises.
//
// What it does NOT do: refresh tokens (sessions are this application's own
// concept, not the provider's), userinfo endpoint calls (every claim this
// application needs is asked for in the id_token's scopes), or any algorithm
// but RS256 (both reference providers sign with it; a provider that does not
// is out of scope rather than silently downgraded to a weaker check).
package oauth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// oidcConfig is everything an oidcProvider needs, sourced from OIDC_* configuration.
type oidcConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is this application's own callback, e.g.
	// "https://<base>/auth/callback". Sent on both the authorization request
	// and the token exchange, and must match what the provider has on file
	// byte for byte.
	RedirectURL string
	Scopes      []string
	// InsecureAllowed permits a plain-http issuer and endpoints
	// (OIDC_INSECURE_ALLOWED; local evaluation only).
	InsecureAllowed bool
}

// oidcProvider is a discovered, ready-to-use OIDC provider: its endpoints and its
// signing keys. Construct with discoverOIDC once at startup; it is safe for
// concurrent use.
type oidcProvider struct {
	cfg    oidcConfig
	client *http.Client
	doc    discoveryDocument
	jwks   *jwksCache
}

// discoverOIDC fetches the issuer's discovery document and prepares its JWKS
// cache. httpClient may be nil, in which case a client with a 15-second
// timeout is used.
func discoverOIDC(ctx context.Context, cfg oidcConfig, httpClient *http.Client) (*oidcProvider, error) {
	if httpClient == nil {
		// Never http.DefaultClient: it has no timeout, so a provider that
		// stops answering would hang sign-in (and startup) indefinitely.
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc: issuer, client id, client secret and redirect URL are all required")
	}
	doc, err := discover(ctx, httpClient, cfg.Issuer, cfg.InsecureAllowed)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %s: %w", cfg.Issuer, err)
	}
	return &oidcProvider{
		cfg:    cfg,
		client: httpClient,
		doc:    doc,
		jwks:   newJWKSCache(httpClient, doc.JWKSURI),
	}, nil
}

// oidcAuthRequest is the state a caller must hold (typically in a short-lived
// cookie) between the redirect to the provider and the callback.
type oidcAuthRequest struct {
	State        string
	Nonce        string
	CodeVerifier string
}

// newOIDCAuthRequest generates a fresh state, nonce and PKCE verifier.
func newOIDCAuthRequest() (oidcAuthRequest, error) {
	state, err := newOIDCState()
	if err != nil {
		return oidcAuthRequest{}, err
	}
	nonce, err := newOIDCNonce()
	if err != nil {
		return oidcAuthRequest{}, err
	}
	verifier, err := newOIDCCodeVerifier()
	if err != nil {
		return oidcAuthRequest{}, err
	}
	return oidcAuthRequest{State: state, Nonce: nonce, CodeVerifier: verifier}, nil
}

// AuthCodeURL builds the authorization request URL. hintDomain, when
// non-empty, is sent as the provider's domain hint (Google: "hd") — a
// narrowing of the account chooser only; the callback still checks the
// claims independently.
func (p *oidcProvider) AuthCodeURL(req oidcAuthRequest, hintDomain string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("scope", strings.Join(p.cfg.Scopes, " "))
	q.Set("state", req.State)
	q.Set("nonce", req.Nonce)
	q.Set("code_challenge", oidcCodeChallengeS256(req.CodeVerifier))
	q.Set("code_challenge_method", "S256")
	if hintDomain != "" {
		q.Set("hd", hintDomain)
	}
	sep := "?"
	if strings.Contains(p.doc.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return p.doc.AuthorizationEndpoint + sep + q.Encode()
}

// oidcTokenResponse is the subset of RFC 6749's token response this package
// reads. access_token and refresh_token are not modeled: this application's
// unit of session is its own sessions table, not the provider's tokens.
type oidcTokenResponse struct {
	IDToken string `json:"id_token"`
}

// Exchange trades an authorization code for an id_token, presenting the PKCE
// verifier in place of a client-secret-only exchange (both are sent: Google
// and Dex both accept a confidential client that also uses PKCE, and sending
// both is strictly more defensible than picking one).
func (p *oidcProvider) Exchange(ctx context.Context, code, codeVerifier string) (oidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("client_secret", p.cfg.ClientSecret)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return oidcTokenResponse{}, fmt.Errorf("oidc: token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Only the RFC 6749 error code is kept: the rest of the body is the
		// provider's free text and does not belong in logs.
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		return oidcTokenResponse{}, fmt.Errorf("oidc: token exchange: unexpected status %s (error %q)", resp.Status, truncate(e.Error, 64))
	}
	var tok oidcTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return oidcTokenResponse{}, fmt.Errorf("oidc: decode token response: %w", err)
	}
	if tok.IDToken == "" {
		return oidcTokenResponse{}, errors.New("oidc: token response carried no id_token")
	}
	return tok, nil
}

// oidcClaims is the verified, decoded content of an id_token that this
// application acts on. EmailVerifiedSet distinguishes "the provider said
// false or true" from "the provider sent no such claim at all" — Entra's
// common endpoint is the reference case that omits it, and that difference
// must be treated as meaningful rather than defaulting one way.
type oidcClaims struct {
	Subject          string
	Email            string
	EmailVerified    bool
	EmailVerifiedSet bool
	// HostedDomain is Google's "hd" claim when present: the Workspace domain
	// for a Workspace account, absent for a consumer account.
	HostedDomain string
	Raw          map[string]any
}

// StringClaim reads an arbitrary top-level claim as a string, for
// OIDC_USERNAME_CLAIM / OIDC_ADMIN_CLAIM style configuration that names a
// claim this package has no fixed field for.
func (c oidcClaims) StringClaim(name string) (string, bool) {
	v, ok := c.Raw[name]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// HasClaimValue reports whether the named claim, read as either a single
// string or a list of strings (the shape a "groups" claim usually takes),
// contains value. Used for OIDC_ADMIN_CLAIM / OIDC_ADMIN_VALUE.
func (c oidcClaims) HasClaimValue(name, value string) bool {
	v, ok := c.Raw[name]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case string:
		return t == value
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s == value {
				return true
			}
		}
	}
	return false
}

// VerifyIDToken verifies signature (RS256 via the provider's JWKS), issuer,
// audience, expiry and nonce, then decodes the claims this application
// reads.
func (p *oidcProvider) VerifyIDToken(ctx context.Context, rawToken, expectedNonce string) (oidcClaims, error) {
	header, payload, signingInput, signature, err := splitJWT(rawToken)
	if err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: %w", err)
	}
	if header.Alg != "RS256" {
		return oidcClaims{}, fmt.Errorf("oidc: unsupported id_token algorithm %q (only RS256 is accepted)", header.Alg)
	}
	key, err := p.jwks.key(ctx, header.Kid)
	if err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: %w", err)
	}
	if err := verifyRS256(key, signingInput, signature); err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: id_token signature verification failed: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return oidcClaims{}, fmt.Errorf("oidc: decode id_token claims: %w", err)
	}

	issuer, _ := raw["iss"].(string)
	if strings.TrimRight(issuer, "/") != strings.TrimRight(p.doc.Issuer, "/") {
		return oidcClaims{}, fmt.Errorf("oidc: id_token issuer %q does not match provider %q", issuer, p.doc.Issuer)
	}
	if !audienceContains(raw["aud"], p.cfg.ClientID) {
		return oidcClaims{}, fmt.Errorf("oidc: id_token audience does not include client id %q", p.cfg.ClientID)
	}
	// OIDC Core 3.1.3.7: with several audiences, azp must be this client.
	if list, ok := raw["aud"].([]any); ok && len(list) > 1 {
		if azp, _ := raw["azp"].(string); azp != p.cfg.ClientID {
			return oidcClaims{}, errors.New("oidc: id_token has several audiences and azp is not this client")
		}
	}
	exp, ok := numericClaim(raw["exp"])
	if !ok || time.Now().After(time.Unix(exp, 0)) {
		return oidcClaims{}, errors.New("oidc: id_token is expired")
	}
	nonce, _ := raw["nonce"].(string)
	if expectedNonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(expectedNonce)) != 1 {
		return oidcClaims{}, errors.New("oidc: id_token nonce does not match the authorization request")
	}

	claims := oidcClaims{Raw: raw}
	claims.Subject, _ = raw["sub"].(string)
	claims.Email, _ = raw["email"].(string)
	claims.HostedDomain, _ = raw["hd"].(string)
	if v, present := raw["email_verified"]; present {
		claims.EmailVerifiedSet = true
		claims.EmailVerified = boolClaim(v)
	}
	if claims.Subject == "" {
		return oidcClaims{}, errors.New("oidc: id_token carries no sub claim")
	}
	return claims, nil
}

func boolClaim(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, _ := strconv.ParseBool(t)
		return b
	default:
		return false
	}
}

func numericClaim(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func audienceContains(aud any, clientID string) bool {
	switch t := aud.(type) {
	case string:
		return t == clientID
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// splitJWT parses a compact JWT into its decoded header, decoded payload, the
// raw signing input (header.payload, exactly as signed) and the decoded
// signature.
func splitJWT(token string) (header jwtHeader, payload []byte, signingInput []byte, signature []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtHeader{}, nil, nil, nil, errors.New("id_token is not a compact JWT")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtHeader{}, nil, nil, nil, fmt.Errorf("decode header: %w", err)
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return jwtHeader{}, nil, nil, nil, fmt.Errorf("decode header: %w", err)
	}
	payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtHeader{}, nil, nil, nil, fmt.Errorf("decode payload: %w", err)
	}
	signature, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtHeader{}, nil, nil, nil, fmt.Errorf("decode signature: %w", err)
	}
	signingInput = []byte(parts[0] + "." + parts[1])
	return header, payload, signingInput, signature, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
