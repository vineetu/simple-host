package accesshttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// SetEmailSignIn wires EMAIL_SIGNIN. Call once at startup.
func SetEmailSignIn(on bool) { web.EmailSignInEnabled = on }

func writeEmailCodeError(w http.ResponseWriter, status int, body httpx.ErrorResponse) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	httpx.WriteJSON(w, status, body)
}

// issueEmailCode is accounts.IssueEmailCode with this transport's messages,
// handle actions and error body.
func issueEmailCode(ctx context.Context, database *sql.DB, mailer signInCodeSender, limiter *httpx.RateLimiter, address, linkBase string, purpose string, siteID, nonceHash sql.NullString, ip string, geoBlock accounts.SignupGeoBlock, authTTL time.Duration) (string, int, int, httpx.ErrorResponse) {
	address, expires, status, failure := accounts.New(database).IssueEmailCode(ctx, mailer, limiter.Allow, address, linkBase, purpose, siteID, nonceHash, ip, geoBlock, authTTL, web.EmailSignInEnabled, codeMessages(), codeHandleActions(database))
	return address, expires, status, codeFailureResponse(failure)
}

// verifyEmailCode shares token verification and lazy account creation. Only the
// dashboard caller accepts a magic-link token in req.
func VerifyEmailCode(ctx context.Context, database *sql.DB, limiter *httpx.RateLimiter, req VerifyRequest, purpose string, siteID sql.NullString, randomHandles bool, authTTL time.Duration) (model.User, bool, int, httpx.ErrorResponse) {
	user, created, status, failure := accounts.New(database).VerifyEmailCode(ctx, limiter.Allow, req, purpose, siteID, randomHandles, authTTL, web.EmailSignInEnabled, codeMessages(), codeHandleActions(database))
	return user, created, status, codeFailureResponse(failure)
}

// nonceHashRe is the shape of a SHA-256 digest in unpadded base64url.
var nonceHashRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// nonceHashParam validates an optional nonce hash sent by a sign-in page.
func nonceHashParam(s string) (sql.NullString, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{}, true
	}
	if !nonceHashRe.MatchString(s) {
		return sql.NullString{}, false
	}
	return sql.NullString{String: s, Valid: true}, true
}

// nonceMatches reports whether base64url(SHA-256(nonce)) equals hash, in
// constant time.
func nonceMatches(nonce, hash string) bool {
	if nonce == "" || len(nonce) > 256 {
		return false
	}
	sum := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(hash)) == 1
}

// signupBlockedCode is the stable error code for a refused sign-in/sign-up.
const signupBlockedCode = "signup_unavailable_region"

// signupBlockedError is the JSON body for a refused sign-in/sign-up (403).
var signupBlockedError = httpx.ErrorResponse{Error: web.SignupBlockedMessage, Code: signupBlockedCode}
