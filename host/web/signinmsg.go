package web

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
