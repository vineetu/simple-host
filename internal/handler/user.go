package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/model"
)

var validEmail = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

const (
	maxCodeAttempts = 3
)

type UserHandler struct {
	httpCaches HTTPCaches
	ed         *edition.Edition
	limitSet
	database      *sql.DB
	accounts      *accounts.Service
	mailer        signInCodeSender
	publicBaseURL string

	// Abuse limiters: ipLimiter caps requests per client IP across both auth
	// routes; emailLimiter caps challenges/verifies aimed at a single address
	// (mail-bomb + code-grinding defense). See ratelimit.go.
	ipLimiter    *rateLimiter
	emailLimiter *rateLimiter
	// checkLimiter caps address checks per client IP (GET /v1/handles/check).
	checkLimiter                            *rateLimiter
	passwordEnabled, passwordSelfSignup     bool
	passwordLoginLimiter, passwordIPLimiter *rateLimiter

	// publicPage builds an account's public page address (SiteHandler.PersonPageURL).
	publicPage func(handle string) string
	fileUsage  func(context.Context, *model.User) (*accountFileUsage, error)
	// addressState reports the account's own-address certificate state
	// (SiteHandler.AddressState).
	addressState func(handle string) *addressState

	// alerts emails the owner after each sign-in (signin_alert.go).
	alerts *SignInAlerts
	// reviewerEmail is the plugin reviewer account (REVIEW_ACCOUNT_EMAIL):
	// its email cannot be changed and it gets no sign-in alerts.
	reviewerEmail string
	// previewAccounts is PREVIEW_ACCOUNTS (lowercased): their email cannot
	// be changed (account_email.go).
	previewAccounts map[string]bool

	// geoBlock gates email-code sign-in and sign-up by the request's country
	// (SIGNUP_BLOCKED_COUNTRIES). Zero value: off. See signupgeo.go.
	geoBlock signupGeoBlock

	// assets is the asset/chrome pipeline for ed and publicBaseURL (the
	// message pages in account_email.go); read it through pipeline().
	assets     *assetPipeline
	assetsOnce sync.Once
}

// pipeline is h's asset pipeline. A bare &UserHandler{} (tests) gets one for
// its (possibly nil, so Host) edition on first use.
func (h *UserHandler) pipeline() *assetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = newAssetPipeline(h.ed, h.publicBaseURL)
		}
	})
	return h.assets
}

// SetSignupGeoBlock wires SIGNUP_BLOCKED_COUNTRIES and the local geo database
// (internal/geoip, GEOIP_DIR) so every dashboard email-code sign-in or
// sign-up attempt from a blocked country is refused before anything is
// created or emailed — a new account and an existing one alike. geo may be
// nil; countries may be empty — either turns this off.
func (h *UserHandler) SetSignupGeoBlock(guard accounts.SignupGeoBlock) {
	h.geoBlock = guard
}

// SignInAlerts is the sign-in alert sender, shared with the connector's
// consent screen.
func (h *UserHandler) SignInAlerts() *SignInAlerts { return h.alerts }

// SetReviewerEmail names the plugin reviewer account (REVIEW_ACCOUNT_EMAIL).
func (h *UserHandler) SetReviewerEmail(address string) {
	h.reviewerEmail = strings.TrimSpace(address)
	h.alerts.reviewerEmail = h.reviewerEmail
}

// SetPublicPage sets how GET /v1/me names the account's public page.
func (h *UserHandler) SetPublicPage(f func(handle string) string) { h.publicPage = f }

// SetAddressState sets how GET /v1/me reports the account's own address.
func (h *UserHandler) SetAddressState(f func(handle string) *addressState) { h.addressState = f }

type authRequest struct {
	Email string `json:"email"`
	// NonceHash is base64url(SHA-256(nonce)) of a nonce the requesting page
	// keeps. With it, the email carries a sign-in link that works only in
	// that browser; without it, the email carries the code alone.
	NonceHash string `json:"nonce_hash"`
}

type authChallengeResponse struct {
	Message   string `json:"message"`
	Email     string `json:"email"`
	ExpiresIn int    `json:"expires_in_seconds"`
}

type verifyRequest = accounts.VerifyCodeRequest

type authResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	APIKey   string `json:"api_key"`
	IsAdmin  bool   `json:"is_admin"`
	Created  bool   `json:"created"`
	Handle   string `json:"handle,omitempty"`
}

func NewUserHandler(database *sql.DB, service *accounts.Service, mailer signInCodeSender, publicBaseURL string, editions ...*edition.Edition) *UserHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	lim := ed.Limits()
	// ~12 req/min/IP (burst 20) across both auth routes; ~1.2/min/email
	// (burst 5). Generous for a human signing in, tight against automation.
	ipLimiter := newRateLimiterFor(lim.RateSigninIP)
	emailLimiter := newRateLimiterFor(lim.RateSigninEmail)

	checkLimiter := newRateLimiterFor(lim.RateHandleCheck)

	h := &UserHandler{
		ed:            ed,
		limitSet:      newLimitSet(lim),
		checkLimiter:  checkLimiter,
		database:      database,
		accounts:      service,
		mailer:        mailer,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		ipLimiter:     ipLimiter,
		emailLimiter:  emailLimiter,
		alerts:        newSignInAlerts(service, mailer, publicBaseURL),
	}
	h.httpCaches.rate("http.user.ipLimiter", ipLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.rate("http.user.emailLimiter", emailLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.rate("http.user.checkLimiter", checkLimiter, 10*time.Minute, 30*time.Minute)
	return h
}

func (h *UserHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h.registerPasswords(mux, authMiddleware, noticeMiddleware)
	// Account sign-in answers only on the app's own address (accountauth.go);
	// a hosted page's visitors use the visitor routes on the site's address.
	mux.Handle("POST /v1/auth", accountAuthOnly(h.publicBaseURL, noticeMiddleware(rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.requestSignIn)))))
	mux.Handle("POST /v1/auth/verify", accountAuthOnly(h.publicBaseURL, noticeMiddleware(rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.verifySignIn)))))
	mux.Handle("GET /v1/handles/check", rateLimitByIP(h.checkLimiter, http.HandlerFunc(h.handleCheck)))
	mux.Handle("GET /v1/me", noticeMiddleware(authMiddleware(http.HandlerFunc(h.me))))
	mux.Handle("POST /v1/me/api-key/rotate", noticeMiddleware(authMiddleware(http.HandlerFunc(h.rotateAPIKey))))
	mux.Handle("POST /v1/me/sign-out", noticeMiddleware(authMiddleware(http.HandlerFunc(h.signOut))))
	mux.Handle("DELETE /v1/sso/consents/{site}", accountAuthOnly(h.publicBaseURL, authMiddleware(http.HandlerFunc(h.forgetSSOConsent))))
	mux.Handle("GET /v1/me/keys", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listKeys))))
	mux.Handle("POST /v1/me/keys", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createKey))))
	mux.Handle("DELETE /v1/me/keys/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteKey))))
	mux.Handle("POST /v1/me/email", noticeMiddleware(authMiddleware(http.HandlerFunc(h.requestEmailChange))))
	mux.Handle("POST /v1/me/email/verify", noticeMiddleware(authMiddleware(http.HandlerFunc(h.verifyEmailChange))))
	mux.Handle("GET /v1/me/email/undo", rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.undoEmailChange)))
	mux.Handle("POST /v1/me/email/undo", rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.undoEmailChange)))
	mux.Handle("GET /v1/me/identities", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listIdentities))))
	mux.Handle("DELETE /v1/me/identities/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.unlinkIdentity))))
}

func (h *UserHandler) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if user.KeyHash == "" {
		// The env admin key, or a connected app acting for the person.
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "only an account API key can be rotated through this endpoint"})
		return
	}
	newKey, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	err = h.accounts.RotateKey(r.Context(), user.ID, user.KeyHash, newKey)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: auth.InvalidKeyMessage(), Code: "invalid_api_key"})
		return
	}
	if errors.Is(err, accounts.ErrDisconnectAfterRotation) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"api_key": newKey,
		"message": "API key rotated. The old key no longer works; update your agent or CLI with this new key. Connected apps (ChatGPT, Claude, Grok) were disconnected and sign-ins on your sites ended; reconnect the ones you use.",
	})
}

// requestSignIn handles POST /v1/auth: generates a 6-digit code and a
// magic-link token, stores them with a 15-minute TTL, and emails the user.
//
// The user row is NOT created here. It is created lazily on successful
// verification (see verifySignIn). This keeps requestSignIn doing identical
// work for every email — no DB-write side effect and no timing difference
// between known and unknown addresses — so it can't be used to enumerate
// registered users or to pollute the users table with unverified addresses.
//
// The API key is NEVER returned by this endpoint. Only /v1/auth/verify can
// hand it out, and only after the code/token round-trips through the user's
// mailbox.
func (h *UserHandler) requestSignIn(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	nonceHash, ok := nonceHashParam(req.NonceHash)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "nonce_hash must be base64url SHA-256 (43 characters)"})
		return
	}
	email, expires, status, body := issueEmailCode(r.Context(), h.database, h.mailer, h.emailLimiter, req.Email, h.publicBaseURL, "dashboard", sql.NullString{}, nonceHash, clientIP(r), h.geoBlock, h.authTokenTTL())
	if status != 0 {
		writeEmailCodeError(w, status, body)
		return
	}
	req.Email = email

	writeJSON(w, http.StatusAccepted, authChallengeResponse{
		Message:   signInCodeSentMessage(req.Email),
		Email:     req.Email,
		ExpiresIn: expires,
	})
}

// verifySignIn handles POST /v1/auth/verify in two shapes:
//   - {"token": "..."}       — magic-link sign-in (browser)
//   - {"email": "...", "code": "..."} — code entry (CLI / agent)
//
// On success, returns the user's API key. On failure, increments the attempt
// counter and returns 401; the token becomes useless after maxCodeAttempts.
func (h *UserHandler) verifySignIn(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if _, err := signInKeyName(req); err != nil {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}

	user, created, status, body := verifyEmailCode(r.Context(), h.database, h.emailLimiter, req, "dashboard", sql.NullString{}, h.ed.Identity().RandomHandles, h.authTokenTTL())
	if status != 0 {
		if status == http.StatusUnauthorized {
			writeJSON(w, status, signInUnauthorized(body))
		} else {
			writeEmailCodeError(w, status, body)
		}
		return
	}
	if created {
		source := signupForVerify(r.Context(), h.database, r, h.publicBaseURL, req.SignupClient, strings.TrimSpace(req.Token) != "")
		if _, err := h.accounts.GetSSOHop(r.Context(), req.SSOState, false); req.SSOState != "" && err == nil {
			source.Source = "visitor_sso"
		}
		recordSignup(r.Context(), h.database, user.ID, source)
	}

	h.finishSignIn(w, r, user, created, req)
}

func signInKeyName(req verifyRequest) (string, error) {
	keyName := strings.TrimSpace(req.Name)
	if n := strings.ToLower(keyName); n == model.KeyNameDashboard || n == model.KeyNameAgent {
		keyName = n
	} else {
		var err error
		keyName, err = normalizeKeyName(req.Name)
		if err != nil {
			return "", err
		}
	}
	if keyName == "" {
		keyName = model.KeyNameAgent
		if req.Token != "" {
			keyName = model.KeyNameDashboard
		}
	}
	return keyName, nil
}

func (h *UserHandler) finishSignIn(w http.ResponseWriter, r *http.Request, user model.User, created bool, req verifyRequest) {
	keyName, err := signInKeyName(req)
	if err != nil {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}

	// A suspended account gets no new key. It has proved its identity, so it is
	// told why.
	fresh, err := h.accounts.GetUserByID(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if fresh.Suspended {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":  auth.SuspendedMessage(fresh.SuspendedReason),
			"code":   "account_suspended",
			"reason": fresh.SuspendedReason,
		})
		return
	}

	// Keys are stored only as hashes, so each sign-in hands out a new key;
	// keys from earlier sign-ins keep working until the person rotates.
	apiKey, err := auth.GenerateAPIKey()
	if err == nil {
		err = h.accounts.AddSignInKey(r.Context(), user.ID, apiKey, keyName)
	}
	if errors.Is(err, model.ErrAccountSuspended) {
		// Suspended between the check above and the insert.
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": auth.SuspendedMessage(""), "code": "account_suspended", "reason": "",
		})
		return
	}
	if err != nil {
		log.Printf("auth: issue key after verify: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Emailed code, emailed link or Google (which ends here through its
	// one-time link): the owner hears about it. Browser sign-in then calls
	// /v1/sso/session so cookie replacement stays inside the SSO namespace.
	h.alerts.Alert(user.ID, r.UserAgent(), "")
	writeJSON(w, http.StatusOK, authResponse{
		ID:       user.ID,
		Username: user.Username,
		APIKey:   apiKey,
		IsAdmin:  user.IsAdmin,
		Created:  created,
		Handle:   user.Handle.String,
	})
}

func (h *UserHandler) me(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	if user.Team != nil {
		// A team credential (hosted events): who it acts for and the one
		// site it publishes, never the event's internal account.
		limit, _ := h.siteLimitFor(r.Context(), h.database, user)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":          user.Team.MemberID,
			"is_admin":    false,
			"handle":      user.Team.EventSlug,
			"max_site_mb": statedSiteMB(limit),
			"team": map[string]any{
				"event": user.Team.EventSlug,
				"team":  user.Team.TeamSlug,
				"site":  user.Team.TeamSlug,
			},
		})
		return
	}
	fresh, err := h.accounts.GetUserByID(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	user = &fresh
	alertsOn, err := h.accounts.SignInAlertsOn(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := meResponse{
		ID:           user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		Handle:       user.Handle.String,
		DisplayName:  user.DisplayName.String,
		SignInAlerts: &alertsOn,
	}
	if !user.IsAdmin {
		n := h.maxSitesFor(r.Context(), h.database, user)
		resp.MaxSites = &n
	}
	if h.fileUsage != nil && h.ed.Identity().PersonalSites {
		resp.FileUsage, err = h.fileUsage(r.Context(), user)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "could not measure account files"})
			return
		}
	}
	if h.passwordEnabled {
		has, err := h.accounts.HasPassword(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		resp.HasPassword = &has
	}
	resp.CanSetKeepVersions = h.limits.CanSetKeepVersions(accountHandles(r.Context(), h.database, user)...)
	resp.KeepVersions = h.limits.KeepVersionsFor(keepVersions, accountHandles(r.Context(), h.database, user)...)
	limit, _ := h.siteLimitFor(r.Context(), h.database, user)
	resp.MaxSiteMB = statedSiteMB(limit)
	if h.publicPage != nil && user.Handle.String != "" {
		resp.PublicPage = h.publicPage(user.Handle.String)
	}
	// A Simple Hack account has no personal sites (no_personal_sites), so it
	// has no own site address to wait for.
	if h.addressState != nil && user.Handle.String != "" && h.ed.Identity().PersonalSites {
		resp.Address = h.addressState(user.Handle.String)
	}
	home, hasHome, err := h.accounts.HomeSite(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if hasHome && h.ed.Identity().PersonalSites {
		resp.HomeSite = &home.Name
	}
	writeJSON(w, http.StatusOK, resp)
}

type meResponse struct {
	FileUsage          *accountFileUsage `json:"file_usage,omitempty"`
	CanSetKeepVersions bool              `json:"can_set_keep_versions"`
	KeepVersions       int               `json:"keep_versions"`
	HomeSite           *string           `json:"home_site"`
	DisplayName        string            `json:"display_name,omitempty"`
	ID                 string            `json:"id"`
	Username           string            `json:"username"`
	IsAdmin            bool              `json:"is_admin"`
	Handle             string            `json:"handle,omitempty"`
	// PublicPage is the account's public page: https://<handle>.<SITE_DOMAIN>/
	// (or the path address when person hosts are not the canonical address).
	PublicPage string `json:"public_page,omitempty"`
	// SignInAlerts: an email after each sign-in (default on).
	SignInAlerts *bool `json:"signin_alerts,omitempty"`
	// HasPassword: whether this account has a password set. Present only
	// when PASSWORD_SIGNIN is on (self-hosted installs only), so hosted
	// responses stay byte-identical with the field absent.
	HasPassword *bool `json:"has_password,omitempty"`
	// MaxSites is the sites this account may hold (MAX_SITES_OVERRIDES, else
	// MAX_SITES_PER_ACCOUNT); absent for admins, who have no limit.
	MaxSites *int `json:"max_sites,omitempty"`
	// MaxSiteMB is the largest site this account may deploy, in MB
	// (MAX_ARCHIVE_MB_OVERRIDES, else MAX_ARCHIVE_MB). Admins too.
	MaxSiteMB int `json:"max_site_mb"`
	// Address is the state of the account's own site address
	// (https://<site>.<handle>.<SITE_DOMAIN>/): ready, or waiting / failing
	// with a rough time, while sites are at <handle>.<SITE_DOMAIN>/<site>/.
	Address *addressState `json:"address,omitempty"`
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func generateNumericCode(digits int) (string, error) {
	max := big.NewInt(1)
	for i := 0; i < digits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	s := n.String()
	for len(s) < digits {
		s = "0" + s
	}
	return s, nil
}

func generateLinkToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// subtleConstantTimeEqual returns 1 if a == b, else 0. Avoids timing
// side-channels on the code comparison.
func subtleConstantTimeEqual(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	if v == 0 {
		return 1
	}
	return 0
}

func (h *UserHandler) SetFileUsage(s *SiteHandler) { h.fileUsage = s.accountFileUsage }
