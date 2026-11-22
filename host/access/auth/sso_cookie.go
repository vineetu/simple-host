package auth

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
)

// SSOCookieHost and SSOCookieHTTP name the browser's SSO pass cookie.
const SSOCookieHost = "__Host-sh_sso"

const SSOCookieHTTP = "sh_sso"

// SSOCookieName is the SSO pass cookie for r: __Host- over HTTPS.
func SSOCookieName(r *http.Request) string {
	if httpx.IsHTTPS(r) {
		return SSOCookieHost
	}
	return SSOCookieHTTP
}

// SignOutBrowserPass reads the browser family only on dashboard sign-out.
func SignOutBrowserPass(r *http.Request) string {
	if r.URL.Path != "/v1/me/sign-out" {
		return ""
	}
	c, err := r.Cookie(SSOCookieName(r))
	if err != nil {
		return ""
	}
	return c.Value
}

// SetSSOCookie sets (or, with maxAge < 0, clears) the SSO pass cookie.
func SetSSOCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: SSOCookieName(r), Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: httpx.IsHTTPS(r), SameSite: http.SameSiteLaxMode})
}
