package web

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

func TestRateLimiterBurstThenRefill(t *testing.T) {
	now := time.Unix(0, 0)
	rl := httpx.NewRateLimiter(3, 1) // burst 3, 1 token/sec
	rl.Now = func() time.Time { return now }

	// First 3 calls allowed (burst), 4th denied.
	for i := 0; i < 3; i++ {
		if !rl.Allow("k") {
			t.Fatalf("call %d should be allowed", i)
		}
	}
	if rl.Allow("k") {
		t.Fatal("4th call should be denied")
	}

	// After 1s, exactly one token refills.
	now = now.Add(1 * time.Second)
	if !rl.Allow("k") {
		t.Fatal("call after 1s refill should be allowed")
	}
	if rl.Allow("k") {
		t.Fatal("only one token should have refilled")
	}
}

func TestRateLimiterKeysAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	rl := httpx.NewRateLimiter(1, 0)
	rl.Now = func() time.Time { return now }

	if !rl.Allow("a") || !rl.Allow("b") {
		t.Fatal("distinct keys must not share a bucket")
	}
	if rl.Allow("a") {
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
		{"no xff falls back to remote", "", "10.0.0.1:5000", "10.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := httpx.ClientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// With TRUSTED_PROXY_CIDRS set (Kubernetes: a load balancer or an ingress
// controller in front), X-Forwarded-For is believed only from a trusted peer
// and read right to left past every trusted hop.
func TestClientIPTrustedProxies(t *testing.T) {
	var nets []*net.IPNet
	for _, c := range []string{"10.0.0.0/8", "127.0.0.0/8", "fd00::/8"} {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		nets = append(nets, n)
	}
	httpx.SetTrustedProxies(nets)
	t.Cleanup(func() { httpx.SetTrustedProxies(nil) })
	cases := []struct {
		name       string
		xff        string
		remoteAddr string
		want       string
	}{
		{"one trusted proxy appends the client", "203.0.113.7", "10.244.1.9:5000", "203.0.113.7"},
		{"forged entry before the real client", "198.51.100.1, 203.0.113.7", "10.244.1.9:5000", "203.0.113.7"},
		{"a forged trusted-looking entry is still skipped", "198.51.100.1, 10.9.9.9", "10.244.1.9:5000", "198.51.100.1"},
		{"direct connection ignores the header", "203.0.113.7", "198.51.100.1:5000", "198.51.100.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := httpx.ClientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
