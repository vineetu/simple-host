package signin

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

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
func (h *Handler) requestSignIn(w http.ResponseWriter, r *http.Request) {
	var req web.AuthRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}

	nonceHash, ok := accounts.NonceHashParam(req.NonceHash)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "nonce_hash must be base64url SHA-256 (43 characters)"})
		return
	}
	email, expires, status, body := web.IssueEmailCode(r.Context(), h.database, h.mailer, h.EmailLimiter, req.Email, h.publicBaseURL, "dashboard", sql.NullString{}, nonceHash, httpx.ClientIP(r), h.geoBlock, h.Limits.SigninCodeTTL)
	if status != 0 {
		web.WriteEmailCodeError(w, status, body)
		return
	}
	req.Email = email

	httpx.WriteJSON(w, http.StatusAccepted, web.AuthChallengeResponse{
		Message:   web.SignInCodeSentMessage(req.Email),
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
func (h *Handler) verifySignIn(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}

	if _, err := signInKeyName(req); err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}

	user, created, status, body := web.VerifyEmailCode(r.Context(), h.database, h.EmailLimiter, req, "dashboard", sql.NullString{}, h.ed.Identity().RandomHandles, h.Limits.SigninCodeTTL)
	if status != 0 {
		if status == http.StatusUnauthorized {
			httpx.WriteJSON(w, status, web.SignInUnauthorized(body))
		} else {
			web.WriteEmailCodeError(w, status, body)
		}
		return
	}
	if created {
		source := web.SignupForVerify(r.Context(), h.database, r, h.publicBaseURL, req.SignupClient, strings.TrimSpace(req.Token) != "")
		RecordNewSignup(r.Context(), h.database, user.ID, source, req.SSOState)
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

func (h *Handler) finishSignIn(w http.ResponseWriter, r *http.Request, user model.User, created bool, req VerifyRequest) {
	keyName, err := signInKeyName(req)
	if err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}

	apiKey, err := auth.GenerateAPIKey()
	login := ""
	if err == nil {
		login, err = IssueSignInKey(r.Context(), h.database, user.ID, apiKey, keyName, h.passwordEnabled)
	}
	var suspended *SuspendedError
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

// Handler serves the account's own sign-in on the app's address: an emailed
// code or link (POST /v1/auth, /v1/auth/verify) and, where it is on, a
// password (password.go).
type Handler struct {
	database      *sql.DB
	accounts      *accounts.Service
	ed            *edition.Edition
	Limits        edition.Limits
	mailer        accounts.CodeSender
	publicBaseURL string
	httpCaches    *httpx.Caches

	// IPLimiter caps requests per client IP across both auth routes;
	// EmailLimiter caps challenges/verifies aimed at one address.
	IPLimiter, EmailLimiter *httpx.RateLimiter

	passwordEnabled, passwordSelfSignup     bool
	passwordLoginLimiter, passwordIPLimiter *httpx.RateLimiter

	// Alerts emails the owner after each sign-in.
	Alerts *auth.SignInAlerts
	// geoBlock gates email-code sign-in and sign-up by the request's country.
	geoBlock accounts.SignupGeoBlock
}

// New builds the sign-in routes' handler; caches is where its limiters are
// swept (the account handler's).
func New(database *sql.DB, service *accounts.Service, mailer accounts.CodeSender, publicBaseURL string, ed *edition.Edition, ipLimiter, emailLimiter *httpx.RateLimiter, alerts *auth.SignInAlerts, caches *httpx.Caches) *Handler {
	return &Handler{database: database, accounts: service, ed: ed, Limits: ed.Limits(), mailer: mailer, publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		httpCaches: caches, IPLimiter: ipLimiter, EmailLimiter: emailLimiter, Alerts: alerts}
}

// SetSignupGeoBlock wires SIGNUP_BLOCKED_COUNTRIES for email-code sign-ins.
func (h *Handler) SetSignupGeoBlock(guard accounts.SignupGeoBlock) { h.geoBlock = guard }

// PasswordEnabled reports whether PASSWORD_SIGNIN is on.
func (h *Handler) PasswordEnabled() bool { return h.passwordEnabled }

// Register mounts the sign-in routes. Account sign-in answers only on the
// app's own address (web.AccountAuthOnly); a hosted page's visitors use the
// visitor routes on the site's address.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h.registerPasswords(mux, authMiddleware, noticeMiddleware)
	mux.Handle("POST /v1/auth", web.AccountAuthOnly(h.publicBaseURL, noticeMiddleware(httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.requestSignIn)))))
	mux.Handle("POST /v1/auth/verify", web.AccountAuthOnly(h.publicBaseURL, noticeMiddleware(httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.verifySignIn)))))
}
