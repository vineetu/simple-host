package handler

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
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
	allowed, _, _ := rl.allowState(key)
	return allowed
}

// allowState takes the token and snapshots the remaining balance under one lock.
// next is seconds until one whole token is available, zero if already available.
func (rl *rateLimiter) allowState(key string) (allowed bool, tokens, next float64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.capacity, last: now}
		rl.buckets[key] = b
	}
	b.tokens = math.Min(rl.capacity, b.tokens+now.Sub(b.last).Seconds()*rl.rate)
	b.last = now
	allowed = b.tokens >= 1
	if allowed {
		b.tokens--
	}
	if b.tokens < 1 && rl.rate > 0 {
		next = (1 - b.tokens) / rl.rate
	}
	return allowed, b.tokens, next
}

// allowHTTP reports the post-consumption state even on successful requests.
// Policy w is the empty-to-full refill duration, not a fixed reset window.
func (rl *rateLimiter) allowHTTP(w http.ResponseWriter, key string) bool {
	allowed, tokens, next := rl.allowState(key)
	remaining := int(math.Floor(tokens))
	reset := int(math.Ceil(next))
	capacity := strconv.FormatFloat(rl.capacity, 'f', -1, 64)
	policy := `"token-bucket";q=` + capacity
	if rl.rate > 0 {
		policy += ";w=" + strconv.FormatFloat(math.Ceil(rl.capacity/rl.rate), 'f', 0, 64)
	}
	// A quoted extension preserves the exact refill rate, including fractions.
	policy += `;refill="` + strconv.FormatFloat(rl.rate, 'f', -1, 64) + `"`
	w.Header().Set("RateLimit-Policy", policy)
	w.Header().Set("RateLimit", fmt.Sprintf(`"token-bucket";r=%d;t=%d`, remaining, reset))
	w.Header().Set("RateLimit-Limit", capacity)
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("RateLimit-Reset", strconv.Itoa(reset))
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(reset))
	}
	return allowed
}

// startCleanup periodically evicts idle, fully-refilled buckets so the map
// can't grow without bound under a churn of distinct keys.
func (rl *rateLimiter) startCleanup(every, idle time.Duration) {
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for range ticker.C {
			rl.evictIdle(idle)
		}
	}()
}

// evictIdle drops buckets unused for idle that have refilled: a new bucket
// starts full, so forgetting them changes nothing.
func (rl *rateLimiter) evictIdle(idle time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	cutoff := now.Add(-idle)
	for k, b := range rl.buckets {
		if b.last.Before(cutoff) && b.tokens+now.Sub(b.last).Seconds()*rl.rate >= rl.capacity-0.0001 {
			delete(rl.buckets, k)
		}
	}
}

// clientIP returns the originating client address. We sit behind the trusted
// nginx appends the real client to X-Forwarded-For; earlier entries can be forged.
// Fall back to the transport peer otherwise.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if last := strings.TrimSpace(xff[strings.LastIndex(xff, ",")+1:]); last != "" {
			return last
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
		if !rl.allowHTTP(w, clientIP(r)) {
			tooManyRequests(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tooManyRequests(w http.ResponseWriter) {
	if w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "60")
	}
	writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
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
