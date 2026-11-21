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

// issueEmailCode normalizes the address and sends a challenge. The email
// carries a sign-in link only for a dashboard request that sent a nonce hash
// (the link then works only in the browser holding that nonce); otherwise it
// carries the code alone.
//
// ip and geoBlock gate every request, new address or existing: when the
// request's country is on SIGNUP_BLOCKED_COUNTRIES, no code is ever sent and
// no auth_token row is created — there is no "does this address already have
// an account" check first (deliberately: that lookup is itself the thing a
// probe could use to enumerate addresses, and the owner decided a blanket
// refusal is simpler and sufficient). geoBlock's zero value (the default,
// feature off) never looks anything up, so this is a no-op for every caller
// until the knob is set.
func issueEmailCode(ctx context.Context, database *sql.DB, mailer signInCodeSender, limiter *httpx.RateLimiter, address, linkBase string, purpose string, siteID, nonceHash sql.NullString, ip string, geoBlock signupGeoBlock, authTTL time.Duration) (string, int, int, httpx.ErrorResponse) {
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
