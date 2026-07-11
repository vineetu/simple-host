package handler

import (
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
	"time"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
)

var validEmail = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

const (
	authTokenTTL    = 15 * time.Minute
	maxCodeAttempts = 3
)

type UserHandler struct {
	database      *sql.DB
	mailer        email.Sender
	publicBaseURL string

	// Abuse limiters: ipLimiter caps requests per client IP across both auth
	// routes; emailLimiter caps challenges/verifies aimed at a single address
	// (mail-bomb + code-grinding defense). See ratelimit.go.
	ipLimiter    *rateLimiter
	emailLimiter *rateLimiter
}

type authRequest struct {
	Email string `json:"email"`
}

type authChallengeResponse struct {
	Message  string `json:"message"`
	Email    string `json:"email"`
	ExpiresIn int   `json:"expires_in_seconds"`
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
	Token string `json:"token"`
}

type authResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	APIKey   string `json:"api_key"`
	IsAdmin  bool   `json:"is_admin"`
	Created  bool   `json:"created"`
	Handle   string `json:"handle,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewUserHandler(database *sql.DB, mailer email.Sender, publicBaseURL string) *UserHandler {
	// ~12 req/min/IP (burst 20) across both auth routes; ~1.2/min/email
	// (burst 5). Generous for a human signing in, tight against automation.
	ipLimiter := newRateLimiter(20, 0.2)
	emailLimiter := newRateLimiter(5, 0.02)
	ipLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	emailLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	return &UserHandler{
		database:      database,
		mailer:        mailer,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		ipLimiter:     ipLimiter,
		emailLimiter:  emailLimiter,
	}
}

func (h *UserHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/auth", noticeMiddleware(rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.requestSignIn))))
	mux.Handle("POST /v1/auth/verify", noticeMiddleware(rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.verifySignIn))))
	mux.Handle("GET /v1/me", noticeMiddleware(authMiddleware(http.HandlerFunc(h.me))))
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

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || !validEmail.MatchString(req.Email) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "valid email is required"})
		return
	}

	// Per-address throttle: stops one email from being mail-bombed even if the
	// attacker rotates source IPs.
	if !h.emailLimiter.allow(req.Email) {
		tooManyRequests(w)
		return
	}

	code, err := generateNumericCode(6)
	if err != nil {
		log.Printf("auth: generateNumericCode: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	linkToken, err := generateLinkToken(24)
	if err != nil {
		log.Printf("auth: generateLinkToken: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	expiresAt := time.Now().Add(authTokenTTL)
	if err := db.CreateAuthToken(r.Context(), h.database, req.Email, code, linkToken, expiresAt); err != nil {
		log.Printf("auth: CreateAuthToken: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	link := h.publicBaseURL + "/?token=" + linkToken
	if err := h.mailer.SendSignInCode(req.Email, code, link); err != nil {
		// Don't expose details to the caller, but log loudly — this is the
		// most likely failure mode in production (Resend misconfig, DNS, etc).
		log.Printf("auth: mailer.SendSignInCode(%s): %v", req.Email, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not send verification email"})
		return
	}

	writeJSON(w, http.StatusAccepted, authChallengeResponse{
		Message:   "Check your email for a sign-in code.",
		Email:     req.Email,
		ExpiresIn: int(authTokenTTL.Seconds()),
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

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Code = strings.ReplaceAll(strings.TrimSpace(req.Code), "-", "")
	req.Token = strings.TrimSpace(req.Token)

	var tok db.AuthToken
	var err error

	switch {
	case req.Token != "":
		tok, err = db.GetAuthTokenByLink(r.Context(), h.database, req.Token)
	case req.Email != "" && req.Code != "":
		tok, err = db.GetLatestAuthTokenForEmail(r.Context(), h.database, req.Email)
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "supply token or email+code"})
		return
	}

	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired code"})
		return
	}
	if err != nil {
		log.Printf("auth: lookup token: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if tok.Attempts >= maxCodeAttempts {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "too many attempts, request a new code"})
		return
	}

	// For code-entry path, compare submitted code against the stored value.
	if req.Token == "" {
		// Throttle guesses against a specific address across tokens, on top of
		// the per-token maxCodeAttempts cap.
		if !h.emailLimiter.allow(tok.Email) {
			tooManyRequests(w)
			return
		}
		if subtleConstantTimeEqual(req.Code, tok.Code) != 1 {
			_ = db.IncrementAuthTokenAttempts(r.Context(), h.database, tok.ID)
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired code"})
			return
		}
	}

	// Lazily create the user on first successful verification. requestSignIn no
	// longer pre-creates the row, so this is where a new account is born.
	created := false
	user, err := db.GetUserByUsername(r.Context(), h.database, tok.Email)
	if errors.Is(err, sql.ErrNoRows) {
		apiKey, kerr := auth.GenerateAPIKey()
		if kerr != nil {
			log.Printf("auth: GenerateAPIKey: %v", kerr)
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		user, err = db.CreateUser(r.Context(), h.database, tok.Email, apiKey, false)
		if err != nil {
			if isUniqueViolation(err) {
				// Concurrent verify won the race — re-fetch the existing row.
				user, err = db.GetUserByUsername(r.Context(), h.database, tok.Email)
			}
			if err != nil {
				log.Printf("auth: CreateUser after verify: %v", err)
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
		} else {
			created = true
		}
	} else if err != nil {
		log.Printf("auth: GetUserByUsername after verify: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Assign a URL-safe handle for new users (or lazy-backfill older rows that
	// still have a NULL handle). ClaimHandle only writes WHERE handle IS NULL,
	// so existing handles are never overwritten.
	if created || !user.Handle.Valid {
		h.assignHandle(r.Context(), user.ID, tok.Email)
		if refetched, rerr := db.GetUserByUsername(r.Context(), h.database, tok.Email); rerr == nil {
			user = refetched
		} else {
			log.Printf("auth: refetch after assignHandle: %v", rerr)
		}
	}

	if err := db.MarkAuthTokenUsed(r.Context(), h.database, tok.ID); err != nil {
		log.Printf("auth: MarkAuthTokenUsed: %v", err)
	}

	writeJSON(w, http.StatusOK, authResponse{
		ID:       user.ID,
		Username: user.Username,
		APIKey:   user.APIKey,
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

	writeJSON(w, http.StatusOK, meResponse{
		ID:       user.ID,
		Username: user.Username,
		IsAdmin:  user.IsAdmin,
		Handle:   user.Handle.String,
	})
}

type meResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	Handle   string `json:"handle,omitempty"`
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
