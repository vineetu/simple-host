package handler

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(3, 1) // burst 3, 1 token/sec
	rl.now = func() time.Time { return now }

	// First 3 calls allowed (burst), 4th denied.
	for i := 0; i < 3; i++ {
		if !rl.allow("k") {
			t.Fatalf("call %d should be allowed", i)
		}
	}
	if rl.allow("k") {
		t.Fatal("4th call should be denied")
	}

	// After 1s, exactly one token refills.
	now = now.Add(1 * time.Second)
	if !rl.allow("k") {
		t.Fatal("call after 1s refill should be allowed")
	}
	if rl.allow("k") {
		t.Fatal("only one token should have refilled")
	}
}

func TestRateLimiterKeysAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(1, 0)
	rl.now = func() time.Time { return now }

	if !rl.allow("a") || !rl.allow("b") {
		t.Fatal("distinct keys must not share a bucket")
	}
	if rl.allow("a") {
		t.Fatal("key a should be exhausted")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
		{"xff single", "203.0.113.7", "10.0.0.1:5000", "203.0.113.7"},
		{"xff chain takes leftmost", "203.0.113.7, 70.0.0.1", "10.0.0.1:5000", "203.0.113.7"},
		{"xff with spaces", "  203.0.113.7  ,70.0.0.1", "10.0.0.1:5000", "203.0.113.7"},
		{"no xff falls back to remote", "", "10.0.0.1:5000", "10.0.0.1"},
		{"no port remote", "", "10.0.0.1", "10.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
