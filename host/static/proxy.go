package static

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
)

// NewProxy streams requests to the API without changing the visitor's origin.
// The API must trust Static's transport address in TRUSTED_PROXY_CIDRS.
func NewProxy(target *url.URL, trusted []*net.IPNet, errorPage ...http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = p.In.Host
			// Rewrite has already removed all forwarding headers. Collapse the
			// incoming chain to exactly the client the existing code trusts.
			p.Out.Header.Set("X-Forwarded-For", httpx.ClientIP(p.In))
			scheme := "http"
			if p.In.TLS != nil {
				scheme = "https"
			}
			peer, _, err := net.SplitHostPort(p.In.RemoteAddr)
			if err != nil {
				peer = p.In.RemoteAddr
			}
			ip := net.ParseIP(peer)
			for _, network := range trusted {
				if network.Contains(ip) {
					if proto := strings.ToLower(p.In.Header.Get("X-Forwarded-Proto")); proto == "http" || proto == "https" {
						scheme = proto
					}
					break
				}
			}
			p.Out.Header.Set("X-Forwarded-Proto", scheme)
			p.Out.Header.Del("X-Forwarded-Host")
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			if len(errorPage) > 0 {
				errorPage[0].ServeHTTP(w, r)
				return
			}
			Unavailable(w, r)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The API may answer while an import is still arriving. HTTP/1 must
		// not drain the rest of the request before forwarding that response.
		_ = http.NewResponseController(w).EnableFullDuplex()
		proxy.ServeHTTP(w, r)
	})
}

func Unavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "10")
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Service temporarily unavailable. Please try again.", http.StatusServiceUnavailable)
}
