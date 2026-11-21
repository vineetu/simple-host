package accesshttp

import (
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

// withLimits loads the env settings in m into a config.Limits value. The
// caller builds a handler (or edition.ForKindWithLimits) from it directly:
// each handler holds its own snapshot now, so there is no global left to
// apply it to.
func withLimits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// The limiters are built from the RATE_LIMIT_* settings.
func TestRateLimitsFollowSettings(t *testing.T) {
	l := withLimits(t, map[string]string{"RATE_LIMIT_SIGNIN_IP": "2,1h", "RATE_LIMIT_SIGNIN_EMAIL": "9,20s"})
	h := NewUserHandler(nil, accountapp.New(nil), nil, "https://simple-host.app", edition.Default(l))
	if h.IPLimiter.Capacity != 2 || h.IPLimiter.Rate != 1.0/3600 {
		t.Errorf("sign-in IP limiter: burst %v, %v/s", h.IPLimiter.Capacity, h.IPLimiter.Rate)
	}
	if h.EmailLimiter.Capacity != 9 || h.EmailLimiter.Rate != 1.0/20 {
		t.Errorf("sign-in email limiter: burst %v, %v/s", h.EmailLimiter.Capacity, h.EmailLimiter.Rate)
	}
	if !h.IPLimiter.Allow("a") || !h.IPLimiter.Allow("a") || h.IPLimiter.Allow("a") {
		t.Error("a burst of 2 should allow two and refuse the third")
	}
}
