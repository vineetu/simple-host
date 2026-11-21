package accesshttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/httpx"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// withEmailSignIn flips the package-level EMAIL_SIGNIN switch for the
// duration of a test and restores it afterward, so tests never leak state
// into one another (the switch is a single process-wide var).
func withEmailSignIn(t *testing.T, on bool) {
	t.Helper()
	previous := web.EmailSignInEnabled
	SetEmailSignIn(on)
	t.Cleanup(func() { SetEmailSignIn(previous) })
}

// TestIssueEmailCodeOff: EMAIL_SIGNIN=off refuses every code request before
// anything is validated, looked up or emailed — no DB access at all, which
// this test proves by passing a nil *sql.DB.
func TestIssueEmailCodeOff(t *testing.T) {
	withEmailSignIn(t, false)
	m := &mailbox{}
	limiter := httpx.NewRateLimiter(1000, 1000)
	_, _, status, body := issueEmailCode(context.Background(), nil, m, limiter, "person@example.com", "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
	if status != http.StatusForbidden || body.Code != "email_signin_off" {
		t.Fatalf("got status=%d code=%q, want 403 email_signin_off", status, body.Code)
	}
	if len(m.codes) != 0 {
		t.Fatal("a refused request must not be emailed a code")
	}
}

// TestVerifyEmailCodeOff_EmailShapeRefused: EMAIL_SIGNIN=off refuses the
// email+code verification shape, again before any DB access.
func TestVerifyEmailCodeOff_EmailShapeRefused(t *testing.T) {
	withEmailSignIn(t, false)
	limiter := httpx.NewRateLimiter(1000, 1000)
	_, _, status, body := VerifyEmailCode(context.Background(), nil, limiter, VerifyRequest{Email: "person@example.com", Code: "123456"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
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
	database := testDB(t)
	withEmailSignIn(t, false)
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	address := "emailsignin-token-" + stamp + "@example.com"
	code := "654321"
	linkToken := "link-" + stamp
	nonce := "nonce-" + stamp
	nonceHashVal := testNonceHash(nonce)
	if err := accountsrepo.CreateAuthToken(ctx, database, address, code, linkToken, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, nonceHashVal); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM auth_tokens WHERE email = $1`, address) })
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE username = $1`, address) })

	limiter := httpx.NewRateLimiter(1000, 1000)
	user, created, status, body := VerifyEmailCode(ctx, database, limiter, VerifyRequest{Token: linkToken, Nonce: nonce}, "dashboard", sql.NullString{}, false, 15*time.Minute)
	if status != 0 {
		t.Fatalf("token redemption refused while EMAIL_SIGNIN=off: status=%d body=%+v", status, body)
	}
	if !created || user.Username != address {
		t.Fatalf("unexpected user: created=%v user=%+v", created, user)
	}
}

// GET /v1/auth/oauth/providers: oidc carries its label only when on, and
// email_enabled needs both EMAIL_SIGNIN and a mail key.
func TestListProvidersOIDC(t *testing.T) {
	list := func(withOIDC bool, resendKey, emailSignIn string) map[string]any {
		providers := map[string]accounts.Provider{"google": fakeProvider{}}
		if withOIDC {
			providers["oidc"] = ssoOIDCProvider{}
		}
		cfg := config.Config{ResendAPIKey: resendKey, EmailSignIn: emailSignIn}
		h := &OAuthHandler{Cfg: testOAuthSettings(cfg, accounts.New(nil)), SignIn: accounts.NewProviderSignIn(accounts.New(nil), providers)}
		h.Cfg.OIDCLabel = "Acme Okta"
		rr := httptest.NewRecorder()
		h.listProviders(rr, httptest.NewRequest("GET", "/v1/auth/oauth/providers", nil))
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	off := list(false, "resend-key", "on")
	if _, has := off["oidc_label"]; has || off["email_enabled"] != true {
		t.Fatalf("oidc off: %v", off)
	}
	if on := list(true, "resend-key", "off"); on["oidc_label"] != "Acme Okta" || on["email_enabled"] != false {
		t.Fatalf("oidc on, EMAIL_SIGNIN=off: %v", on)
	}
	if nokey := list(false, "", "on"); nokey["email_enabled"] != false {
		t.Fatalf("no mail key: %v", nokey)
	}
}

// TestEmailChangeRefusedWhenEmailSignInOff: with EMAIL_SIGNIN=off no
// emailed code is sent anywhere, so changing the sign-in email (which mails
// codes to both addresses) is refused before any lookup.
func TestEmailChangeRefusedWhenEmailSignInOff(t *testing.T) {
	withEmailSignIn(t, false)
	h := &UserHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/me/email", nil)
	if !h.emailChangeRefused(rec, req, &model.User{Username: "person@example.com"}) {
		t.Fatal("expected the email change to be refused")
	}
	var body httpx.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusForbidden || body.Code != "email_signin_off" {
		t.Fatalf("got %d %q, want 403 email_signin_off", rec.Code, body.Code)
	}
}
