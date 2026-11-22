// Package signin is a person's own sign-in on the app's address: an emailed
// code or link (POST /v1/auth, /v1/auth/verify) and, where PASSWORD_SIGNIN is
// on, a password with its operator and self sign-up routes (password.go).
// Each success ends in finishSignIn: a new key, the sign-in alert, the
// answer. Issuing and checking a code is the core's (access.IssueEmailCode),
// worded for HTTP by web.IssueEmailCode, which the site gate's visitor and
// viewer sign-in share.
package signin
