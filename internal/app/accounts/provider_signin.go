package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/oauth"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	sites "github.com/vsriram/simple-host/internal/repository/sites"
	"golang.org/x/oauth2"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
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
	Attribution                      func(context.Context, AttributionStore, string, string) model.Signup
	Assign                           func(context.Context, AttributionStore, string, string, bool)
	NonceHash                        func(string) sql.NullString
}

func providerRandomRaw(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}
func (flow *ProviderSignIn) Callback(ctx context.Context, in ProviderCallbackInput) (ProviderResult, *ProviderFailure) {
	st, err := repo.ConsumeOAuthState(ctx, flow.service.database, in.State)
	if err != nil {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	}
	if st.Provider != in.Name {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	}
	if in.ProviderError != "" || strings.TrimSpace(in.Code) == "" {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	}

	ident, err := flow.providers[in.Name].Exchange(ctx, strings.TrimSpace(in.Code), st.CodeVerifier)
	if err != nil {
		var refused *oauth.RefusalError
		if errors.As(err, &refused) {
			// A business-rule refusal (unverified email, domain not
			// allowed): the message names no internals and is safe to show.
			return ProviderResult{}, &ProviderFailure{Status: 403, Refusal: refused.Message}
		}
		log.Printf("oauth: %s exchange: %v", in.Name, err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	if ident.UserID == "" {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}

	tx, err := flow.service.database.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("oauth: begin: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	defer tx.Rollback()

	user, created, err := resolveUser(ctx, tx, ident)
	if errors.Is(err, errOAuthEmailRefused) || errors.Is(err, errOAuthAdminRefused) {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	}
	if errors.Is(err, errOAuthPasswordRefused) {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusConflict, Refusal: "This email already has a password account on this install. Sign in with the password instead."}
	}
	if err != nil {
		log.Printf("oauth: resolve user: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	// Where a new account came from, recorded after the commit below so a
	// failure to record it can never abort the sign-in.
	var signup *model.Signup
	if created {
		sig := model.Signup{Source: model.SignupVisitor, Method: ident.Provider}
		if st.Purpose == "owner" {
			sig = in.Attribution(ctx, tx, st.ReturnTo, ident.Provider)
		}
		signup = &sig
	}
	recordAfterCommit := func() {
		if signup != nil {
			RecordSignup(ctx, flow.service.database, user.ID, *signup)
		}
	}
	// A suspended account signs in on no site. (A dashboard sign-in is
	// refused, with the reason, when its token is redeemed at /v1/auth/verify.)
	if susp, serr := repo.UserSuspended(ctx, tx, user.ID); st.Purpose != "owner" && (serr != nil || susp) {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusForbidden}
	}

	if st.Purpose == "owner" {
		if created || !user.Handle.Valid {
			in.Assign(ctx, tx, user.ID, user.Username, in.RandomHandles)
		}
		linkToken, err := secureHex(24)
		if err != nil {
			log.Printf("oauth: owner link token: %v", err)
			return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
		}
		code, err := numericCode(6)
		if err != nil {
			log.Printf("oauth: owner unused code: %v", err)
			return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
		}
		// The one-time token is bound to the nonce of the tab that started
		// this sign-in (cn in its return_to), so only that tab can redeem it.
		if err := repo.CreateAuthToken(ctx, tx, user.Username, code, linkToken, time.Now().Add(in.AuthTTL), "dashboard", sql.NullString{}, in.NonceHash(st.ReturnTo)); err != nil {
			log.Printf("oauth: owner auth token: %v", err)
			return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
		}
		if err := tx.Commit(); err != nil {
			log.Printf("oauth: commit: %v", err)
			return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
		}
		recordAfterCommit()
		return ProviderResult{State: st, LinkToken: linkToken}, nil
	}

	// A site sign-in must have started on the site host, bound to a browser.
	if !st.SiteID.Valid || st.SiteID.String == "" || st.Host == "" || !st.NonceHash.Valid || !providerNoncePattern.MatchString(st.NonceHash.String) {
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadRequest}
	}

	sessionID, err := providerRandomRaw(32)
	if err != nil {
		log.Printf("oauth: session id: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	now := time.Now()
	if err := repo.InsertPendingVisitorSession(ctx, tx, sessionID, user.ID, st.SiteID.String, st.Host, now.Add(time.Minute)); err != nil {
		log.Printf("oauth: insert session: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}

	once, err := secureHex(32)
	if err != nil {
		log.Printf("oauth: establish once: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	if err := repo.InsertEstablishToken(ctx, tx, once, sessionID, st.Host, st.ReturnTo, st.NonceHash, now.Add(60*time.Second)); err != nil {
		log.Printf("oauth: insert establish: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("oauth: commit: %v", err)
		return ProviderResult{}, &ProviderFailure{Status: http.StatusBadGateway}
	}
	recordAfterCommit()

	return ProviderResult{State: st, Once: once}, nil
}
func resolveUser(ctx context.Context, q repo.Querier, ident ProviderIdentity) (model.User, bool, error) {
	// A re-login through an identity already linked here needs no
	// password-account check to stay sound: the link itself was only ever
	// made below, past that check, so it can never point at one of
	// CreatePasswordAccount's accounts in the first place. The check here
	// is defence in depth only, in case that ever stops being true.
	existing, err := repo.GetOAuthIdentity(ctx, q, ident.Provider, ident.UserID)
	if err == nil {
		user, uerr := repo.GetUserByID(ctx, q, existing.UserID)
		if uerr != nil {
			return model.User{}, false, uerr
		}
		if pw, perr := repo.PasswordSignupAccount(ctx, q, user.ID); perr != nil {
			return model.User{}, false, perr
		} else if pw {
			log.Printf("oauth: refused existing-identity link to a password account provider=%s", ident.Provider)
			return model.User{}, false, errOAuthPasswordRefused
		}
		if terr := repo.TouchOAuthIdentity(ctx, q, existing.ID, ident.Email, ident.EmailVerified); terr != nil {
			return model.User{}, false, terr
		}
		return user, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.User{}, false, err
	}

	email := strings.ToLower(strings.TrimSpace(ident.Email))
	if email == "" || !ident.EmailVerified || !codeEmailPattern.MatchString(email) {
		if email == "" {
			log.Printf("oauth: refused unverified or missing email provider=%s", ident.Provider)
		} else {
			log.Printf("oauth: refused unverified or missing email provider=%s email=%s", ident.Provider, redactEmail(email))
		}
		return model.User{}, false, errOAuthEmailRefused
	}

	user, err := repo.GetUserByUsername(ctx, q, email)
	created := false
	switch {
	case err == nil:
		if user.Username == "admin" {
			log.Printf("oauth: refused admin link provider=%s", ident.Provider)
			return model.User{}, false, errOAuthAdminRefused
		}
	case errors.Is(err, sql.ErrNoRows):
		user, err = repo.CreateUser(ctx, q, email, "", false)
		if err != nil {
			if repo.IsUniqueConstraintViolation(err) {
				user, err = repo.GetUserByUsername(ctx, q, email)
			}
			if err != nil {
				return model.User{}, false, err
			}
		} else {
			created = true
		}
		if user.Username == "admin" {
			log.Printf("oauth: refused admin link provider=%s", ident.Provider)
			return model.User{}, false, errOAuthAdminRefused
		}
	default:
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
		if pw, perr := repo.PasswordSignupAccount(ctx, q, user.ID); perr != nil {
			return model.User{}, false, perr
		} else if pw {
			log.Printf("oauth: refused password-account link provider=%s", ident.Provider)
			return model.User{}, false, errOAuthPasswordRefused
		}
	}

	if _, err := repo.InsertOAuthIdentity(ctx, q, user.ID, ident.Provider, ident.UserID, email, ident.EmailVerified); err != nil {
		if repo.IsUniqueConstraintViolation(err) {
			// Concurrent callback won the race — reload the linked row.
			linked, lerr := repo.GetOAuthIdentity(ctx, q, ident.Provider, ident.UserID)
			if lerr != nil {
				return model.User{}, false, lerr
			}
			user, uerr := repo.GetUserByID(ctx, q, linked.UserID)
			return user, false, uerr
		}
		return model.User{}, false, err
	}
	return user, created, nil
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
func (s *Service) GetSiteByCustomDomain(ctx context.Context, domain string) (model.SiteDomainInfo, error) {
	return sites.GetSiteByCustomDomain(ctx, s.database, domain, repo.HostForms(domain))
}

func (s *Service) SiteOffline(ctx context.Context, id string) (bool, error) {
	return sites.SiteOffline(ctx, s.database, id)
}

func (s *Service) SiteSuspension(ctx context.Context, id string) (bool, string, error) {
	return sites.SiteSuspension(ctx, s.database, id)
}

func (s *Service) GetSitePasscode(ctx context.Context, id string) (model.SitePasscodeRow, error) {
	return sites.GetSitePasscode(ctx, s.database, id)
}
