package gate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedHostVisitorEndpointsIgnoreCookie(t *testing.T) {
	h := &Handler{ContentHost: "sites.simple-host.app"}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		body    string
	}{
		{"request email", h.requestVisitorEmail, 400, `"code":"custom_domain_required"`},
		{"verify email", h.verifyVisitorEmail, 400, `"code":"custom_domain_required"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://SITES.simple-host.app:443/v1/sites/demo/me", nil)
			r.AddCookie(&http.Cookie{Name: VisitorCookieHost, Value: strings.Repeat("ab", 32)})
			w := httptest.NewRecorder()
			tc.handler(w, r) // No database: consulting a session would fail this test.
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestOAuthSharedHostReturnRejected(t *testing.T) {
	h := &OAuthHandler{Cfg: OAuthSettings{PublicBaseURL: "https://simple-host.app", SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"}}
	_, _, _, _, err := h.SanitizeReturnTo(context.Background(), "https://sites.simple-host.app/alice/blog/")
	if err != errInvalidReturnTo {
		t.Fatalf("got %v", err)
	}
	cn := strings.Repeat("A", 43)
	_, _, _, purpose, err := h.SanitizeReturnTo(context.Background(), "https://simple-host.app/?cn="+cn)
	if err != nil || purpose != "owner" {
		t.Fatalf("owner: %q %v", purpose, err)
	}
	// The dashboard return must carry the tab's nonce hash (login-CSRF fix).
	if _, _, _, _, err := h.SanitizeReturnTo(context.Background(), "https://simple-host.app/"); err != errInvalidReturnTo {
		t.Fatalf("bare dashboard return accepted: %v", err)
	}
}
