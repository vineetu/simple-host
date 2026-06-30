package handler

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a simple in-memory token-bucket limiter keyed by an arbitrary
// string (client IP, email, user id, ...). Safe for concurrent use.
//
// This is a single-instance limiter — adequate because simple-host runs as one
// binary behind the proxy. If this ever scales horizontally, swap for a shared
// store (Redis, Postgres). It is defense against abuse/DoS, not a hard quota.
type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	rate     float64 // tokens refilled per second
	capacity float64 // max burst
	now      func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter builds a limiter that allows bursts up to capacity and refills
// at perSecond tokens/sec. It does NOT start the cleanup goroutine — callers
// invoke startCleanup once they're wired into a long-lived server (keeps tests
// goroutine-free).
func newRateLimiter(capacity, perSecond float64) *rateLimiter {
	return &rateLimiter{
		buckets:  make(map[string]*tokenBucket),
		rate:     perSecond,
		capacity: capacity,
		now:      time.Now,
	}
}

// allow consumes one token for key, returning false when the bucket is empty.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		// New keys start full so a first request is never throttled.
		rl.buckets[key] = &tokenBucket{tokens: rl.capacity - 1, last: now}
		return true
	}

	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// startCleanup periodically evicts idle, fully-refilled buckets so the map
// can't grow without bound under a churn of distinct keys.
func (rl *rateLimiter) startCleanup(every, idle time.Duration) {
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for range ticker.C {
			rl.mu.Lock()
			cutoff := rl.now().Add(-idle)
			for k, b := range rl.buckets {
				if b.tokens >= rl.capacity-0.0001 && b.last.Before(cutoff) {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
}

// clientIP returns the originating client address. We sit behind the trusted
// nginx reverse proxy, which sets X-Forwarded-For, so the left-most entry is
// the real client; fall back to the transport peer otherwise.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimitByIP wraps next, rejecting requests from a client IP that has
// exhausted its bucket with 429.
func rateLimitByIP(rl *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			tooManyRequests(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down"})
}

// SecurityHeaders adds X-Content-Type-Options: nosniff to every response.
//
// It deliberately does NOT set X-Frame-Options, Strict-Transport-Security, or
// Referrer-Policy: in every real deployment a TLS-terminating proxy fronts this
// binary (the app port is not publicly reachable) and that proxy already sets
// those edge headers. Re-setting them here produced *conflicting duplicates*
// (e.g. two X-Frame-Options values, which browsers then ignore, and a weaker
// HSTS max-age shadowing the proxy's). nosniff is kept because identical
// duplicates are harmless and it's valuable on API JSON if ever served direct.
// The admin UI's Content-Security-Policy — which the proxy does NOT set — is
// applied separately in adminUICSP (ui.go); its frame-ancestors 'none'
// supersedes X-Frame-Options on modern browsers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
