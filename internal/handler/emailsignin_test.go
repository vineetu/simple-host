package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/oauth"
)

// withEmailSignIn flips the package-level EMAIL_SIGNIN switch for the
// duration of a test and restores it afterward, so tests never leak state
// into one another (the switch is a single process-wide var, the same
// pattern hackMode uses).
func withEmailSignIn(t *testing.T, on bool) {
	t.Helper()
	previous := emailSignInEnabled
	SetEmailSignIn(on)
	t.Cleanup(func() { SetEmailSignIn(previous) })
}

// TestIssueEmailCodeOff: EMAIL_SIGNIN=off refuses every code request before
// anything is validated, looked up or emailed — no DB access at all, which
// this test proves by passing a nil *sql.DB.
func TestIssueEmailCodeOff(t *testing.T) {
	withEmailSignIn(t, false)
	m := &mailbox{}
	limiter := newRateLimiter(1000, 1000)
	_, _, status, body := issueEmailCode(context.Background(), nil, m, limiter, "person@example.com", "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", signupGeoBlock{})
	if status != http.StatusForbidden || body.Code != "email_signin_off" {
		t.Fatalf("got status=%d code=%q, want 403 email_signin_off", status, body.Code)
	}
	if body.Error != "Email sign-in is off on this server. Sign in with your company account." {
		t.Fatalf("unexpected message: %q", body.Error)
	}
	if len(m.codes) != 0 {
		t.Fatal("a refused request must not be emailed a code")
	}
}

// TestVerifyEmailCodeOff_EmailShapeRefused: EMAIL_SIGNIN=off refuses the
// email+code verification shape, again before any DB access.
func TestVerifyEmailCodeOff_EmailShapeRefused(t *testing.T) {
	withEmailSignIn(t, false)
	limiter := newRateLimiter(1000, 1000)
	_, _, status, body := verifyEmailCode(context.Background(), nil, limiter, verifyRequest{Email: "person@example.com", Code: "123456"}, "dashboard", sql.NullString{})
	if status != http.StatusForbidden || body.Code != "email_signin_off" {
		t.Fatalf("got status=%d code=%q, want 403 email_signin_off", status, body.Code)
	}
}

// TestVerifyEmailCodeOff_TokenShapeStillWorks: the magic-link token shape
// (how the company and Google owner flows redeem their one-time token) is a
// different code path from the email+code shape and must keep working
// whether or not EMAIL_SIGNIN is on. Needs DB_DSN: redeeming a real token
// touches the database.
func TestVerifyEmailCodeOff_TokenShapeStillWorks(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	withEmailSignIn(t, false)
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	address := "emailsignin-token-" + stamp + "@example.com"
	code := "654321"
	linkToken := "link-" + stamp
	nonce := "nonce-" + stamp
	nonceHashVal := testNonceHash(nonce)
	if err := db.CreateAuthToken(ctx, database, address, code, linkToken, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, nonceHashVal); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM auth_tokens WHERE email = $1`, address) })
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE username = $1`, address) })

	limiter := newRateLimiter(1000, 1000)
	user, created, status, body := verifyEmailCode(ctx, database, limiter, verifyRequest{Token: linkToken, Nonce: nonce}, "dashboard", sql.NullString{})
	if status != 0 {
		t.Fatalf("token redemption refused while EMAIL_SIGNIN=off: status=%d body=%+v", status, body)
	}
	if !created || user.Username != address {
		t.Fatalf("unexpected user: created=%v user=%+v", created, user)
	}
}

// TestListProvidersOIDC covers GET /v1/auth/oauth/providers with oidc on
// and off, and email_enabled following EmailSignInEnabled rather than bare
// ResendAPIKey. listProviders touches no database.
func TestListProvidersOIDC(t *testing.T) {
	newHandler := func(withOIDC bool, resendKey, emailSignIn string) *OAuthHandler {
		providers := map[string]oauth.Provider{"google": fakeProvider{}}
		if withOIDC {
			providers["oidc"] = oauth.NewCompany(oauth.CompanyConfig{Issuer: "https://idp.example.test", ClientID: "c", ClientSecret: "s", RedirectURI: "https://app.example.test/v1/auth/oauth/oidc/callback"})
		}
		cfg := config.Config{ResendAPIKey: resendKey, EmailSignIn: emailSignIn}
		return &OAuthHandler{cfg: cfg, providers: providers}
	}

	t.Run("oidc off", func(t *testing.T) {
		h := newHandler(false, "resend-key", "on")
		rr := httptest.NewRecorder()
		h.listProviders(rr, httptest.NewRequest("GET", "/v1/auth/oauth/providers", nil))
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if _, has := body["oidc_label"]; has {
			t.Fatalf("oidc_label must be absent when oidc is off: %v", body)
		}
		names := body["providers"].([]any)
		for _, n := range names {
			if n == "oidc" {
				t.Fatalf("oidc listed while off: %v", names)
			}
		}
		if body["email_enabled"] != true {
			t.Fatalf("email_enabled should be true: %v", body)
		}
	})

	t.Run("oidc on carries its label", func(t *testing.T) {
		h := newHandler(true, "resend-key", "on")
		h.cfg.OIDCLabel = "Acme Okta"
		rr := httptest.NewRecorder()
		h.listProviders(rr, httptest.NewRequest("GET", "/v1/auth/oauth/providers", nil))
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["oidc_label"] != "Acme Okta" {
			t.Fatalf("oidc_label = %v, want Acme Okta", body["oidc_label"])
		}
		names := body["providers"].([]any)
		found := false
		for _, n := range names {
			if n == "oidc" {
				found = true
			}
		}
		if !found {
			t.Fatalf("oidc missing from providers: %v", names)
		}
	})

	t.Run("email_enabled follows EMAIL_SIGNIN, not bare ResendAPIKey", func(t *testing.T) {
		h := newHandler(true, "resend-key", "off")
		rr := httptest.NewRecorder()
		h.listProviders(rr, httptest.NewRequest("GET", "/v1/auth/oauth/providers", nil))
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["email_enabled"] != false {
			t.Fatalf("email_enabled should be false with EMAIL_SIGNIN=off: %v", body)
		}
	})

	t.Run("no resend key turns email_enabled off even with EMAIL_SIGNIN=on", func(t *testing.T) {
		h := newHandler(false, "", "on")
		rr := httptest.NewRecorder()
		h.listProviders(rr, httptest.NewRequest("GET", "/v1/auth/oauth/providers", nil))
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["email_enabled"] != false {
			t.Fatalf("email_enabled should be false with no RESEND_API_KEY: %v", body)
		}
	})
}

// TestEmailChangeRefusedWhenEmailSignInOff: with EMAIL_SIGNIN=off no
// emailed code is sent anywhere, so changing the sign-in email (which mails
// codes to both addresses) is refused before any lookup.
func TestEmailChangeRefusedWhenEmailSignInOff(t *testing.T) {
	withEmailSignIn(t, false)
	h := &UserHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/me/email", nil)
	if !h.emailChangeRefused(rec, req, &db.User{Username: "person@example.com"}) {
		t.Fatal("expected the email change to be refused")
	}
	var body errorResponse
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusForbidden || body.Code != "email_signin_off" {
		t.Fatalf("got %d %q, want 403 email_signin_off", rec.Code, body.Code)
	}
}
