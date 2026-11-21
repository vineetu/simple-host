package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"

	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/oauth"
	"github.com/vsriram/simple-host/host/access/repo"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
	"golang.org/x/oauth2"
)

const providerStateTTL = 10 * time.Minute

var providerNoncePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var errOAuthEmailRefused = errors.New("oauth email refused")
var errOAuthAdminRefused = errors.New("oauth admin refused")
var errOAuthPasswordRefused = errors.New("oauth password account refused")

// ProviderIdentity is the stable provider user ID and verified-email snapshot.
// Email is used only for the first-link decision; re-logins key on UserID.
type ProviderIdentity struct {
	Provider      string
	UserID        string
	Email         string
	EmailVerified bool
}

// Provider supplies authorization URLs and exchanges codes for sign-in identities.
type Provider interface {
	AuthCodeURL(state, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (ProviderIdentity, error)
}

type ProviderSignIn struct {
	service   *Service
	providers map[string]Provider
}

func NewProviderSignIn(service *Service, providers map[string]Provider) *ProviderSignIn {
	return &ProviderSignIn{service: service, providers: providers}
}
func (flow *ProviderSignIn) Has(name string) bool {
	if flow == nil {
		return false
	}
	_, ok := flow.providers[name]
	return ok
}

type ProviderFailure struct {
	Status                 int
	Message, Code, Refusal string
}
type ProviderResult struct {
	State           model.OAuthState
	LinkToken, Once string
}
type ProviderCallbackInput struct {
	Name, State, Code, ProviderError string
	RandomHandles                    bool
	AuthTTL                          time.Duration
	Attribution                      func(context.Context, sqlkit.Querier, string, string) model.Signup
	Assign                           func(context.Context, sqlkit.Querier, string, string, bool)
	NonceHash                        func(string) sql.NullString
}

func providerRandomRaw(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

// Callback finishes a provider sign-in: the state it started with is spent,
// the provider's code exchanged for an identity, the account found or made,
// and either a dashboard sign-in link (owner) or a pending visitor session on
// the site it started on is issued. Every refusal is a ProviderFailure.
func (flow *ProviderSignIn) Callback(ctx context.Context, in ProviderCallbackInput) (ProviderResult, *ProviderFailure) {
	st, ident, fail := flow.callbackIdentity(ctx, in)
	if fail != nil {
		return ProviderResult{}, fail
	}
	tx, err := flow.service.database.BeginTx(ctx, nil)
	if err != nil {
		return ProviderResult{}, providerGatewayFailure("begin", err)
	}
	defer tx.Rollback()
	user, created, err := resolveUser(ctx, tx, ident)
	switch {
	case errors.Is(err, errOAuthEmailRefused) || errors.Is(err, errOAuthAdminRefused):
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	case errors.Is(err, errOAuthPasswordRefused):
		return ProviderResult{}, &ProviderFailure{Status: http.StatusConflict, Refusal: "This email already has a password account on this install. Sign in with the password instead."}
	case err != nil:
		return ProviderResult{}, providerGatewayFailure("resolve user", err)
	}
	// Where a new account came from (a visitor sign-in, or the owner sign-in's
	// own attribution), recorded after the commit so a failure to record it
	// can never abort the sign-in.
	var signup *model.Signup
	if created {
		sig := model.Signup{Source: model.SignupVisitor, Method: ident.Provider}
		if st.Purpose == "owner" {
			sig = in.Attribution(ctx, tx, st.ReturnTo, ident.Provider)
		}
		signup = &sig
	}
	// A suspended account signs in on no site. (A dashboard sign-in is
	// refused, with the reason, when its token is redeemed at /v1/auth/verify.)
	if susp, serr := repo.UserSuspended(ctx, tx, user.ID); st.Purpose != "owner" && (serr != nil || susp) {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusForbidden}
	}
	var result ProviderResult
	if st.Purpose == "owner" {
		result, fail = ownerSignInLink(ctx, tx, in, st, user, created)
	} else {
		result, fail = siteSignInSession(ctx, tx, st, user)
	}
	if fail != nil {
		return ProviderResult{}, fail
	}
	if signup != nil {
		RecordSignup(ctx, flow.service.database, user.ID, *signup)
	}
	return result, nil
}

// providerGatewayFailure logs a failed step and answers 502.
func providerGatewayFailure(step string, err error) *ProviderFailure {
	log.Printf("oauth: %s: %v", step, err)
	return &ProviderFailure{Status: http.StatusBadGateway}
}

// callbackIdentity spends the sign-in's state and exchanges the provider's
// code for the identity it names. A provider's business-rule refusal
// (unverified email, domain not allowed) names no internals and is shown.
func (flow *ProviderSignIn) callbackIdentity(ctx context.Context, in ProviderCallbackInput) (model.OAuthState, ProviderIdentity, *ProviderFailure) {
	st, err := repo.ConsumeOAuthState(ctx, flow.service.database, in.State)
	if err != nil || st.Provider != in.Name || in.ProviderError != "" || strings.TrimSpace(in.Code) == "" {
		return st, ProviderIdentity{}, &ProviderFailure{Status: http.StatusBadRequest}
	}
	ident, err := flow.providers[in.Name].Exchange(ctx, strings.TrimSpace(in.Code), st.CodeVerifier)
	if err != nil {
		var refused *oauth.RefusalError
		if errors.As(err, &refused) {
			return st, ident, &ProviderFailure{Status: 403, Refusal: refused.Message}
		}
		return st, ident, providerGatewayFailure(in.Name+" exchange", err)
	}
	if ident.UserID == "" {
		return st, ident, &ProviderFailure{Status: http.StatusBadGateway}
	}
	return st, ident, nil
}

// ownerSignInLink issues the dashboard's one-time sign-in token, bound to
// the nonce of the tab that started this sign-in (cn in its return_to), so
// only that tab can redeem it, and commits.
func ownerSignInLink(ctx context.Context, tx *sql.Tx, in ProviderCallbackInput, st model.OAuthState, user model.User, created bool) (ProviderResult, *ProviderFailure) {
	if created || !user.Handle.Valid {
		in.Assign(ctx, tx, user.ID, user.Username, in.RandomHandles)
	}
	linkToken, err := secureHex(24)
	if err != nil {
		return ProviderResult{}, providerGatewayFailure("owner link token", err)
	}
	code, err := numericCode(6)
	if err != nil {
		return ProviderResult{}, providerGatewayFailure("owner unused code", err)
	}
	if err := repo.CreateAuthToken(ctx, tx, user.Username, code, linkToken, time.Now().Add(in.AuthTTL), "dashboard", sql.NullString{}, in.NonceHash(st.ReturnTo)); err != nil {
		return ProviderResult{}, providerGatewayFailure("owner auth token", err)
	}
	if err := tx.Commit(); err != nil {
		return ProviderResult{}, providerGatewayFailure("commit", err)
	}
	return ProviderResult{State: st, LinkToken: linkToken}, nil
}

// siteSignInSession issues a pending visitor session on the site the sign-in
// started on, and the one-time token the site's own host redeems for it, and
// commits. A site sign-in must have started on the site host, bound to a
// browser.
func siteSignInSession(ctx context.Context, tx *sql.Tx, st model.OAuthState, user model.User) (ProviderResult, *ProviderFailure) {
	if !st.SiteID.Valid || st.SiteID.String == "" || st.Host == "" || !st.NonceHash.Valid || !providerNoncePattern.MatchString(st.NonceHash.String) {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	}
	sessionID, err := providerRandomRaw(32)
	if err != nil {
		return ProviderResult{}, providerGatewayFailure("session id", err)
	}
	now := time.Now()
	if err := repo.InsertPendingVisitorSession(ctx, tx, sessionID, user.ID, st.SiteID.String, st.Host, now.Add(time.Minute)); err != nil {
		return ProviderResult{}, providerGatewayFailure("insert session", err)
	}
	once, err := secureHex(32)
	if err != nil {
		return ProviderResult{}, providerGatewayFailure("establish once", err)
	}
	if err := repo.InsertEstablishToken(ctx, tx, once, sessionID, st.Host, st.ReturnTo, st.NonceHash, now.Add(60*time.Second)); err != nil {
		return ProviderResult{}, providerGatewayFailure("insert establish", err)
	}
	if err := tx.Commit(); err != nil {
		return ProviderResult{}, providerGatewayFailure("commit", err)
	}
	return ProviderResult{State: st, Once: once}, nil
}

func resolveUser(ctx context.Context, q repo.Querier, ident ProviderIdentity) (model.User, bool, error) {
	existing, err := repo.GetOAuthIdentity(ctx, q, ident.Provider, ident.UserID)
	if err == nil {
		return relogin(ctx, q, ident, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.User{}, false, err
	}
	email, err := verifiedProviderEmail(ident)
	if err != nil {
		return model.User{}, false, err
	}
	user, created, err := accountForProviderEmail(ctx, q, ident.Provider, email)
	if err != nil {
		return model.User{}, false, err
	}
	// An existing account under this address that CreatePasswordAccount made
	// (admin-created; a self-signup password account's username is now a
	// synthetic value and could never match email here in the first place)
	// never proved it controls the address: this provider's verified email
	// proves the opposite, but linking it to that account would hand
	// whoever signs in here a password its creator already knows
	// (pre-account hijacking). Refuse instead of resolving to or linking it.
	if !created {
		if err := refusePasswordLink(ctx, q, ident.Provider, user.ID, "password-account link"); err != nil {
			return model.User{}, false, err
		}
	}
	return linkProviderIdentity(ctx, q, ident, user, email, created)
}

// relogin is a sign-in through an identity already linked here. It needs no
// password-account check to stay sound: the link itself was only ever made
// past that check, so it can never point at one of CreatePasswordAccount's
// accounts in the first place. The check is defence in depth only, in case
// that ever stops being true.
func relogin(ctx context.Context, q repo.Querier, ident ProviderIdentity, existing model.OAuthIdentity) (model.User, bool, error) {
	user, err := repo.GetUserByID(ctx, q, existing.UserID)
	if err != nil {
		return model.User{}, false, err
	}
	if err := refusePasswordLink(ctx, q, ident.Provider, user.ID, "existing-identity link to a password account"); err != nil {
		return model.User{}, false, err
	}
	if err := repo.TouchOAuthIdentity(ctx, q, existing.ID, ident.Email, ident.EmailVerified); err != nil {
		return model.User{}, false, err
	}
	return user, false, nil
}

// refusePasswordLink refuses (errOAuthPasswordRefused) an account that
// CreatePasswordAccount made.
func refusePasswordLink(ctx context.Context, q repo.Querier, provider, userID, what string) error {
	pw, err := repo.PasswordSignupAccount(ctx, q, userID)
	if err != nil {
		return err
	}
	if pw {
		log.Printf("oauth: refused %s provider=%s", what, provider)
		return errOAuthPasswordRefused
	}
	return nil
}

// verifiedProviderEmail is the identity's email when the provider verified
// it; a first link is never made on an unverified or missing one.
func verifiedProviderEmail(ident ProviderIdentity) (string, error) {
	email, valid := authrules.NormalEmail(ident.Email)
	if valid && ident.EmailVerified {
		return email, nil
	}
	email = strings.ToLower(strings.TrimSpace(ident.Email))
	if email == "" {
		log.Printf("oauth: refused unverified or missing email provider=%s", ident.Provider)
	} else {
		log.Printf("oauth: refused unverified or missing email provider=%s email=%s", ident.Provider, redactEmail(email))
	}
	return "", errOAuthEmailRefused
}

// accountForProviderEmail is the account under email, made now when there
// is none. The admin account is never linked.
func accountForProviderEmail(ctx context.Context, q repo.Querier, provider, email string) (model.User, bool, error) {
	user, err := repo.GetUserByUsername(ctx, q, email)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		user, err = repo.CreateUser(ctx, q, email, "", false)
		created = err == nil
		if repo.IsUniqueConstraintViolation(err) {
			user, err = repo.GetUserByUsername(ctx, q, email)
		}
	}
	if err != nil {
		return model.User{}, false, err
	}
	if user.Username == "admin" {
		log.Printf("oauth: refused admin link provider=%s", provider)
		return model.User{}, false, errOAuthAdminRefused
	}
	return user, created, nil
}

// linkProviderIdentity links the identity to user. A concurrent callback
// that won the race has linked it already: its account is the one.
func linkProviderIdentity(ctx context.Context, q repo.Querier, ident ProviderIdentity, user model.User, email string, created bool) (model.User, bool, error) {
	_, err := repo.InsertOAuthIdentity(ctx, q, user.ID, ident.Provider, ident.UserID, email, ident.EmailVerified)
	if err == nil {
		return user, created, nil
	}
	if !repo.IsUniqueConstraintViolation(err) {
		return model.User{}, false, err
	}
	linked, err := repo.GetOAuthIdentity(ctx, q, ident.Provider, ident.UserID)
	if err != nil {
		return model.User{}, false, err
	}
	user, err = repo.GetUserByID(ctx, q, linked.UserID)
	return user, false, err
}

func redactEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return "***"
	}
	return string([]rune(local)[0]) + "***@" + domain
}

func (flow *ProviderSignIn) Begin(ctx context.Context, name, sanitized, host string, siteID sql.NullString, purpose, nonce string) (string, *ProviderFailure) {
	var nonceHash sql.NullString
	if nonce != "" {
		sum := sha256.Sum256([]byte(nonce))
		nonceHash = sql.NullString{String: base64.RawURLEncoding.EncodeToString(sum[:]), Valid: true}
	}
	if err := repo.PruneExpiredOAuthStates(ctx, flow.service.database); err != nil {
		log.Printf("oauth: prune states: %v", err)
	}

	state, err := secureHex(32)
	if err != nil {
		log.Printf("oauth: random state: %v", err)
		return "", &ProviderFailure{Status: http.StatusInternalServerError, Message: "internal server error", Code: ""}
	}
	verifier := oauth2.GenerateVerifier()
	expiresAt := time.Now().Add(providerStateTTL)
	if err := repo.InsertOAuthState(ctx, flow.service.database, state, name, verifier, sanitized, host, siteID, purpose, nonceHash, expiresAt); err != nil {
		log.Printf("oauth: insert state: %v", err)
		return "", &ProviderFailure{Status: http.StatusInternalServerError, Message: "internal server error", Code: ""}
	}

	authURL := flow.providers[name].AuthCodeURL(state, verifier)
	if authURL == "" {
		// A provider whose AuthCodeURL cannot report an error (the
		// interface has none) returns "" when it could not get ready —
		// today only oidc, when its identity provider's discovery
		// document is not reachable yet. Refuse cleanly rather than
		// redirecting the browser to an empty Location.
		return "", &ProviderFailure{Status: http.StatusServiceUnavailable, Message: "company sign-in is temporarily unavailable, try again shortly", Code: "oidc_unavailable"}
	}

	return authURL, nil
}
