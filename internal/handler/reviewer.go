package handler

// Reviewer sign-in: one designated account that can sign in to the connector's
// consent page with an email and a password, so a plugin-directory reviewer
// can connect without an emailed code, SMS or MFA (the directory requires
// demo credentials that work with none of those).
//
// It is off unless the operator sets both REVIEW_ACCOUNT_EMAIL and
// REVIEW_ACCOUNT_PASSWORD_HASH. It never enables password sign-in for anyone
// else: the only password that exists is this one, and it opens only the
// account named by REVIEW_ACCOUNT_EMAIL, which must be (or becomes, on first
// use) an ordinary non-admin account. It exists only on the consent page's
// endpoint, not on the dashboard or the API.
//
// The password is stored as PBKDF2-HMAC-SHA256 (crypto/pbkdf2, standard
// library, so no new dependency) at >= 600,000 iterations, the OWASP 2023
// figure for this construction. `simple-host review-account hash` makes one.

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

const (
	reviewerHashScheme    = "pbkdf2-sha256"
	reviewerMinIterations = 600_000
	reviewerIterations    = 600_000
	reviewerSaltBytes     = 16
	reviewerKeyBytes      = 32
	// ReviewerMinPasswordLength is enforced when hashing: the account is on
	// the public internet behind a password alone.
	ReviewerMinPasswordLength = 16
)

// reviewerHash is a parsed REVIEW_ACCOUNT_PASSWORD_HASH.
type reviewerHash struct {
	iterations int
	salt, key  []byte
}

// HashReviewerPassword returns the encoded hash for a reviewer password:
// pbkdf2-sha256$<iterations>$<salt, base64url>$<key, base64url>.
func HashReviewerPassword(password string) (string, error) {
	if len([]rune(password)) < ReviewerMinPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters", ReviewerMinPasswordLength)
	}
	salt := make([]byte, reviewerSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, reviewerIterations, reviewerKeyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return strings.Join([]string{reviewerHashScheme, strconv.Itoa(reviewerIterations), enc.EncodeToString(salt), enc.EncodeToString(key)}, "$"), nil
}

func parseReviewerHash(encoded string) (reviewerHash, error) {
	parts := strings.Split(strings.TrimSpace(encoded), "$")
	if len(parts) != 4 || parts[0] != reviewerHashScheme {
		return reviewerHash{}, errors.New("not a pbkdf2-sha256$<iterations>$<salt>$<key> hash (make one with `simple-host review-account hash`)")
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < reviewerMinIterations || iterations > 50_000_000 {
		return reviewerHash{}, fmt.Errorf("iterations must be a number from %d", reviewerMinIterations)
	}
	enc := base64.RawURLEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil || len(salt) < reviewerSaltBytes {
		return reviewerHash{}, errors.New("salt is malformed")
	}
	key, err := enc.DecodeString(parts[3])
	if err != nil || len(key) != reviewerKeyBytes {
		return reviewerHash{}, errors.New("key is malformed")
	}
	return reviewerHash{iterations: iterations, salt: salt, key: key}, nil
}

func (h reviewerHash) matches(password string) bool {
	got, err := pbkdf2.Key(sha256.New, password, h.salt, h.iterations, reviewerKeyBytes)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, h.key) == 1
}

// reviewerSignIn is the configured reviewer account, or nil when it is off.
type reviewerSignIn struct {
	email string
	hash  reviewerHash
	// perIP bounds one address's guesses; global bounds everyone's, so a
	// spread-out guessing run is as slow as a single one, and so is the CPU
	// each PBKDF2 derivation costs.
	perIP  *rateLimiter
	global *rateLimiter
}

// newReviewerSignIn validates the operator's settings. Both empty: off, no
// error. Anything else incomplete or malformed: off, with the reason, so a
// typo never half-enables it.
func newReviewerSignIn(email, encodedHash string) (*reviewerSignIn, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	encodedHash = strings.TrimSpace(encodedHash)
	if email == "" && encodedHash == "" {
		return nil, nil
	}
	if email == "" || encodedHash == "" {
		return nil, errors.New("set both REVIEW_ACCOUNT_EMAIL and REVIEW_ACCOUNT_PASSWORD_HASH, or neither")
	}
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n") {
		return nil, errors.New("REVIEW_ACCOUNT_EMAIL is not an email address")
	}
	hash, err := parseReviewerHash(encodedHash)
	if err != nil {
		return nil, fmt.Errorf("REVIEW_ACCOUNT_PASSWORD_HASH: %w", err)
	}
	return &reviewerSignIn{
		email:  email,
		hash:   hash,
		perIP:  newRateLimiter(10, 1.0/60),    // 10 tries, then one a minute
		global: newRateLimiter(30, 30.0/3600), // 30 tries, then 30 an hour, everyone together
	}, nil
}

// EnableReviewerSignIn turns on the reviewer account from the operator's
// settings. It reports whether it is on; a bad setting leaves it off and is
// logged, never fatal.
func (h *ConnectorHandler) EnableReviewerSignIn(email, encodedHash string) bool {
	rv, err := newReviewerSignIn(email, encodedHash)
	if err != nil {
		log.Printf("warning: reviewer sign-in disabled: %v", err)
		return false
	}
	h.reviewer = rv
	if rv != nil {
		rv.perIP.startCleanup(10*time.Minute, 2*time.Hour)
		log.Printf("reviewer sign-in enabled for one account on the connector consent page")
	}
	return rv != nil
}

// reviewerSignInHandler is POST /oauth/reviewer-signin {email, password}. On
// success it answers the account's API key, exactly as a verified email code
// does, so the consent page carries on the same way.
func (h *ConnectorHandler) reviewerSignInHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rv := h.reviewer
	if rv == nil {
		http.NotFound(w, r)
		return
	}
	// Same-origin only, like the consent decision.
	if o := r.Header.Get("Origin"); o != "" && !strings.EqualFold(strings.TrimRight(o, "/"), h.issuer) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin request refused"})
		return
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-site request refused"})
		return
	}
	if !rv.perIP.allow(clientIP(r)) || !rv.global.allow("all") {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "Too many attempts. Wait a few minutes and try again."})
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	// Derive before comparing the address too, so a wrong address and a wrong
	// password cost the same and read the same.
	passwordOK := len(body.Password) <= 1024 && rv.hash.matches(body.Password)
	emailOK := subtle.ConstantTimeCompare([]byte(email), []byte(rv.email)) == 1
	if !passwordOK || !emailOK {
		log.Printf("connector: reviewer sign-in refused ip=%s", clientIP(r))
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "That email and password did not match the reviewer account."})
		return
	}
	user, err := h.reviewerAccount(r.Context(), rv.email)
	if err != nil {
		log.Printf("connector: reviewer account: %v", err)
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "The reviewer account is not available."})
		return
	}
	log.Printf("connector: reviewer sign-in user_id=%s", user.ID)
	writeJSON(w, http.StatusOK, map[string]string{"api_key": user.APIKey, "username": user.Username})
}

// reviewerAccount returns the reviewer's ordinary account, creating it on first
// use the way a first verified email code does. An admin account is refused:
// the reviewer must never hold more than a normal person's power.
func (h *ConnectorHandler) reviewerAccount(ctx context.Context, email string) (db.User, error) {
	user, err := db.GetUserByUsername(ctx, h.database, email)
	if errors.Is(err, sql.ErrNoRows) {
		key, kerr := auth.GenerateAPIKey()
		if kerr != nil {
			return db.User{}, kerr
		}
		user, err = db.CreateUser(ctx, h.database, email, key, false)
		if err != nil && isUniqueViolation(err) {
			user, err = db.GetUserByUsername(ctx, h.database, email)
		}
	}
	if err != nil {
		return db.User{}, err
	}
	if user.IsAdmin || subtle.ConstantTimeCompare([]byte(user.APIKey), []byte(h.adminAPIKey)) == 1 {
		return db.User{}, errors.New("the reviewer account is an admin account; refusing")
	}
	if !user.Handle.Valid {
		assignHandle(ctx, h.database, user.ID, email)
		if fresh, ferr := db.GetUserByUsername(ctx, h.database, email); ferr == nil {
			user = fresh
		}
	}
	return user, nil
}
