package signin

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// withEmailSignIn flips the package-level EMAIL_SIGNIN switch for the
// duration of a test and restores it afterward, so tests never leak state
// into one another (the switch is a single process-wide var).
func withEmailSignIn(t *testing.T, on bool) {
	t.Helper()
	previous := web.EmailSignInEnabled
	web.SetEmailSignIn(on)
	t.Cleanup(func() { web.SetEmailSignIn(previous) })
}

// TestIssueEmailCodeOff: EMAIL_SIGNIN=off refuses every code request before
// anything is validated, looked up or emailed — no DB access at all, which
// this test proves by passing a nil *sql.DB.
func TestIssueEmailCodeOff(t *testing.T) {
	withEmailSignIn(t, false)
	m := &mailbox{}
	limiter := httpx.NewRateLimiter(1000, 1000)
	_, _, status, body := web.IssueEmailCode(context.Background(), nil, m, limiter, "person@example.com", "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
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
	_, _, status, body := web.VerifyEmailCode(context.Background(), nil, limiter, accounts.VerifyCodeRequest{Email: "person@example.com", Code: "123456"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
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
	user, created, status, body := web.VerifyEmailCode(ctx, database, limiter, accounts.VerifyCodeRequest{Token: linkToken, Nonce: nonce}, "dashboard", sql.NullString{}, false, 15*time.Minute)
	if status != 0 {
		t.Fatalf("token redemption refused while EMAIL_SIGNIN=off: status=%d body=%+v", status, body)
	}
	if !created || user.Username != address {
		t.Fatalf("unexpected user: created=%v user=%+v", created, user)
	}
}

// TestIssueEmailCodeGeoBlock covers the required cases for the email-code
// sign-in/sign-up path: a brand-new address from a blocked country is
// refused with no email sent and no token stored; an EXISTING account from
// the same blocked country is refused the same way (owner decision
// 2026-10-01: no carve-out); an unresolved country is allowed; and an empty
// block list is a no-op.
func TestIssueEmailCodeGeoBlock(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	const blockedIP = "198.51.100.9"

	assertBlocked := func(t *testing.T, address string) {
		t.Helper()
		m := &mailbox{}
		geoBlock := geoBlockFor(t, blockedIP, "IQ", []string{"IQ"})
		limiter := httpx.NewRateLimiter(1000, 1000)

		_, _, status, body := web.IssueEmailCode(ctx, database, m, limiter, address, "", "dashboard", sql.NullString{}, sql.NullString{}, blockedIP, geoBlock, 15*time.Minute)
		if status != 403 || body.Code != web.SignupBlockedCode {
			t.Fatalf("got status=%d code=%q, want 403 %s", status, body.Code, web.SignupBlockedCode)
		}
		if _, sent := m.codes[address]; sent {
			t.Fatal("a blocked request must not be emailed a code")
		}
		var n int
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM auth_tokens WHERE email = $1`, address).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("a blocked request must not store an auth token")
		}
	}

	t.Run("new address from a blocked country is refused", func(t *testing.T) {
		assertBlocked(t, "emailgeoblock-new-"+stamp+"@example.com")
	})

	t.Run("existing account from a blocked country is also refused, account untouched", func(t *testing.T) {
		address := "emailgeoblock-existing-" + stamp + "@example.com"
		key, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		existing, err := accountsrepo.CreateUser(ctx, database, address, key, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, existing.ID) })

		assertBlocked(t, address)
	})

	t.Run("unresolved country is allowed", func(t *testing.T) {
		address := "emailgeoblock-unknown-" + stamp + "@example.com"
		m := &mailbox{}
		geoBlock := geoBlockFor(t, blockedIP, "IQ", []string{"IQ"})
		limiter := httpx.NewRateLimiter(1000, 1000)
		// A different, unmapped IP: Lookup finds nothing, ISO is empty, so the
		// request is treated as unresolved and allowed.
		_, _, status, body := web.IssueEmailCode(ctx, database, m, limiter, address, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.77", geoBlock, 15*time.Minute)
		if status != 0 {
			t.Fatalf("unresolved country must be allowed: status=%d body=%+v", status, body)
		}
		if _, sent := m.codes[address]; !sent {
			t.Fatal("an allowed request must still be emailed its code")
		}
	})
}

func TestIssueEmailCodeInvalidEmail(t *testing.T) {
	limiter := httpx.NewRateLimiter(1000, 1000)
	_, _, status, body := web.IssueEmailCode(context.Background(), nil, &mailbox{}, limiter, "not-an-email", "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
	if status != http.StatusBadRequest || body.Code != "invalid_email" {
		t.Fatalf("got status=%d code=%q", status, body.Code)
	}
}

func TestIssueEmailCodeRateLimited(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database := testDB(t)
	addr := "signin-rl-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
	limiter := httpx.NewRateLimiter(1, 0)
	m := &mailbox{}
	_, _, status, body := web.IssueEmailCode(context.Background(), database, m, limiter, addr, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
	if status != 0 {
		t.Fatalf("first issue: status=%d body=%+v", status, body)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, addr) })
	_, _, status, body = web.IssueEmailCode(context.Background(), database, m, limiter, addr, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
	if status != http.StatusTooManyRequests || body.Code != "rate_limited" {
		t.Fatalf("second issue: status=%d code=%q body=%+v", status, body.Code, body)
	}
}

func TestVerifyEmailCodeReasons(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	limiter := httpx.NewRateLimiter(1000, 1000)

	put := func(t *testing.T, label, code string, attempts int) string {
		t.Helper()
		addr := "signin-" + label + "-" + stamp + "@example.com"
		lt, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := accountsrepo.CreateAuthToken(ctx, database, addr, code, lt, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = database.ExecContext(ctx, `DELETE FROM auth_tokens WHERE email = $1`, addr)
			_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE username = $1`, addr)
		})
		if attempts > 0 {
			if _, err := database.ExecContext(ctx, `UPDATE auth_tokens SET attempts = $1 WHERE email = $2`, attempts, addr); err != nil {
				t.Fatal(err)
			}
		}
		return addr
	}

	t.Run("wrong code reports tries_left", func(t *testing.T) {
		addr := put(t, "wrong", "123456", 0)
		_, _, status, body := web.VerifyEmailCode(ctx, database, limiter, accounts.VerifyCodeRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
		if status != http.StatusUnauthorized || body.Reason != web.SignInReasonWrong {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		if body.TriesLeft == nil || *body.TriesLeft != 2 {
			t.Fatalf("tries_left=%v", body.TriesLeft)
		}
		out := web.SignInUnauthorized(body)
		if out.Code != "invalid_code" {
			t.Fatalf("401 code=%q", out.Code)
		}
	})

	t.Run("last try is used up", func(t *testing.T) {
		addr := put(t, "last", "123456", 2)
		_, _, status, body := web.VerifyEmailCode(ctx, database, limiter, accounts.VerifyCodeRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
		if status != http.StatusUnauthorized || body.Reason != web.SignInReasonUsedUp {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		if body.TriesLeft != nil {
			t.Fatalf("used_up must not send tries_left: %+v", body)
		}
	})

	t.Run("no live token is expired", func(t *testing.T) {
		addr := "signin-none-" + stamp + "@example.com"
		_, _, status, body := web.VerifyEmailCode(ctx, database, limiter, accounts.VerifyCodeRequest{Email: addr, Code: "123456"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
		if status != http.StatusUnauthorized || body.Reason != web.SignInReasonExpired {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
	})
}
