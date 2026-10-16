package handler

import (
	"net/http"
	"strings"
	"testing"
)

// TestHackCompaniesPage: GET /companies answers 200 with the page's title,
// both on the canonical hosted service and on a self-hosted Simple Hack
// instance, because the page describes the standalone package itself and
// should still make sense wherever it is reached.
func TestHackCompaniesPage(t *testing.T) {
	for _, base := range []string{"https://simple-hack.app", "https://hack.example.org"} {
		mux := hackHomeTestMux(t, base)
		host := strings.TrimPrefix(base, "https://")
		rec := get(t, mux, host, "/companies")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /companies: status %d", base, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{
			"Simple Hack for companies", "id=\"bigPlay\"", "id=\"playBtn\"", "id=\"endCard\"",
			"42 seconds, with sound", "Okta", "Google Workspace", "Microsoft Entra", "Keycloak",
			"OpenID Connect", "OIDC_ISSUER", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET",
			"ALLOWED_EMAIL_DOMAINS", "OIDC_LABEL", "EMAIL_SIGNIN", "--tls internal", "--ca-cert",
			"deploy/hack/standalone/install.sh", "docs/platforms/simple-hack-standalone.md",
			"2 GB", "Docker", "class=\"sh-header sh-hack\"", "class=\"sh-footer\"", "/hack-ink.css?v=",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s /companies missing %q", base, want)
			}
		}
		for _, banned := range []string{"pricing", "testimonial"} {
			if strings.Contains(strings.ToLower(body), banned) {
				t.Errorf("%s /companies contains banned word %q", base, banned)
			}
		}
	}
}

// TestHackHomeLinksToCompanies: the home page (both the canonical video home
// and the self-hosted drawn-film home) carries a plain link to /companies,
// and the shared footer does too.
func TestHackHomeLinksToCompanies(t *testing.T) {
	for _, base := range []string{"https://simple-hack.app", "https://hack.example.org"} {
		mux := hackHomeTestMux(t, base)
		host := strings.TrimPrefix(base, "https://")
		rec := get(t, mux, host, "/")
		body := rec.Body.String()
		if !strings.Contains(body, `href="/companies"`) {
			t.Errorf("%s home page does not link to /companies", base)
		}
		if !strings.Contains(body, "For companies") {
			t.Errorf("%s home page missing the \"For companies\" link text", base)
		}
	}
}
