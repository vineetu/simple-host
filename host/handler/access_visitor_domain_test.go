package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

func TestSharedHostVisitorEndpointsIgnoreCookie(t *testing.T) {
	h := &AccessHandler{contentHost: "sites.simple-host.app"}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		body    string
	}{
		{"request email", h.requestVisitorEmail, 400, `{"error":"sign in on the site's own address, not the shared address","code":"custom_domain_required"}`},
		{"verify email", h.verifyVisitorEmail, 400, `{"error":"sign in on the site's own address, not the shared address","code":"custom_domain_required"}`},
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
	h := &OAuthHandler{cfg: OAuthSettings{PublicBaseURL: "https://simple-host.app", SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"}}
	_, _, _, _, err := h.sanitizeReturnTo(context.Background(), "https://sites.simple-host.app/alice/blog/")
	if err != errInvalidReturnTo {
		t.Fatalf("got %v", err)
	}
	cn := strings.Repeat("A", 43)
	_, _, _, purpose, err := h.sanitizeReturnTo(context.Background(), "https://simple-host.app/?cn="+cn)
	if err != nil || purpose != "owner" {
		t.Fatalf("owner: %q %v", purpose, err)
	}
	// The dashboard return must carry the tab's nonce hash (login-CSRF fix).
	if _, _, _, _, err := h.sanitizeReturnTo(context.Background(), "https://simple-host.app/"); err != errInvalidReturnTo {
		t.Fatalf("bare dashboard return accepted: %v", err)
	}
}

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
