package handler

import (
	"strconv"
	"strings"
	"time"
)

// signInMessages is the one table of sign-in sentences. The served signin.js
// gets this map as JSON (see transformStatic), so the server and the card
// speak the same words. Keys with {n} or {min} are filled by the server; the
// client falls back to the text without the number.
var signInMessages = map[string]string{
	"code_wrong":         "That code is not right. {n} tries left.",
	"code_used_up":       "Too many wrong tries, so this code no longer works. Send a new one to try again.",
	"code_expired":       "That code has expired or was already used. Codes last {min} minutes. Send a new code.",
	"rate_limited":       "Too many tries. Wait a minute, then try again.",
	"invalid_email":      "That email address does not look right.",
	"send_failed":        "We could not send the email just now. Try again in a minute.",
	"signup_blocked":     signupBlockedMessage,
	"email_signin_off":   errEmailSignInOff.Error,
	"email_unavailable":  "This server cannot send sign-in codes. Use Google if it is offered, or ask whoever runs this server.",
	"link_other_browser": errLinkOtherBrowser,
	"account_suspended":  "this account has been suspended by the operator",
	"network":            "Could not reach {product}. Check your connection.",
	"unknown":            "Something went wrong. Try again.",
}

const (
	signInReasonWrong   = "code_wrong"
	signInReasonUsedUp  = "code_used_up"
	signInReasonExpired = "code_expired"
	signInReasonLink    = "link_other_browser"
)

func signInCodeTTLMinutes() int {
	min := int(authTokenTTL() / time.Minute)
	if min < 1 {
		min = 1
	}
	return min
}

func signInCodeWrong(triesLeft int) errorResponse {
	msg := signInMessages["code_wrong"]
	if triesLeft == 1 {
		msg = "That code is not right. 1 try left."
	} else {
		msg = strings.ReplaceAll(msg, "{n}", strconv.Itoa(triesLeft))
	}
	n := triesLeft
	return errorResponse{Error: msg, Reason: signInReasonWrong, TriesLeft: &n}
}

func signInCodeUsedUp() errorResponse {
	return errorResponse{Error: signInMessages["code_used_up"], Reason: signInReasonUsedUp}
}

func signInCodeExpired() errorResponse {
	msg := strings.ReplaceAll(signInMessages["code_expired"], "{min}", strconv.Itoa(signInCodeTTLMinutes()))
	return errorResponse{Error: msg, Reason: signInReasonExpired}
}

func signInLinkOtherBrowser() errorResponse {
	return errorResponse{Error: errLinkOtherBrowser, Reason: signInReasonLink}
}

func signInRateLimited() errorResponse {
	return errorResponse{Error: signInMessages["rate_limited"], Code: "rate_limited"}
}

func signInInvalidEmail() errorResponse {
	return errorResponse{Error: signInMessages["invalid_email"], Code: "invalid_email"}
}

func signInSendFailed() errorResponse {
	return errorResponse{Error: signInMessages["send_failed"], Code: "send_failed"}
}

func signInEmailUnavailable() errorResponse {
	return errorResponse{Error: signInMessages["email_unavailable"], Code: "email_unavailable"}
}

func signInCodeSentMessage(email string) string {
	return "We sent a 6-digit code to " + email + "."
}

func signInUnauthorized(body errorResponse) errorResponse {
	body.Code = "invalid_code"
	return body
}
