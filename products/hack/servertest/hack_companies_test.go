package servertest

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
			"45 seconds, with sound", "Okta", "Google Workspace", "Microsoft Entra", "Keycloak",
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

// TestHackHomeLinksToCompanies: the hosted home page and footer link to
// /companies; a self-hosted install's do not.
func TestHackHomeLinksToCompanies(t *testing.T) {
	for _, base := range []string{"https://simple-hack.app", "https://hack.example.org"} {
		mux := hackHomeTestMux(t, base)
		host := strings.TrimPrefix(base, "https://")
		rec := get(t, mux, host, "/")
		body := rec.Body.String()
		// The companies page pitches running Simple Hack on your own server:
		// only the hosted service links to it (owner, 2026-11-04).
		hosted := base == "https://simple-hack.app"
		if got := strings.Contains(body, `href="/companies"`); got != hosted {
			t.Errorf("%s home page links to /companies = %v, want %v", base, got, hosted)
		}
		if got := strings.Contains(body, "For companies"); got != hosted {
			t.Errorf("%s home page \"For companies\" link text = %v, want %v", base, got, hosted)
		}
	}
}
