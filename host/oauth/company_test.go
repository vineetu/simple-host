package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIdP is a minimal OIDC identity provider for company provider tests:
// discovery, JWKS and a token endpoint that hands back whatever id_token
// the test queued. up gates the discovery endpoint so a test can simulate
// the identity provider starting after this app (lazy discovery retry).
type fakeIdP struct {
	server      *httptest.Server
	key         *rsa.PrivateKey
	kid         string
	up          atomic.Bool
	discoverHit atomic.Int32
	nextIDToken string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &fakeIdP{key: key, kid: "test-kid"}
	idp.up.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		idp.discoverHit.Add(1)
		if !idp.up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
			idp.issuer(), idp.issuer()+"/auth", idp.issuer()+"/token", idp.issuer()+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}) // 65537
		fmt.Fprintf(w, `{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":%q,"n":%q,"e":%q}]}`, idp.kid, n, e)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oidcTokenResponse{IDToken: idp.nextIDToken})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (idp *fakeIdP) issuer() string { return idp.server.URL }

func (idp *fakeIdP) mintIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": idp.kid, "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// newTestCompany builds a Company against idp, with insecure http allowed
// (local test server) and the given allowed domains.
func newTestCompany(idp *fakeIdP, domains []string) *Company {
	return NewCompany(CompanyConfig{
		Issuer:              idp.issuer(),
		ClientID:            "client-1",
		ClientSecret:        "secret",
		RedirectURI:         "https://app.example.test/v1/auth/oauth/oidc/callback",
		AllowedEmailDomains: domains,
		InsecureAllowed:     true,
	})
}

func baseCompanyClaims(issuer, clientID, verifier string) map[string]any {
	return map[string]any{
		"iss":            issuer,
		"aud":            clientID,
		"sub":            "user-123",
		"email":          "person@example.com",
		"email_verified": true,
		"exp":            float64(time.Now().Add(time.Hour).Unix()),
		"nonce":          companyNonce(verifier),
	}
}

func TestCompanyExchangeHappyPath(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-1"
	idp.nextIDToken = idp.mintIDToken(t, baseCompanyClaims(idp.issuer(), "client-1", verifier))

	ident, err := c.Exchange(context.Background(), "code", verifier)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if ident.Provider != "oidc" {
		t.Errorf("Provider = %q, want oidc", ident.Provider)
	}
	if want := idp.issuer() + " user-123"; ident.UserID != want {
		t.Errorf("UserID = %q, want %q (issuer-bound)", ident.UserID, want)
	}
	if ident.Email != "person@example.com" || !ident.EmailVerified {
		t.Errorf("Email/EmailVerified = %q/%v", ident.Email, ident.EmailVerified)
	}
}

func TestCompanyExchangeLowercasesEmail(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-case"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	claims["email"] = "Person@Example.COM"
	idp.nextIDToken = idp.mintIDToken(t, claims)

	ident, err := c.Exchange(context.Background(), "code", verifier)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if ident.Email != "person@example.com" {
		t.Errorf("Email = %q, want lowercased", ident.Email)
	}
}

func TestCompanyExchangeRejectsWrongNonce(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-2"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	claims["nonce"] = "not-the-derived-nonce"
	idp.nextIDToken = idp.mintIDToken(t, claims)

	if _, err := c.Exchange(context.Background(), "code", verifier); err == nil {
		t.Fatal("expected a nonce mismatch to be rejected")
	}
}

func TestCompanyExchangeRejectsWrongAudience(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-3"
	claims := baseCompanyClaims(idp.issuer(), "someone-elses-client", verifier)
	idp.nextIDToken = idp.mintIDToken(t, claims)

	if _, err := c.Exchange(context.Background(), "code", verifier); err == nil {
		t.Fatal("expected a wrong audience to be rejected")
	}
}

func TestCompanyExchangeRejectsWrongIssuer(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-4"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	claims["iss"] = "https://not-the-configured-issuer.example.test"
	idp.nextIDToken = idp.mintIDToken(t, claims)

	if _, err := c.Exchange(context.Background(), "code", verifier); err == nil {
		t.Fatal("expected a wrong issuer to be rejected")
	}
}

func TestCompanyExchangeRejectsExpired(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-5"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	claims["exp"] = float64(time.Now().Add(-time.Hour).Unix())
	idp.nextIDToken = idp.mintIDToken(t, claims)

	if _, err := c.Exchange(context.Background(), "code", verifier); err == nil {
		t.Fatal("expected an expired token to be rejected")
	}
}

func TestCompanyExchangeRefusesUnverifiedEmail(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-6"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	claims["email_verified"] = false
	idp.nextIDToken = idp.mintIDToken(t, claims)

	_, err := c.Exchange(context.Background(), "code", verifier)
	var refused *RefusalError
	if err == nil {
		t.Fatal("expected an unverified email to be refused")
	}
	if !errors.As(err, &refused) {
		t.Fatalf("expected a *RefusalError, got %T: %v", err, err)
	}
	if refused.Reason != "email_not_verified" {
		t.Errorf("Reason = %q, want email_not_verified", refused.Reason)
	}
}

func TestCompanyExchangeRefusesDomainNotAllowed(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, []string{"allowed.example.com"})
	verifier := "verifier-7"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier) // email at example.com
	idp.nextIDToken = idp.mintIDToken(t, claims)

	_, err := c.Exchange(context.Background(), "code", verifier)
	var refused *RefusalError
	if !errors.As(err, &refused) {
		t.Fatalf("expected a *RefusalError, got %T: %v", err, err)
	}
	if refused.Reason != "domain_not_allowed" {
		t.Errorf("Reason = %q, want domain_not_allowed", refused.Reason)
	}
}

func TestCompanyExchangeAllowsMatchingDomain(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, []string{"example.com", "example.co.uk"})
	verifier := "verifier-8"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	idp.nextIDToken = idp.mintIDToken(t, claims)

	if _, err := c.Exchange(context.Background(), "code", verifier); err != nil {
		t.Fatalf("expected a matching domain to be allowed: %v", err)
	}
}

// --- Google hd and Entra xms_edov: unit-level, same methods Exchange calls ---

func TestCompanyRefuseIdentityGoogleHD(t *testing.T) {
	c := &Company{cfg: CompanyConfig{Issuer: "https://accounts.google.com", AllowedEmailDomains: []string{"example.com"}}}

	// A Workspace account with hd set and allowed: fine.
	if reason, _ := c.refuseIdentity(oidcClaims{EmailVerifiedSet: true, EmailVerified: true, HostedDomain: "example.com"}, "person@example.com"); reason != "" {
		t.Errorf("allowed Workspace domain refused: %s", reason)
	}
	// A consumer Google account with no hd, even at an allowed-looking
	// address: refused — hd is required once domains are restricted.
	if reason, _ := c.refuseIdentity(oidcClaims{EmailVerifiedSet: true, EmailVerified: true}, "person@example.com"); reason != "google_hd_missing" {
		t.Errorf("reason = %q, want google_hd_missing", reason)
	}
	// hd present but not on the allowed list: refused.
	if reason, _ := c.refuseIdentity(oidcClaims{EmailVerifiedSet: true, EmailVerified: true, HostedDomain: "other.com"}, "person@other.com"); reason != "domain_not_allowed" && reason != "google_hd_not_allowed" {
		t.Errorf("reason = %q, want a domain refusal", reason)
	}
}

func TestCompanyEmailVerifiedEntraXmsEdov(t *testing.T) {
	c := &Company{cfg: CompanyConfig{Issuer: "https://login.microsoftonline.com/some-tenant/v2.0"}}

	// Entra never sends email_verified; xms_edov true stands in for it.
	if !c.emailVerified(oidcClaims{Raw: map[string]any{"xms_edov": true}}) {
		t.Error("Entra xms_edov=true must count as verified")
	}
	if !c.emailVerified(oidcClaims{Raw: map[string]any{"xms_edov": "true"}}) {
		t.Error("Entra xms_edov=\"true\" (string) must count as verified")
	}
	if c.emailVerified(oidcClaims{Raw: map[string]any{"xms_edov": false}}) {
		t.Error("Entra xms_edov=false must not count as verified")
	}
	if c.emailVerified(oidcClaims{}) {
		t.Error("no email_verified and no xms_edov must not count as verified")
	}

	// A non-Entra issuer gets no such benefit of the doubt.
	other := &Company{cfg: CompanyConfig{Issuer: "https://idp.example.test"}}
	if other.emailVerified(oidcClaims{Raw: map[string]any{"xms_edov": true}}) {
		t.Error("xms_edov must only be honored for an Entra issuer")
	}
}

// --- lazy discovery: retried, success cached, failure never cached ---

func TestCompanyLazyDiscoveryRetries(t *testing.T) {
	idp := newFakeIdP(t)
	idp.up.Store(false) // the identity provider has not started yet
	c := newTestCompany(idp, nil)

	if url := c.AuthCodeURL("state-1", "verifier-down"); url != "" {
		t.Fatalf("AuthCodeURL with the IdP down = %q, want empty", url)
	}
	hitsWhileDown := idp.discoverHit.Load()
	if hitsWhileDown == 0 {
		t.Fatal("expected a discovery attempt while down")
	}

	idp.up.Store(true) // the identity provider is now up
	url := c.AuthCodeURL("state-2", "verifier-up")
	if url == "" {
		t.Fatal("AuthCodeURL with the IdP up should succeed")
	}
	if !strings.Contains(url, "code_challenge=") {
		t.Errorf("auth URL missing PKCE challenge: %s", url)
	}
	hitsAfterUp := idp.discoverHit.Load()
	if hitsAfterUp <= hitsWhileDown {
		t.Fatal("expected a fresh discovery attempt once the IdP came up (failure must not be cached)")
	}

	// A third call must not discover again: success is cached.
	if url := c.AuthCodeURL("state-3", "verifier-up-2"); url == "" {
		t.Fatal("AuthCodeURL after a cached discovery should succeed")
	}
	if idp.discoverHit.Load() != hitsAfterUp {
		t.Errorf("discovery hit again after success was cached: %d -> %d", hitsAfterUp, idp.discoverHit.Load())
	}
}

func TestGoogleHint(t *testing.T) {
	cases := []struct {
		name    string
		issuer  string
		domains []string
		want    string
	}{
		{"google with exactly one domain", "https://accounts.google.com", []string{"example.com"}, "example.com"},
		{"google with no domains", "https://accounts.google.com", nil, ""},
		{"google with two domains", "https://accounts.google.com", []string{"a.com", "b.com"}, ""},
		{"non-google issuer with one domain", "https://idp.example.test", []string{"example.com"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := googleHint(c.issuer, c.domains); got != c.want {
				t.Errorf("googleHint(%q, %v) = %q, want %q", c.issuer, c.domains, got, c.want)
			}
		})
	}
}

func TestCompanyExchangeMultipleAudiencesNeedAzp(t *testing.T) {
	idp := newFakeIdP(t)
	c := newTestCompany(idp, nil)
	verifier := "verifier-azp"
	claims := baseCompanyClaims(idp.issuer(), "client-1", verifier)
	claims["aud"] = []any{"client-1", "another-client"}
	idp.nextIDToken = idp.mintIDToken(t, claims)
	if _, err := c.Exchange(context.Background(), "code", verifier); err == nil {
		t.Fatal("several audiences without azp must be rejected")
	}
	claims["azp"] = "client-1"
	idp.nextIDToken = idp.mintIDToken(t, claims)
	if _, err := c.Exchange(context.Background(), "code", verifier); err != nil {
		t.Fatalf("several audiences with azp = this client: %v", err)
	}
}
