package handler

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/httpx"
)

// Transitional (cleanup 3.2): callers move to httpx in 4.x.
//
// This file re-exposes internal/httpx's protocol helpers under the names
// internal/handler used before the move, so the hundreds of existing call
// sites (including internal/handler/hack*.go, owned by a parallel worker in
// this step) keep compiling unchanged.

// errorResponse is httpx.ErrorResponse. A type alias (not a new type) so
// existing composite literals (errorResponse{...}) keep compiling and
// satisfy httpx.WriteJSON's type switch.
type errorResponse = httpx.ErrorResponse

var (
	writeJSON       = httpx.WriteJSON
	writeJSONETag   = httpx.WriteJSONETag
	writeHackErr    = httpx.WriteError
	writeInternal   = httpx.WriteInternal
	acceptsMarkdown = httpx.AcceptsMarkdown
	csvSafe         = httpx.CSVSafe
)

// cspNonceKey is httpx.NonceKey. A type alias, not a redefinition, so the
// context value adminUICSP (ui.go) sets and the one httpx.StampNonce reads
// are exactly the same key.
type cspNonceKey = httpx.NonceKey

var stampNonce = httpx.StampNonce

// rateLimiter wraps httpx.RateLimiter with the lowercase method names every
// existing call site (dozens of files, including hack*.go) already uses.
// It cannot be a plain `type rateLimiter = httpx.RateLimiter` alias: Go does
// not let an alias add methods, and httpx's methods are exported (Allow,
// AllowHTTP, ...) for hackhttp's own future direct use. Embedding promotes
// those exported methods too, so both spellings work during the transition.
type rateLimiter struct {
	*httpx.RateLimiter
}

// newRateLimiter builds a limiter with the handler-package lowercase API.
func newRateLimiter(capacity, perSecond float64) *rateLimiter {
	return &rateLimiter{httpx.NewRateLimiter(capacity, perSecond)}
}

func (rl *rateLimiter) allow(key string) bool { return rl.RateLimiter.Allow(key) }

func (rl *rateLimiter) allowHTTP(w http.ResponseWriter, key string) bool {
	return rl.RateLimiter.AllowHTTP(w, key)
}

func (rl *rateLimiter) startCleanup(every, idle time.Duration) {
	rl.RateLimiter.StartCleanup(every, idle)
}

func (rl *rateLimiter) evictIdle(idle time.Duration) {
	rl.RateLimiter.EvictIdle(idle)
}

var (
	clientIP          = httpx.ClientIP
	tooManyRequests   = httpx.TooManyRequests
	SetTrustedProxies = httpx.SetTrustedProxies
)

// rateLimitByIP wraps next, rejecting requests from a client IP that has
// exhausted its bucket with 429. Deliberately NOT httpx.RateLimitByIP(rl.
// RateLimiter, next): some handlers are built in tests with a nil *rateLimiter
// field and never route a request through it, and rl must stay undereferenced
// until a request actually arrives (matching the pre-move behaviour), not
// when the route is registered.
func rateLimitByIP(rl *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allowHTTP(w, clientIP(r)) {
			tooManyRequests(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
