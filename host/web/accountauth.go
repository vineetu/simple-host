package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
)

func WriteAccountAuthUnavailable(w http.ResponseWriter, baseHost string) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{
		Code:  "account_auth_unavailable",
		Error: "Account sign-in is only served on the app's own address.",
		Hint:  "For a page's visitors use visitor sign-in on the site's own address: load https://" + baseHost + "/auth.js, call SH.mount('#sh-auth'), and await SH.requireSignIn() before saving. Visitor sign-in never returns an API key.",
	})
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

func IsLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
