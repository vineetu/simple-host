package handler

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/workers"
)

// The host/httpx protocol helpers under this package's short names. The
// type aliases keep errorResponse{...} literals and the CSP nonce context key
// identical to httpx's own.
type (
	errorResponse = httpx.ErrorResponse
	cspNonceKey   = httpx.NonceKey
)

var (
	writeJSON         = httpx.WriteJSON
	writeProductErr   = httpx.WriteError
	writeInternal     = httpx.WriteInternal
	acceptsMarkdown   = httpx.AcceptsMarkdown
	csvSafe           = httpx.CSVSafe
	stampNonce        = httpx.StampNonce
	clientIP          = httpx.ClientIP
	tooManyRequests   = httpx.TooManyRequests
	SetTrustedProxies = httpx.SetTrustedProxies
)

// rateLimitByIP wraps next, rejecting requests from a client IP that has
// exhausted its bucket with 429. rl is read only when a request arrives, so a
// test handler may leave it nil.
func rateLimitByIP(rl *httpx.RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.AllowHTTP(w, clientIP(r)) {
			tooManyRequests(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *SiteHandler) HTTPCaches() workers.Owner      { return &h.httpCaches }
func (h *UserHandler) HTTPCaches() workers.Owner      { return &h.httpCaches }
func (h *OAuthHandler) HTTPCaches() workers.Owner     { return &h.httpCaches }
func (h *ConnectorHandler) HTTPCaches() workers.Owner { return &h.httpCaches }
