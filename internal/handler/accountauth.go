package handler

import (
	"net/http"
	"net/url"
	"strings"
)

// accountAuthOnly keeps account sign-in (the emailed code and Google flows
// that end in an account API key) on the app's own address. Every other host
// this server answers on (a site's address, a person's address, an address
// family, a connected domain, the shared content host) serves hosted pages,
// and a hosted page must never be able to mint an account key: a visitor there
// signs in with the visitor routes instead, which issue a cookie scoped to
// that one site.
//
// Two checks: the request's Host must be the public base host, and when a
// browser sends an Origin it must be exactly the public base origin (scheme,
// host and port, with default ports dropped), so a page on a site cannot call
// the apex cross-origin either; the open CORS policy would otherwise let it.
// A loopback host (a bare local run, a test fixture) is allowed with no Origin
// or its own, since no hosted page shares that address. With no usable public
// base URL nothing is refused.
func accountAuthOnly(publicBaseURL string, next http.Handler) http.Handler {
	base := publicBaseOrigin(publicBaseURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base != nil && !accountAuthAllowed(r, base) {
			writeAccountAuthUnavailable(w, base.Hostname())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAccountAuthUnavailable(w http.ResponseWriter, baseHost string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, errorResponse{
		Code:  "account_auth_unavailable",
		Error: "Account sign-in is for Simple Host account owners and their agents, never for a site's visitors; it is only served on the app's own address.",
		Hint:  "For a page's visitors use visitor sign-in on the site's own address: load https://" + baseHost + "/auth.js, call SH.mount('#sh-auth') (emailed code or Google), and await SH.requireSignIn() before saving. Visitor sign-in never returns an API key.",
	})
}

// publicBaseOrigin parses PUBLIC_BASE_URL into an origin. A schemeless value
// is read as https; an unparseable or empty one gives nil (nothing gated).
func publicBaseOrigin(publicBaseURL string) *url.URL {
	s := strings.TrimSpace(publicBaseURL)
	if s == "" {
		return nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	return u
}

func accountAuthAllowed(r *http.Request, base *url.URL) bool {
	host := requestHostName(r)
	loopback := isLoopbackHost(host)
	if host != strings.ToLower(base.Hostname()) && !loopback {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin: an agent or a same-origin navigation, not a cross-origin
		// page script.
		return true
	}
	if origin == "null" {
		// An opaque origin (a sandboxed frame): foreign.
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Hostname() == "" {
		return false
	}
	if loopback {
		// A bare local run: the page must come from this very server (same
		// scheme, host and port), not from another local app.
		scheme := "http"
		if requestIsHTTPS(r) {
			scheme = "https"
		}
		own, err := url.Parse(scheme + "://" + r.Host)
		return err == nil && normalizedOrigin(o) == normalizedOrigin(own)
	}
	return normalizedOrigin(o) == normalizedOrigin(base)
}

// normalizedOrigin is scheme://host[:port] in lower case with the scheme's
// default port dropped, so https://simple-host.app:443 equals https://simple-host.app.
func normalizedOrigin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// siteStartOrAccountAuth serves the account Google start on the app's own
// address and, on any other host, hands a hand-written link over to the
// site-host visitor start on that same host (GET /v1/visitor/oauth/{provider}),
// which binds the sign-in to the browser and never mints a key.
func siteStartOrAccountAuth(publicBaseURL string, next http.Handler) http.Handler {
	base := publicBaseOrigin(publicBaseURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base != nil && !accountAuthAllowed(r, base) {
			if requestHostName(r) != strings.ToLower(base.Hostname()) {
				provider := url.PathEscape(strings.ToLower(strings.TrimSpace(r.PathValue("provider"))))
				returnTo := r.URL.Query().Get("return_to")
				if returnTo == "" {
					scheme := "http"
					if requestIsHTTPS(r) {
						scheme = "https"
					}
					returnTo = scheme + "://" + r.Host + "/"
				}
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "/v1/visitor/oauth/"+provider+"?return_to="+url.QueryEscape(returnTo), http.StatusFound)
				return
			}
			writeAccountAuthUnavailable(w, base.Hostname())
			return
		}
		next.ServeHTTP(w, r)
	})
}
