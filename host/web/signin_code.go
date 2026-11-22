package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// SetEmailSignIn wires EMAIL_SIGNIN. Call once at startup.
func SetEmailSignIn(on bool) { EmailSignInEnabled = on }

func WriteEmailCodeError(w http.ResponseWriter, status int, body httpx.ErrorResponse) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	httpx.WriteJSON(w, status, body)
}

// IssueEmailCode is accounts.IssueEmailCode with this transport's error body.
func IssueEmailCode(ctx context.Context, database *sql.DB, mailer accounts.CodeSender, limiter *httpx.RateLimiter, address, linkBase string, purpose string, siteID, nonceHash sql.NullString, ip string, geoBlock accounts.SignupGeoBlock, authTTL time.Duration) (string, int, int, httpx.ErrorResponse) {
	address, expires, status, failure := accounts.New(database).IssueEmailCode(ctx, mailer, limiter.Allow, address, linkBase, purpose, siteID, nonceHash, ip, geoBlock, authTTL, EmailSignInEnabled)
	return address, expires, status, codeFailureResponse(failure, authTTL)
}

// verifyEmailCode shares token verification and lazy account creation. Only the
// dashboard caller accepts a magic-link token in req.
func VerifyEmailCode(ctx context.Context, database *sql.DB, limiter *httpx.RateLimiter, req accounts.VerifyCodeRequest, purpose string, siteID sql.NullString, randomHandles bool, authTTL time.Duration) (model.User, bool, int, httpx.ErrorResponse) {
	user, created, status, failure := accounts.New(database).VerifyEmailCode(ctx, limiter.Allow, req, purpose, siteID, randomHandles, authTTL, EmailSignInEnabled)
	return user, created, status, codeFailureResponse(failure, authTTL)
}

// NonceMatches reports whether base64url(SHA-256(nonce)) equals hash, in
// constant time.
func NonceMatches(nonce, hash string) bool {
	if nonce == "" || len(nonce) > 256 {
		return false
	}
	sum := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(hash)) == 1
}

// SignupBlockedCode is the stable error code for a refused sign-in/sign-up.
const SignupBlockedCode = "signup_unavailable_region"

// SignupBlockedError is the JSON body for a refused sign-in/sign-up (403).
var SignupBlockedError = httpx.ErrorResponse{Error: SignupBlockedMessage, Code: SignupBlockedCode}

// codeFailureResponse words an emailed-code refusal for this transport;
// authTTL is the code lifetime an expired one names.
func codeFailureResponse(f accounts.CodeFailure, authTTL time.Duration) httpx.ErrorResponse {
	switch f.Refusal {
	case accounts.CodeExpired:
		return signInCodeExpired(authTTL)
	case accounts.CodeWrong:
		return signInCodeWrong(*f.TriesLeft)
	case accounts.CodeUsedUp:
		return signInCodeUsedUp()
	case accounts.CodeLinkOtherBrowser:
		return signInLinkOtherBrowser()
	case accounts.CodeRateLimited:
		return SignInRateLimited()
	case accounts.CodeInvalidEmail:
		return signInInvalidEmail()
	case accounts.CodeSendFailed:
		return signInSendFailed()
	case accounts.CodeSignInOff:
		return ErrEmailSignInOff
	case accounts.CodeBlocked:
		return SignupBlockedError
	}
	return httpx.ErrorResponse{Error: f.Error, Code: f.Code, Reason: f.Reason, Address: f.Address, SuggestedHandle: f.SuggestedHandle, TriesLeft: f.TriesLeft}
}

const (
	SignInReasonWrong   = "code_wrong"
	SignInReasonUsedUp  = "code_used_up"
	SignInReasonExpired = "code_expired"
	SignInReasonLink    = "link_other_browser"
)

func signInCodeTTLMinutes(authTTL time.Duration) int {
	min := int(authTTL / time.Minute)
	if min < 1 {
		min = 1
	}
	return min
}

func signInCodeWrong(triesLeft int) httpx.ErrorResponse {
	msg := SignInMessages["code_wrong"]
	if triesLeft == 1 {
		msg = "That code is not right. 1 try left."
	} else {
		msg = strings.ReplaceAll(msg, "{n}", strconv.Itoa(triesLeft))
	}
	n := triesLeft
	return httpx.ErrorResponse{Error: msg, Reason: SignInReasonWrong, TriesLeft: &n}
}

func signInCodeUsedUp() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: SignInMessages["code_used_up"], Reason: SignInReasonUsedUp}
}

func signInCodeExpired(authTTL time.Duration) httpx.ErrorResponse {
	msg := strings.ReplaceAll(SignInMessages["code_expired"], "{min}", strconv.Itoa(signInCodeTTLMinutes(authTTL)))
	return httpx.ErrorResponse{Error: msg, Reason: SignInReasonExpired}
}

func signInLinkOtherBrowser() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: ErrLinkOtherBrowser, Reason: SignInReasonLink}
}

func SignInRateLimited() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: SignInMessages["rate_limited"], Code: "rate_limited"}
}

func signInInvalidEmail() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: SignInMessages["invalid_email"], Code: "invalid_email"}
}

func signInSendFailed() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: SignInMessages["send_failed"], Code: "send_failed"}
}

func SignInEmailUnavailable() httpx.ErrorResponse {
	return httpx.ErrorResponse{Error: SignInMessages["email_unavailable"], Code: "email_unavailable"}
}

func SignInCodeSentMessage(email string) string {
	return "We sent a 6-digit code to " + email + "."
}

func SignInUnauthorized(body httpx.ErrorResponse) httpx.ErrorResponse {
	body.Code = "invalid_code"
	return body
}

// AuthRequest asks for an emailed sign-in code.
type AuthRequest struct {
	Email string `json:"email"`
	// NonceHash is base64url(SHA-256(nonce)) of a nonce the requesting page
	// keeps. With it, the email carries a sign-in link that works only in
	// that browser; without it, the email carries the code alone.
	NonceHash string `json:"nonce_hash"`
}

// AuthChallengeResponse says a code was sent.
type AuthChallengeResponse struct {
	Message   string `json:"message"`
	Email     string `json:"email"`
	ExpiresIn int    `json:"expires_in_seconds"`
}
