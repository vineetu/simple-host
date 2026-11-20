package handler

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/workers"
)

// Transitional (cleanup 3.2): callers move to httpx in 4.x.
//
// This file re-exposes host/httpx's protocol helpers under the names
// host/handler used before the move, so the hundreds of existing call
// sites (including host/handler/hack*.go, owned by a parallel worker in
// this step) keep compiling unchanged.

// errorResponse is httpx.ErrorResponse. A type alias (not a new type) so
// existing composite literals (errorResponse{...}) keep compiling and
// satisfy httpx.WriteJSON's type switch.
type errorResponse = httpx.ErrorResponse

var (
	writeJSON       = httpx.WriteJSON
	writeProductErr = httpx.WriteError
	writeInternal   = httpx.WriteInternal
	acceptsMarkdown = httpx.AcceptsMarkdown
	csvSafe         = httpx.CSVSafe
)

// cspNonceKey is httpx.NonceKey. A type alias, not a redefinition, so the
// context value adminUICSP (ui.go) sets and the one httpx.StampNonce reads
// are exactly the same key.
type cspNonceKey = httpx.NonceKey

var stampNonce = httpx.StampNonce

var (
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
