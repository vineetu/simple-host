package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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
		{"xff chain takes last", "203.0.113.7, 70.0.0.1", "10.0.0.1:5000", "70.0.0.1"},
		{"xff with spaces", "  203.0.113.7  ,70.0.0.1", "10.0.0.1:5000", "70.0.0.1"},
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

func TestRateLimitHeaders(t *testing.T) {
	now := time.Unix(0, 0)
	rl := newRateLimiter(2, 0.4) // one token every 2.5 seconds
	rl.now = func() time.Time { return now }
	calls := 0
	h := rateLimitByIP(rl, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	for i, want := range []struct{ status, remaining, reset int }{{204, 1, 0}, {204, 0, 3}, {429, 0, 3}, {429, 0, 2}, {204, 0, 3}} {
		if i == 3 {
			now = now.Add(time.Second)
		}
		if i == 4 {
			now = now.Add(1500 * time.Millisecond)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
		if w.Code != want.status {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
		for name, value := range map[string]int{"RateLimit-Limit": 2, "RateLimit-Remaining": want.remaining, "RateLimit-Reset": want.reset} {
			got, err := strconv.Atoi(w.Header().Get(name))
			if err != nil || got != value {
				t.Errorf("request %d: %s=%q, want %d", i, name, w.Header().Get(name), value)
			}
		}
		if w.Header().Get("RateLimit-Policy") != `"token-bucket";q=2;w=5;refill="0.4"` || w.Header().Get("RateLimit") == "" {
			t.Errorf("policy headers: %v", w.Header())
		}
		if want.status == 429 && w.Header().Get("Retry-After") != strconv.Itoa(want.reset) {
			t.Errorf("Retry-After=%q", w.Header().Get("Retry-After"))
		}
		if want.status != 429 && w.Header().Get("Retry-After") != "" {
			t.Error("allowed request has Retry-After")
		}
	}
	if calls != 3 {
		t.Fatalf("handler ran %d times", calls)
	}
}
