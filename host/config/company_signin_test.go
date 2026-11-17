package config

import (
	"strings"
	"testing"
)

// loadBase sets the three vars Load() requires and nothing else, so each
// subtest only has to set what it is testing.
func loadBase(t *testing.T) {
	t.Helper()
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("ADMIN_API_KEY", "test-key")
	t.Setenv("SITE_DOMAIN", "simple-host.app")
}

func TestCompanySignInOff(t *testing.T) {
	loadBase(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompanySignInEnabled() {
		t.Fatal("company sign-in must be off with none of OIDC_ISSUER/OIDC_CLIENT_ID/OIDC_CLIENT_SECRET set")
	}
	if got := cfg.EnabledVisitorProviders(); len(got) != 0 {
		t.Fatalf("EnabledVisitorProviders() = %v, want none", got)
	}
	if cfg.OIDCLabel != "your company account" {
		t.Fatalf("OIDCLabel default = %q, want %q", cfg.OIDCLabel, "your company account")
	}
}

func TestCompanySignInPartialIsTreatedAsOff(t *testing.T) {
	loadBase(t)
	t.Setenv("OIDC_ISSUER", "https://idp.example.com")
	// OIDC_CLIENT_ID and OIDC_CLIENT_SECRET left unset: a partial set.
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompanySignInEnabled() {
		t.Fatal("a partial OIDC_* set must be treated as off, not fatal")
	}
	if cfg.OIDCIssuer != "" {
		t.Fatalf("OIDCIssuer should be cleared on a partial set, got %q", cfg.OIDCIssuer)
	}
}

func TestCompanySignInFullSetEnablesIt(t *testing.T) {
	loadBase(t)
	t.Setenv("OIDC_ISSUER", "https://idp.example.com")
	t.Setenv("OIDC_CLIENT_ID", "client-1")
	t.Setenv("OIDC_CLIENT_SECRET", "secret-1")
	t.Setenv("OIDC_LABEL", "Acme Okta")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CompanySignInEnabled() {
		t.Fatal("all three OIDC_* vars set should turn company sign-in on")
	}
	if cfg.OIDCLabel != "Acme Okta" {
		t.Fatalf("OIDCLabel = %q, want Acme Okta", cfg.OIDCLabel)
	}
	got := cfg.EnabledVisitorProviders()
	if len(got) != 1 || got[0] != "oidc" {
		t.Fatalf("EnabledVisitorProviders() = %v, want [oidc]", got)
	}
	if cfg.OAuthRedirectURI("oidc") != "https://simple-host.app/v1/auth/oauth/oidc/callback" {
		t.Fatalf("OAuthRedirectURI(oidc) = %q", cfg.OAuthRedirectURI("oidc"))
	}
}

func TestCompanySignInGoogleIssuerNeedsAllowedDomains(t *testing.T) {
	loadBase(t)
	t.Setenv("OIDC_ISSUER", "https://accounts.google.com")
	t.Setenv("OIDC_CLIENT_ID", "client-1")
	t.Setenv("OIDC_CLIENT_SECRET", "secret-1")
	if _, err := Load(); err == nil {
		t.Fatal("a Google issuer without ALLOWED_EMAIL_DOMAINS must be fatal")
	}

	t.Setenv("ALLOWED_EMAIL_DOMAINS", "example.com, Example.co.uk")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Google issuer with ALLOWED_EMAIL_DOMAINS should load: %v", err)
	}
	if !cfg.CompanySignInEnabled() {
		t.Fatal("expected company sign-in on")
	}
	want := []string{"example.com", "example.co.uk"}
	if strings.Join(cfg.AllowedEmailDomains, ",") != strings.Join(want, ",") {
		t.Fatalf("AllowedEmailDomains = %v, want %v (trimmed, lower-cased)", cfg.AllowedEmailDomains, want)
	}
}

func TestCompanySignInPlainHTTPNeedsInsecureAllowed(t *testing.T) {
	loadBase(t)
	t.Setenv("OIDC_ISSUER", "http://idp.internal.example.com")
	t.Setenv("OIDC_CLIENT_ID", "client-1")
	t.Setenv("OIDC_CLIENT_SECRET", "secret-1")
	if _, err := Load(); err == nil {
		t.Fatal("a plain-http issuer without OIDC_INSECURE_ALLOWED=true must be fatal")
	}

	t.Setenv("OIDC_INSECURE_ALLOWED", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("plain-http issuer with OIDC_INSECURE_ALLOWED=true should load: %v", err)
	}
	if !cfg.OIDCInsecureAllowed {
		t.Fatal("OIDCInsecureAllowed should be true")
	}
	if !cfg.CompanySignInEnabled() {
		t.Fatal("expected company sign-in on")
	}
}

func TestEmailSignInOffNeedsCompanySignIn(t *testing.T) {
	loadBase(t)
	t.Setenv("EMAIL_SIGNIN", "off")
	if _, err := Load(); err == nil {
		t.Fatal("EMAIL_SIGNIN=off without company sign-in configured must be fatal")
	}

	t.Setenv("OIDC_ISSUER", "https://idp.example.com")
	t.Setenv("OIDC_CLIENT_ID", "client-1")
	t.Setenv("OIDC_CLIENT_SECRET", "secret-1")
	t.Setenv("RESEND_API_KEY", "resend-key")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("EMAIL_SIGNIN=off with company sign-in configured should load: %v", err)
	}
	if cfg.EmailSignInEnabled() {
		t.Fatal("EmailSignInEnabled() must be false with EMAIL_SIGNIN=off, even with a Resend key set")
	}
}

func TestEmailSignInDefaultsOnAndNeedsResendKey(t *testing.T) {
	loadBase(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmailSignIn != "on" {
		t.Fatalf("EmailSignIn default = %q, want on", cfg.EmailSignIn)
	}
	if cfg.EmailSignInEnabled() {
		t.Fatal("EmailSignInEnabled() must be false with no RESEND_API_KEY even though EMAIL_SIGNIN is on")
	}
	t.Setenv("RESEND_API_KEY", "resend-key")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EmailSignInEnabled() {
		t.Fatal("EmailSignInEnabled() should be true with a Resend key and EMAIL_SIGNIN=on (the default)")
	}
}

func TestEmailSignInInvalidValueWarnsAndDefaultsOn(t *testing.T) {
	loadBase(t)
	t.Setenv("EMAIL_SIGNIN", "maybe")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmailSignIn != "on" {
		t.Fatalf("EMAIL_SIGNIN=maybe should be treated as on, got %q", cfg.EmailSignIn)
	}
}
