package handler

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

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/model"
)

// EmailLimiter shares the account sign-in throttle with hosted-page sign-in.
func (h *UserHandler) EmailLimiter() *rateLimiter { return h.emailLimiter }

// emailSignInEnabled is EMAIL_SIGNIN (contract default "on"). SetEmailSignIn
// wires it once at startup from config.Config.EmailSignInEnabled(). off
// refuses every email-code sign-in request (issueEmailCode) and every
// email+code verification (verifyEmailCode's req.Email+req.Code shape); the
// magic-link token shape (req.Token) is untouched, since it is also how the
// OAuth owner flow's one-time token is redeemed and must keep working
// whether or not email sign-in itself is on.
var emailSignInEnabled = true

// SetEmailSignIn wires EMAIL_SIGNIN. Call once at startup.
func SetEmailSignIn(on bool) { emailSignInEnabled = on }

// errEmailSignInOff is the contract's fixed refusal body.
var errEmailSignInOff = errorResponse{Error: "Email sign-in is off on this server. Sign in with your company account.", Code: "email_signin_off"}

func writeEmailCodeError(w http.ResponseWriter, status int, body errorResponse) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	writeJSON(w, status, body)
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
func issueEmailCode(ctx context.Context, database *sql.DB, mailer signInCodeSender, limiter *rateLimiter, address, linkBase string, purpose string, siteID, nonceHash sql.NullString, ip string, geoBlock signupGeoBlock, authTTL time.Duration) (string, int, int, errorResponse) {
	address, expires, status, failure := accounts.New(database).IssueEmailCode(ctx, mailer, limiter.allow, address, linkBase, purpose, siteID, nonceHash, ip, geoBlock, authTTL, emailSignInEnabled, codeMessages(), codeHandleActions(database))
	return address, expires, status, codeFailureResponse(failure)
}

// verifyEmailCode shares token verification and lazy account creation. Only the
// dashboard caller accepts a magic-link token in req.
func verifyEmailCode(ctx context.Context, database *sql.DB, limiter *rateLimiter, req verifyRequest, purpose string, siteID sql.NullString, randomHandles bool, authTTL time.Duration) (model.User, bool, int, errorResponse) {
	user, created, status, failure := accounts.New(database).VerifyEmailCode(ctx, limiter.allow, req, purpose, siteID, randomHandles, authTTL, emailSignInEnabled, codeMessages(), codeHandleActions(database))
	return user, created, status, codeFailureResponse(failure)
}

// emailLimiterKey groups plus aliases without changing the stored or sent address.
func emailLimiterKey(address string) string {
	local, domain, ok := strings.Cut(strings.ToLower(address), "@")
	if !ok {
		return strings.ToLower(address)
	}
	local, _, _ = strings.Cut(local, "+")
	return local + "@" + domain
}

// errLinkOtherBrowser is shown when a sign-in link is opened where it was not
// requested.
const errLinkOtherBrowser = "Open this link on the device where you asked for it, or type the 6-digit code from the email."

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
