package web

import (
	"github.com/vsriram/simple-host/host/httpx"
)

// emailSignInEnabled is EMAIL_SIGNIN (contract default "on"). SetEmailSignIn
// wires it once at startup from config.Config.EmailSignInEnabled(). off
// refuses every email-code sign-in request (issueEmailCode) and every
// email+code verification (verifyEmailCode's req.Email+req.Code shape); the
// magic-link token shape (req.Token) is untouched, since it is also how the
// OAuth owner flow's one-time token is redeemed and must keep working
// whether or not email sign-in itself is on.
var EmailSignInEnabled = true

// ErrEmailSignInOff is the contract's fixed refusal body.
var ErrEmailSignInOff = httpx.ErrorResponse{Error: "Email sign-in is off on this server. Sign in with your company account.", Code: "email_signin_off"}

// ErrLinkOtherBrowser is shown when a sign-in link is opened where it was not
// requested.
const ErrLinkOtherBrowser = "Open this link on the device where you asked for it, or type the 6-digit code from the email."
