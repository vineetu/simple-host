package web

import (
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

func TestEmailLimiterAliases(t *testing.T) {
	limiter := httpx.NewRateLimiter(1, 0)
	if !limiter.Allow(model.BaseEmail("Person+first@Example.COM")) {
		t.Fatal("first address denied")
	}
	if limiter.Allow(model.BaseEmail("person+second@example.com")) {
		t.Fatal("alias bypassed limiter")
	}
	if limiter.Allow(model.BaseEmail("person@example.com")) {
		t.Fatal("base address bypassed limiter")
	}
	if !limiter.Allow(model.BaseEmail("different@example.com")) {
		t.Fatal("different address denied")
	}
	if !limiter.Allow(model.BaseEmail("+x@example.com")) || !limiter.Allow(model.BaseEmail("+y@example.com")) {
		t.Fatal("+-prefixed addresses share one bucket")
	}
}
