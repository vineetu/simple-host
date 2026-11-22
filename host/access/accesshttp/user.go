package accesshttp

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

type UserHandler struct {
	httpCaches    httpx.Caches
	ed            *edition.Edition
	Limits        edition.Limits
	database      *sql.DB
	accounts      *accounts.Service
	mailer        signInCodeSender
	publicBaseURL string

	// Abuse limiters: ipLimiter caps requests per client IP across both auth
	// routes; emailLimiter caps challenges/verifies aimed at a single address
	// (mail-bomb + code-grinding defense). See ratelimit.go.
	IPLimiter    *httpx.RateLimiter
	EmailLimiter *httpx.RateLimiter
	// checkLimiter caps address checks per client IP (GET /v1/handles/check).
	checkLimiter                            *httpx.RateLimiter
	passwordEnabled, passwordSelfSignup     bool
	passwordLoginLimiter, passwordIPLimiter *httpx.RateLimiter

	// siteFiles is Access over the account's sites (SetSites): file usage,
	// the address and the public page GET /v1/me names.
	siteFiles *AccessHandler

	// alerts emails the owner after each sign-in (signin_alert.go).
	Alerts *SignInAlerts
	// reviewerEmail is the plugin reviewer account (REVIEW_ACCOUNT_EMAIL):
	// its email cannot be changed and it gets no sign-in alerts.
	reviewerEmail string
	// previewAccounts is PREVIEW_ACCOUNTS (lowercased): their email cannot
	// be changed (account_email.go).
	previewAccounts map[string]bool

	// geoBlock gates email-code sign-in and sign-up by the request's country
	// (SIGNUP_BLOCKED_COUNTRIES). Zero value: off. See signupgeo.go.
	geoBlock accounts.SignupGeoBlock

	// assets is the asset/chrome pipeline for ed and publicBaseURL (the
	// message pages in account_email.go); read it through pipeline().
	assets     *web.AssetPipeline
	assetsOnce sync.Once
}

// pipeline is h's asset pipeline. A bare &UserHandler{} (tests) gets one for
// its (possibly nil, so Host) edition on first use.
func (h *UserHandler) pipeline() *web.AssetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = web.NewAssetPipeline(h.ed, h.publicBaseURL)
		}
	})
	return h.assets
}

// SetSignupGeoBlock wires SIGNUP_BLOCKED_COUNTRIES for dashboard email-code sign-ins.
func (h *UserHandler) SetSignupGeoBlock(guard accounts.SignupGeoBlock) {
	h.geoBlock = guard
}

// SignInAlerts is the sign-in alert sender, shared with the connector's
// consent screen.
func (h *UserHandler) SignInAlerts() *SignInAlerts { return h.Alerts }

// SetReviewerEmail names the plugin reviewer account (REVIEW_ACCOUNT_EMAIL).
func (h *UserHandler) SetReviewerEmail(address string) {
	h.reviewerEmail = strings.TrimSpace(address)
	h.Alerts.reviewerEmail = h.reviewerEmail
}

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

type VerifyRequest = accounts.VerifyCodeRequest

type authResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	APIKey   string `json:"api_key"`
	IsAdmin  bool   `json:"is_admin"`
	Created  bool   `json:"created"`
	Handle   string `json:"handle,omitempty"`
	// Login is the same field GET /v1/me carries for a password account
	// (account_passwords.login): present on a password sign-in or signup,
	// so the dashboard/Hack UI showing this response directly never shows
	// a self sign-up's synthetic Username instead.
	Login string `json:"login,omitempty"`
}

func NewUserHandler(database *sql.DB, service *accounts.Service, mailer signInCodeSender, publicBaseURL string, editions ...*edition.Edition) *UserHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	lim := ed.Limits()
	// ~12 req/min/IP (burst 20) across both auth routes; ~1.2/min/email
	// (burst 5). Generous for a human signing in, tight against automation.
	ipLimiter := httpx.NewRateLimiterFor(lim.RateSigninIP.Burst, lim.RateSigninIP.Every)
	emailLimiter := httpx.NewRateLimiterFor(lim.RateSigninEmail.Burst, lim.RateSigninEmail.Every)

	checkLimiter := httpx.NewRateLimiterFor(lim.RateHandleCheck.Burst, lim.RateHandleCheck.Every)

	h := &UserHandler{
		ed:            ed,
		Limits:        lim,
		checkLimiter:  checkLimiter,
		database:      database,
		accounts:      service,
		mailer:        mailer,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		IPLimiter:     ipLimiter,
		EmailLimiter:  emailLimiter,
		Alerts:        newSignInAlerts(service, mailer, publicBaseURL),
	}
	h.httpCaches.Rate("http.user.ipLimiter", ipLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.user.emailLimiter", emailLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.user.checkLimiter", checkLimiter, 10*time.Minute, 30*time.Minute)
	return h
}

func (h *UserHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h.registerPasswords(mux, authMiddleware, noticeMiddleware)
	h.registerTeams(mux, authMiddleware, noticeMiddleware)
	// Account sign-in answers only on the app's own address (accountauth.go);
	// a hosted page's visitors use the visitor routes on the site's address.
	mux.Handle("POST /v1/auth", web.AccountAuthOnly(h.publicBaseURL, noticeMiddleware(httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.requestSignIn)))))
	mux.Handle("POST /v1/auth/verify", web.AccountAuthOnly(h.publicBaseURL, noticeMiddleware(httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.verifySignIn)))))
	mux.Handle("GET /v1/handles/check", httpx.RateLimitByIP(h.checkLimiter, auth.Optional(authMiddleware)(http.HandlerFunc(h.handleCheck))))
	mux.Handle("GET /v1/me", noticeMiddleware(authMiddleware(http.HandlerFunc(h.me))))
	mux.Handle("POST /v1/me/api-key/rotate", noticeMiddleware(authMiddleware(http.HandlerFunc(h.RotateAPIKey))))
	mux.Handle("POST /v1/me/sign-out", noticeMiddleware(authMiddleware(http.HandlerFunc(h.signOut))))
	mux.Handle("DELETE /v1/sso/consents/{site}", web.AccountAuthOnly(h.publicBaseURL, authMiddleware(http.HandlerFunc(h.forgetSSOConsent))))
	mux.Handle("GET /v1/me/keys", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listKeys))))
	mux.Handle("POST /v1/me/keys", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createKey))))
	mux.Handle("DELETE /v1/me/keys/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteKey))))
	mux.Handle("POST /v1/me/email", noticeMiddleware(authMiddleware(http.HandlerFunc(h.requestEmailChange))))
	mux.Handle("POST /v1/me/email/verify", noticeMiddleware(authMiddleware(http.HandlerFunc(h.verifyEmailChange))))
	mux.Handle("GET /v1/me/email/undo", httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.undoEmailChange)))
	mux.Handle("POST /v1/me/email/undo", httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.undoEmailChange)))
	mux.Handle("GET /v1/me/identities", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listIdentities))))
	mux.Handle("DELETE /v1/me/identities/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.unlinkIdentity))))
}

func (h *UserHandler) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if user.KeyHash == "" {
		// The env admin key, or a connected app acting for the person.
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "only an account API key can be rotated through this endpoint"})
		return
	}
	newKey, err := auth.GenerateAPIKey()
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	err = h.accounts.RotateKey(r.Context(), user.ID, user.KeyHash, newKey)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: auth.InvalidKeyMessage(), Code: "invalid_api_key"})
		return
	}
	if errors.Is(err, accounts.ErrDisconnectAfterRotation) {
		httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
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
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}

	nonceHash, ok := accounts.NonceHashParam(req.NonceHash)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "nonce_hash must be base64url SHA-256 (43 characters)"})
		return
	}
	email, expires, status, body := issueEmailCode(r.Context(), h.database, h.mailer, h.EmailLimiter, req.Email, h.publicBaseURL, "dashboard", sql.NullString{}, nonceHash, httpx.ClientIP(r), h.geoBlock, h.Limits.SigninCodeTTL)
	if status != 0 {
		writeEmailCodeError(w, status, body)
		return
	}
	req.Email = email

	httpx.WriteJSON(w, http.StatusAccepted, authChallengeResponse{
		Message:   SignInCodeSentMessage(req.Email),
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
	var req VerifyRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}

	if _, err := signInKeyName(req); err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}

	user, created, status, body := VerifyEmailCode(r.Context(), h.database, h.EmailLimiter, req, "dashboard", sql.NullString{}, h.ed.Identity().RandomHandles, h.Limits.SigninCodeTTL)
	if status != 0 {
		if status == http.StatusUnauthorized {
			httpx.WriteJSON(w, status, signInUnauthorized(body))
		} else {
			writeEmailCodeError(w, status, body)
		}
		return
	}
	if created {
		source := signupForVerify(r.Context(), h.database, r, h.publicBaseURL, req.SignupClient, strings.TrimSpace(req.Token) != "")
		h.accounts.RecordNewSignup(r.Context(), user.ID, source, req.SSOState)
	}

	h.finishSignIn(w, r, user, created, req)
}

func signInKeyName(req VerifyRequest) (string, error) {
	keyName := strings.TrimSpace(req.Name)
	if n := strings.ToLower(keyName); n == model.KeyNameDashboard || n == model.KeyNameAgent {
		keyName = n
	} else {
		var err error
		keyName, err = accounts.NormalizeKeyName(req.Name)
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

func (h *UserHandler) finishSignIn(w http.ResponseWriter, r *http.Request, user model.User, created bool, req VerifyRequest) {
	keyName, err := signInKeyName(req)
	if err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}

	apiKey, err := auth.GenerateAPIKey()
	login := ""
	if err == nil {
		login, err = h.accounts.IssueSignInKey(r.Context(), user.ID, apiKey, keyName, h.passwordEnabled)
	}
	var suspended *accounts.SuspendedError
	switch {
	case errors.As(err, &suspended):
		auth.WriteSuspended(w, suspended.Reason, "")
		return
	case errors.Is(err, model.ErrAccountSuspended):
		auth.WriteSuspended(w, "", "")
		return
	case err != nil:
		log.Printf("auth: issue key after verify: %v", err)
		httpx.WriteInternal(w)
		return
	}
	// Emailed code, emailed link or Google (which ends here through its
	// one-time link): the owner hears about it. Browser sign-in then calls
	// /v1/sso/session so cookie replacement stays inside the SSO namespace.
	h.Alerts.Alert(user.ID, r.UserAgent(), "")
	httpx.WriteJSON(w, http.StatusOK, authResponse{
		ID:       user.ID,
		Username: user.Username,
		APIKey:   apiKey,
		IsAdmin:  user.IsAdmin,
		Created:  created,
		Handle:   user.Handle.String,
		Login:    login,
	})
}

func (h *UserHandler) me(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	resp, err := h.meFor(r.Context(), user.ID)
	if errors.Is(err, errFileUsage) {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not measure account files"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// errFileUsage: the account's files could not be measured.
var errFileUsage = errors.New("could not measure account files")

// meFor is GET /v1/me for the account as it is now: who it is, its limits,
// how it signs in, and (where accounts own sites) its home site and teams.
func (h *UserHandler) meFor(ctx context.Context, userID string) (meResponse, error) {
	user, err := h.accounts.GetUserByID(ctx, userID)
	if err != nil {
		return meResponse{}, err
	}
	alertsOn, err := h.accounts.SignInAlertsOn(ctx, user.ID)
	if err != nil {
		return meResponse{}, err
	}
	resp := meResponse{
		ID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin,
		Handle: user.Handle.String, DisplayName: user.DisplayName.String, SignInAlerts: &alertsOn,
	}
	if err := h.meLimits(ctx, &user, &resp); err != nil {
		return resp, err
	}
	if h.passwordEnabled {
		login, has, err := h.accounts.PasswordLogin(ctx, user.ID)
		if err != nil {
			return resp, err
		}
		resp.HasPassword, resp.Login = &has, login
	}
	if h.siteFiles != nil && user.Handle.String != "" {
		resp.PublicPage = h.siteFiles.Hosts.Addressing().PersonPageURL(user.Handle.String)
	}
	return resp, h.mePersonalSites(ctx, &user, &resp)
}

// meLimits are the account's site count, file use, kept versions and site
// size limits.
func (h *UserHandler) meLimits(ctx context.Context, user *model.User, resp *meResponse) error {
	if !user.IsAdmin {
		n := accounts.New(h.database).MaxSites(ctx, user, h.Limits)
		resp.MaxSites = &n
	}
	if h.siteFiles != nil && h.ed.Identity().PersonalSites {
		usage, err := sites.FileUsage(ctx, h.database, h.siteFiles.Disk, h.siteFiles.Limits, user)
		if err != nil {
			return errFileUsage
		}
		resp.FileUsage = usage
	}
	resp.CanSetKeepVersions = h.Limits.CanSetKeepVersions(accounts.New(h.database).SiteAccountHandles(ctx, user)...)
	resp.KeepVersions = h.Limits.KeepVersionsFor(web.DefaultKeepVersions, accounts.New(h.database).SiteAccountHandles(ctx, user)...)
	limit, _ := accounts.New(h.database).SiteLimit(ctx, user, h.Limits, sites.Upload.Body)
	resp.MaxSiteMB = sites.StatedSiteMB(limit)
	return nil
}

// mePersonalSites is the own-address state, home site, teams and team
// invitations of an account that owns its sites. A Simple Hack account has
// no personal sites (no_personal_sites), so it has no own site address to
// wait for.
func (h *UserHandler) mePersonalSites(ctx context.Context, user *model.User, resp *meResponse) error {
	if !h.ed.Identity().PersonalSites {
		return nil
	}
	if h.siteFiles != nil && user.Handle.String != "" {
		resp.Address = h.siteFiles.Hosts.AddressState(user.Handle.String)
	}
	home, hasHome, err := appsites.HomeSite(ctx, h.database, user.ID)
	if err != nil {
		return err
	}
	if hasHome {
		resp.HomeSite = &home.Name
	}
	teams, err := h.accounts.TeamsOf(ctx, user.ID)
	if err != nil {
		return err
	}
	resp.Teams = teamsForMe(teams)
	resp.TeamInvitations, err = accounts.MyInvitations(ctx, h.database, user)
	return err
}

type meResponse struct {
	FileUsage          *sites.AccountFileUsage `json:"file_usage,omitempty"`
	CanSetKeepVersions bool                    `json:"can_set_keep_versions"`
	KeepVersions       int                     `json:"keep_versions"`
	HomeSite           *string                 `json:"home_site"`
	DisplayName        string                  `json:"display_name,omitempty"`
	ID                 string                  `json:"id"`
	Username           string                  `json:"username"`
	IsAdmin            bool                    `json:"is_admin"`
	Handle             string                  `json:"handle,omitempty"`
	// PublicPage is the account's public page: https://<handle>.<SITE_DOMAIN>/
	// (or the path address when person hosts are not the canonical address).
	PublicPage string `json:"public_page,omitempty"`
	// SignInAlerts: an email after each sign-in (default on).
	SignInAlerts *bool `json:"signin_alerts,omitempty"`
	// HasPassword: whether this account has a password set. Present only
	// when PASSWORD_SIGNIN is on (self-hosted installs only), so hosted
	// responses stay byte-identical with the field absent.
	HasPassword *bool `json:"has_password,omitempty"`
	// Login is what a password account actually signs in with (its own
	// account_passwords.login), present whenever HasPassword is true. A
	// self sign-up's Username is a synthetic, never-shown value instead
	// (CreatePasswordAccount), so the dashboard and Hack account screen
	// must show Login, not Username, for a password account.
	Login string `json:"login,omitempty"`
	// MaxSites is the sites this account may hold (MAX_SITES_OVERRIDES, else
	// MAX_SITES_PER_ACCOUNT); absent for admins, who have no limit.
	MaxSites *int `json:"max_sites,omitempty"`
	// MaxSiteMB is the largest site this account may deploy, in MB
	// (MAX_ARCHIVE_MB_OVERRIDES, else MAX_ARCHIVE_MB). Admins too.
	MaxSiteMB int `json:"max_site_mb"`
	// Address is the state of the account's own site address
	// (https://<site>.<handle>.<SITE_DOMAIN>/): ready, or waiting / failing
	// with a rough time, while sites are at <handle>.<SITE_DOMAIN>/<site>/.
	Address *sites.AddressState `json:"address,omitempty"`
	// Teams are the teams this person belongs to: each one's id, which never
	// changes, and its handle (owner=<handle> on the site routes).
	Teams []map[string]string `json:"teams,omitempty"`
	// TeamInvitations are the invitations to join a team waiting for this
	// person's answer (POST /v1/me/team-invitations/{id}).
	TeamInvitations []model.TeamInvitation `json:"team_invitations,omitempty"`
}

// SetSites wires the account's file usage (GET /v1/me) and the site locks and
// file removal of deleting a team, through Access.
func (h *UserHandler) SetSites(a *AccessHandler) { h.siteFiles = a }

func (h *UserHandler) HTTPCaches() workers.Owner { return &h.httpCaches }
