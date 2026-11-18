package static

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

type pageHandoffKey struct{}

// pageHandoff selects the bounded header wait for requests forwarded by the page dispatcher.
func pageHandoff(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pageHandoffKey{}, true)))
	})
}

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
	pageTransport := http.DefaultTransport.(*http.Transport).Clone()
	pageTransport.ResponseHeaderTimeout = 5 * time.Second
	pageProxy := *proxy
	pageProxy.Transport = pageTransport
	pageProxy.Rewrite = func(p *httputil.ProxyRequest) {
		// The API uses the original request URI for protected-page return URLs.
		p.Out.URL.RawQuery = p.In.URL.RawQuery
		proxy.Rewrite(p)
	}
	pageProxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode >= 500 && httpx.PageAPIFallback(resp.Request) != nil {
			// Only an unmarked page waiting on the owner's frame guard can fall back.
			return errors.New("API could not serve the page")
		}
		return nil
	}
	pageProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if fallback := httpx.PageAPIFallback(r); fallback != nil {
			fallback()
			return
		}
		proxy.ErrorHandler(w, r, err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The API may answer while an import is still arriving. HTTP/1 must
		// not drain the rest of the request before forwarding that response.
		_ = http.NewResponseController(w).EnableFullDuplex()
		if r.Context().Value(pageHandoffKey{}) == true {
			pageProxy.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func Unavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "10")
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Service temporarily unavailable. Please try again.", http.StatusServiceUnavailable)
}
