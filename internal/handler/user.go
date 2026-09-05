package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Message   string `json:"message"`
	Email     string `json:"email"`
	ExpiresIn int    `json:"expires_in_seconds"`
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
	mux.Handle("POST /v1/me/api-key/rotate", noticeMiddleware(authMiddleware(http.HandlerFunc(h.rotateAPIKey))))
}

func (h *UserHandler) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if user.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "the server admin key cannot be rotated through this endpoint"})
		return
	}
	newKey, err := auth.GenerateAPIKey()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := db.RotateAPIKey(r.Context(), h.database, user.ID, user.APIKey, newKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"api_key": newKey,
		"message": "API key rotated. The old key no longer works; update your agent or CLI with this new key.",
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

	email, expires, status, body := issueEmailCode(r.Context(), h.database, h.mailer, h.emailLimiter, req.Email, h.publicBaseURL, "dashboard", sql.NullString{})
	if status != 0 {
		writeEmailCodeError(w, status, body)
		return
	}
	req.Email = email

	writeJSON(w, http.StatusAccepted, authChallengeResponse{
		Message:   "Check your email for a sign-in code.",
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

	user, created, status, body := verifyEmailCode(r.Context(), h.database, h.emailLimiter, req, "dashboard", sql.NullString{})
	if status != 0 {
		if status == http.StatusUnauthorized {
			writeJSON(w, status, map[string]string{"error": body.Error, "code": "invalid_code"})
		} else {
			writeEmailCodeError(w, status, body)
		}
		return
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
