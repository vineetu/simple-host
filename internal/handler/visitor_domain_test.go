package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

func TestSharedHostVisitorEndpointsIgnoreCookie(t *testing.T) {
	h := &SiteHandler{contentHost: "sites.simple-host.app"}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		body    string
	}{
		{"request email", h.requestVisitorEmail, 400, `{"code":"custom_domain_required","error":"sign-in needs a custom domain"}`},
		{"verify email", h.verifyVisitorEmail, 400, `{"code":"custom_domain_required","error":"sign-in needs a custom domain"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://SITES.simple-host.app:443/v1/sites/demo/me", nil)
			r.AddCookie(&http.Cookie{Name: visitorCookieHost, Value: strings.Repeat("ab", 32)})
			w := httptest.NewRecorder()
			tc.handler(w, r) // No database: consulting a session would fail this test.
			if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != tc.body {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestOAuthSharedHostReturnRejected(t *testing.T) {
	h := &OAuthHandler{cfg: config.Config{PublicBaseURL: "https://simple-host.app", SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"}}
	_, _, _, _, err := h.sanitizeReturnTo(context.Background(), "https://sites.simple-host.app/alice/blog/")
	if err != errInvalidReturnTo {
		t.Fatalf("got %v", err)
	}
	_, _, _, purpose, err := h.sanitizeReturnTo(context.Background(), "https://simple-host.app/")
	if err != nil || purpose != "owner" {
		t.Fatalf("owner: %q %v", purpose, err)
	}
}

func TestEmailLimiterAliases(t *testing.T) {
	limiter := newRateLimiter(1, 0)
	if !limiter.allow(emailLimiterKey("Person+first@Example.COM")) {
		t.Fatal("first address denied")
	}
	if limiter.allow(emailLimiterKey("person+second@example.com")) {
		t.Fatal("alias bypassed limiter")
	}
	if limiter.allow(emailLimiterKey("person@example.com")) {
		t.Fatal("base address bypassed limiter")
	}
	if !limiter.allow(emailLimiterKey("different@example.com")) {
		t.Fatal("different address denied")
	}
}
