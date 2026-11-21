package web

import (
	"github.com/vsriram/simple-host/host/httpx"
)

// SignInMessages is the one table of sign-in sentences. The served signin.js
// gets this map as JSON (see transformStatic), so the server and the card
// speak the same words. Keys with {n} or {min} are filled by the server; the
// client falls back to the text without the number.
var SignInMessages = map[string]string{
	"bad_credentials":          "Wrong username or password.",
	"password_change_required": "Choose a new password to finish signing in.",
	"username_taken":           "That username is taken.",
	"code_wrong":               "That code is not right. {n} tries left.",
	"code_used_up":             "Too many wrong tries, so this code no longer works. Send a new one to try again.",
	"code_expired":             "That code has expired or was already used. Codes last {min} minutes. Send a new code.",
	"rate_limited":             "Too many tries. Wait a minute, then try again.",
	"invalid_email":            "That email address does not look right.",
	"send_failed":              "We could not send the email just now. Try again in a minute.",
	"signup_blocked":           SignupBlockedMessage,
	"email_signin_off":         ErrEmailSignInOff.Error,
	"email_unavailable":        "This server cannot send sign-in codes. Use Google if it is offered, or ask whoever runs this server.",
	"link_other_browser":       ErrLinkOtherBrowser,
	"account_suspended":        "this account has been suspended by the operator",
	"network":                  "Could not reach {product}. Check your connection.",
	"unknown":                  "Something went wrong. Try again.",
}

// SignupBlockedMessage names no country and no reason beyond "your region" —
// shown verbatim to the person, never with sanctions or policy language. It
// covers both a refused new signup and a refused existing sign-in.
const SignupBlockedMessage = "Sign-in isn't available from your region."

// EmailSignInEnabled is EMAIL_SIGNIN, set once at startup. Off refuses every
// email-code request and email+code verification; the magic-link token shape
// stays, since the OAuth owner flow redeems its one-time token that way.
var EmailSignInEnabled = true

// ErrEmailSignInOff is the contract's fixed refusal body.
var ErrEmailSignInOff = httpx.ErrorResponse{Error: "Email sign-in is off on this server. Sign in with your company account.", Code: "email_signin_off"}

// ErrLinkOtherBrowser is shown when a sign-in link is opened where it was not
// requested.
const ErrLinkOtherBrowser = "Open this link on the device where you asked for it, or type the 6-digit code from the email."
