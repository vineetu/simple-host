package gate

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// siteStartOrAccountAuth serves the account Google start on the app's own
// address and, on any other host, hands a hand-written link over to the
// site-host visitor start on that same host (GET /v1/visitor/oauth/{provider}),
// which binds the sign-in to the browser and never mints a key.
func siteStartOrAccountAuth(publicBaseURL string, next http.Handler) http.Handler {
	base := web.PublicBaseOrigin(publicBaseURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base != nil && !web.AccountAuthAllowed(r, base) {
			if httpx.HostName(r) != strings.ToLower(base.Hostname()) {
				provider := url.PathEscape(strings.ToLower(strings.TrimSpace(r.PathValue("provider"))))
				returnTo := r.URL.Query().Get("return_to")
				if returnTo == "" {
					scheme := "http"
					if httpx.IsHTTPS(r) {
						scheme = "https"
					}
					returnTo = scheme + "://" + r.Host + "/"
				}
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "/v1/visitor/oauth/"+provider+"?return_to="+url.QueryEscape(returnTo), http.StatusFound)
				return
			}
			web.WriteAccountAuthUnavailable(w, base.Hostname())
			return
		}
		next.ServeHTTP(w, r)
	})
}
