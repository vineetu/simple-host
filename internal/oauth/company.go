package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CompanyConfig is the OIDC_* configuration for the "oidc" provider (a
// company's own identity provider: Okta, Entra ID, Google Workspace, Dex...).
type CompanyConfig struct {
	Issuer              string
	ClientID            string
	ClientSecret        string
	RedirectURI         string
	AllowedEmailDomains []string
	InsecureAllowed     bool
}

// RefusalError is a business-rule refusal of an otherwise-valid sign-in
// (an unverified email, a domain not on ALLOWED_EMAIL_DOMAINS): Message is
// shown to the person; Reason is a short code for logs. Not a transport or
// provider-outage error — those are returned unwrapped.
type RefusalError struct {
	Reason  string
	Message string
}

func (e *RefusalError) Error() string { return e.Message }

// Company is the oauth.Provider named "oidc". Discovery
// (internal/discoverOIDC: the issuer's discovery document and JWKS) is
// lazy: the identity provider may still be starting when this process does,
// so nothing is discovered at construction. The first AuthCodeURL or
// Exchange call discovers it and caches success; a failure is never cached,
// so the very next call retries.
type Company struct {
	cfg CompanyConfig

	mu       sync.Mutex
	provider *oidcProvider // nil until a discovery has succeeded
}

// NewCompany builds the provider. Discovery happens lazily, not here, so
// construction never fails or blocks even when the IdP is unreachable.
func NewCompany(cfg CompanyConfig) *Company {
	return &Company{cfg: cfg}
}

func (c *Company) Name() string { return "oidc" }

// discovered returns the cached provider, or discovers it now.
func (c *Company) discovered(ctx context.Context) (*oidcProvider, error) {
	c.mu.Lock()
	if c.provider != nil {
		p := c.provider
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()

	p, err := discoverOIDC(ctx, oidcConfig{
		Issuer:          c.cfg.Issuer,
		ClientID:        c.cfg.ClientID,
		ClientSecret:    c.cfg.ClientSecret,
		RedirectURL:     c.cfg.RedirectURI,
		Scopes:          []string{"openid", "email", "profile"},
		InsecureAllowed: c.cfg.InsecureAllowed,
	}, nil)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.provider = p
	c.mu.Unlock()
	return p, nil
}

// companyNonce derives the OIDC nonce from the PKCE code_verifier:
// base64url(SHA-256("oidc-nonce:" + verifier)). The oauth.Provider interface
// (AuthCodeURL(state, verifier), Exchange(ctx, code, verifier)) carries no
// separate nonce parameter, and adding one would mean threading it through
// every provider and the state table for a value that is, in effect,
// already a secret held server-side with the state row — the verifier
// itself. Deriving the nonce from it instead keeps the interface the same
// shape for google, github and oidc alike.
func companyNonce(verifier string) string {
	sum := sha256.Sum256([]byte("oidc-nonce:" + verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthCodeURL builds the authorization-request URL. The Provider interface
// gives this method no context and no error return, so a discovery failure
// (the IdP not reachable yet) is logged here and reported as "" — the
// caller, OAuthHandler.beginOAuth (internal/handler/oauth.go), turns an
// empty URL into a 503 instead of redirecting the browser to nowhere.
func (c *Company) AuthCodeURL(state, verifier string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := c.discovered(ctx)
	if err != nil {
		log.Printf("oauth: oidc discovery unavailable: %v", err)
		return ""
	}
	req := oidcAuthRequest{State: state, Nonce: companyNonce(verifier), CodeVerifier: verifier}
	return p.AuthCodeURL(req, googleHint(c.cfg.Issuer, c.cfg.AllowedEmailDomains))
}

// googleHint is the "hd" account-chooser hint: a single allowed domain on a
// Google issuer narrows it. The claims check after the callback is
// independent of this hint and is what actually decides.
func googleHint(issuer string, allowedEmailDomains []string) string {
	if len(allowedEmailDomains) == 1 && isGoogleIssuer(issuer) {
		return allowedEmailDomains[0]
	}
	return ""
}

// Exchange trades the code for an id_token, verifies it, and applies the
// sign-in rules (ported from simple-host-enterprise internal/handler/auth.go):
// a verified email (or Entra's xms_edov), ALLOWED_EMAIL_DOMAINS, and Google's
// hd claim when Workspace domains are restricted. UserID is bound to the
// issuer (issuer + " " + sub) so that changing OIDC_ISSUER later can never
// make a new identity provider's subject resolve to an old provider's
// account. A rule refusal comes back as *RefusalError; every other error is
// a transport or provider problem.
func (c *Company) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	p, err := c.discovered(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc discovery: %w", err)
	}
	tok, err := p.Exchange(ctx, code, verifier)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc token exchange: %w", err)
	}
	claims, err := p.VerifyIDToken(ctx, tok.IDToken, companyNonce(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("oidc id_token: %w", err)
	}

	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if reason, message := c.refuseIdentity(claims, email); reason != "" {
		log.Printf("oauth: oidc refused reason=%s", reason)
		return Identity{}, &RefusalError{Reason: reason, Message: message}
	}
	if claims.Subject == "" {
		return Identity{}, fmt.Errorf("oidc id_token: missing sub")
	}
	return Identity{
		Provider:      "oidc",
		UserID:        c.cfg.Issuer + " " + claims.Subject,
		Email:         email,
		EmailVerified: true,
	}, nil
}

// refuseIdentity is the Enterprise sign-in rule set, unchanged in substance:
// an unverified email is refused outright (it both decides whether the
// address may sign in and whether it binds an existing account), then
// ALLOWED_EMAIL_DOMAINS, then Google's hd. Returns "", "" when sign-in may
// proceed.
func (c *Company) refuseIdentity(claims oidcClaims, email string) (reason, message string) {
	if !c.emailVerified(claims) {
		return "email_not_verified", "your email address is not verified with your company's identity provider"
	}
	if email == "" {
		return "email_not_verified", "your company's identity provider did not provide an email address"
	}
	if !c.isAllowedDomain(email) {
		return "domain_not_allowed", "this account's email domain is not allowed to sign in here"
	}
	// Google: a consumer account can carry any verified address, including
	// one at the company's domain it does not control; only a Workspace
	// account carries hd. With ALLOWED_EMAIL_DOMAINS set, hd is required and
	// must be one of them.
	if isGoogleIssuer(c.cfg.Issuer) && len(c.cfg.AllowedEmailDomains) > 0 && claims.HostedDomain == "" {
		return "google_hd_missing", "this Google account is not part of an allowed Google Workspace domain"
	}
	if claims.HostedDomain != "" && !c.isAllowedDomain("x@"+claims.HostedDomain) {
		return "google_hd_not_allowed", "this account's Google Workspace domain is not allowed to sign in here"
	}
	return "", ""
}

// emailVerified: email_verified must be present and true, except Entra ID
// never sends it; its documented equivalent is the optional xms_edov claim
// (the tenant has verified the address's domain), accepted only from an
// Entra issuer.
func (c *Company) emailVerified(claims oidcClaims) bool {
	if claims.EmailVerifiedSet {
		return claims.EmailVerified
	}
	if isEntraIssuer(c.cfg.Issuer) {
		switch v := claims.Raw["xms_edov"].(type) {
		case bool:
			return v
		case string:
			return v == "true" || v == "1"
		}
	}
	return false
}

func (c *Company) isAllowedDomain(email string) bool {
	if len(c.cfg.AllowedEmailDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(strings.ToLower(email), "@")
	if !ok {
		return false
	}
	for _, allowed := range c.cfg.AllowedEmailDomains {
		if allowed == domain {
			return true
		}
	}
	return false
}

func isGoogleIssuer(issuer string) bool {
	return strings.TrimRight(strings.ToLower(issuer), "/") == "https://accounts.google.com"
}

func isEntraIssuer(issuer string) bool {
	u, err := url.Parse(strings.ToLower(issuer))
	return err == nil && u.Host == "login.microsoftonline.com"
}
