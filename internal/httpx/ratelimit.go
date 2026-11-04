package httpx

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiter is a simple in-memory token-bucket limiter keyed by an arbitrary
// string (client IP, email, user id, ...). Safe for concurrent use.
//
// This is a single-instance limiter — adequate because simple-host runs as one
// binary behind the proxy. If this ever scales horizontally, swap for a shared
// store (Redis, Postgres). It is defense against abuse/DoS, not a hard quota.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	// Rate, Capacity and Now are exported so a test (even through internal/
	// handler's transitional *rateLimiter wrapper, which embeds this type)
	// can read or override them directly, as the pre-move tests did.
	Rate     float64 // tokens refilled per second
	Capacity float64 // max burst
	Now      func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter builds a limiter that allows bursts up to capacity and refills
// at perSecond tokens/sec. It does NOT start the cleanup goroutine — callers
// invoke StartCleanup once they're wired into a long-lived server (keeps tests
// goroutine-free).
func NewRateLimiter(capacity, perSecond float64) *RateLimiter {
	return &RateLimiter{
		buckets:  make(map[string]*tokenBucket),
		Rate:     perSecond,
		Capacity: capacity,
		Now:      time.Now,
	}
}

// Allow consumes one token for key, returning false when the bucket is empty.
func (rl *RateLimiter) Allow(key string) bool {
	allowed, _, _ := rl.AllowState(key)
	return allowed
}

// AllowState takes the token and snapshots the remaining balance under one lock.
// next is seconds until one whole token is available, zero if already available.
func (rl *RateLimiter) AllowState(key string) (allowed bool, tokens, next float64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.Capacity, last: now}
		rl.buckets[key] = b
	}
	b.tokens = math.Min(rl.Capacity, b.tokens+now.Sub(b.last).Seconds()*rl.Rate)
	b.last = now
	allowed = b.tokens >= 1
	if allowed {
		b.tokens--
	}
	if b.tokens < 1 && rl.Rate > 0 {
		next = (1 - b.tokens) / rl.Rate
	}
	return allowed, b.tokens, next
}

// AllowHTTP reports the post-consumption state even on successful requests.
// Policy w is the empty-to-full refill duration, not a fixed reset window.
func (rl *RateLimiter) AllowHTTP(w http.ResponseWriter, key string) bool {
	allowed, tokens, next := rl.AllowState(key)
	remaining := int(math.Floor(tokens))
	reset := int(math.Ceil(next))
	capacity := strconv.FormatFloat(rl.Capacity, 'f', -1, 64)
	policy := `"token-bucket";q=` + capacity
	if rl.Rate > 0 {
		policy += ";w=" + strconv.FormatFloat(math.Ceil(rl.Capacity/rl.Rate), 'f', 0, 64)
	}
	// A quoted extension preserves the exact refill rate, including fractions.
	policy += `;refill="` + strconv.FormatFloat(rl.Rate, 'f', -1, 64) + `"`
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

// StartCleanup periodically evicts idle, fully-refilled buckets so the map
// can't grow without bound under a churn of distinct keys.
func (rl *RateLimiter) StartCleanup(ctx context.Context, every, idle time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.EvictIdle(idle)
		}
	}
}

// EvictIdle drops buckets unused for idle that have refilled: a new bucket
// starts full, so forgetting them changes nothing.
func (rl *RateLimiter) EvictIdle(idle time.Duration) int {
	removed := 0
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.Now()
	cutoff := now.Add(-idle)
	for k, b := range rl.buckets {
		if b.last.Before(cutoff) && b.tokens+now.Sub(b.last).Seconds()*rl.Rate >= rl.Capacity-0.0001 {
			delete(rl.buckets, k)
			removed++
		}
	}
	return removed
}

// BucketCount reports how many distinct keys this limiter currently tracks
// (test and diagnostic use).
func (rl *RateLimiter) BucketCount() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}

// Reset atomically installs a new capacity and rate and drops every existing
// bucket, as if the limiter were newly built (test and reconfiguration use).
func (rl *RateLimiter) Reset(capacity, rate float64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.Capacity = capacity
	rl.Rate = rate
	rl.buckets = make(map[string]*tokenBucket)
}

// trustedProxies are the networks the proxies in front of this server
// connect from (TRUSTED_PROXY_CIDRS). Empty keeps the one-local-nginx rule.
var (
	trustedProxiesMu sync.RWMutex
	trustedProxies   []*net.IPNet
)

// SetTrustedProxies installs TRUSTED_PROXY_CIDRS. nil or empty: X-Forwarded-For
// is believed from any peer (its last entry), as behind one local nginx.
func SetTrustedProxies(nets []*net.IPNet) {
	trustedProxiesMu.Lock()
	defer trustedProxiesMu.Unlock()
	trustedProxies = nets
}

func trustedProxy(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the originating client address.
//
// Without TRUSTED_PROXY_CIDRS we sit behind one trusted local nginx that
// appends the real client to X-Forwarded-For, so its last entry is the client;
// earlier entries can be forged. With TRUSTED_PROXY_CIDRS (a load balancer or
// ingress controller on Kubernetes) the header is believed only when the peer
// is a trusted proxy, and is read from the right past every trusted hop: the
// first address no trusted proxy owns is the client. A direct connection from
// outside those networks is its own client, whatever header it sends. Fall
// back to the transport peer otherwise.
func ClientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	xff := r.Header.Get("X-Forwarded-For")
	trustedProxiesMu.RLock()
	gated := len(trustedProxies) > 0
	trusted := trustedProxy(peer)
	trustedProxiesMu.RUnlock()
	if !gated {
		if xff != "" {
			if last := strings.TrimSpace(xff[strings.LastIndex(xff, ",")+1:]); last != "" {
				return last
			}
		}
		return peer
	}
	if !trusted || xff == "" {
		return peer
	}
	hops := strings.Split(xff, ",")
	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		if !trustedProxy(hop) {
			return hop
		}
	}
	// Every hop is a proxy of ours: the leftmost is the nearest to a client.
	if first := strings.TrimSpace(hops[0]); first != "" {
		return first
	}
	return peer
}

// RateLimitByIP wraps next, rejecting requests from a client IP that has
// exhausted its bucket with 429.
func RateLimitByIP(rl *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.AllowHTTP(w, ClientIP(r)) {
			TooManyRequests(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TooManyRequests answers 429 with a default Retry-After (when the caller has
// not already set one) and the shared rate-limited error body.
func TooManyRequests(w http.ResponseWriter) {
	if w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "60")
	}
	WriteJSON(w, http.StatusTooManyRequests, ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
}
