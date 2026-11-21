package accesshttp

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/httpx"
)

func TestIssueEmailCodeInvalidEmail(t *testing.T) {
	limiter := httpx.NewRateLimiter(1000, 1000)
	_, _, status, body := issueEmailCode(context.Background(), nil, &mailbox{}, limiter, "not-an-email", "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
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
	_, _, status, body := issueEmailCode(context.Background(), database, m, limiter, addr, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
	if status != 0 {
		t.Fatalf("first issue: status=%d body=%+v", status, body)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, addr) })
	_, _, status, body = issueEmailCode(context.Background(), database, m, limiter, addr, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", accounts.SignupGeoBlock{}, 15*time.Minute)
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
		_, _, status, body := VerifyEmailCode(ctx, database, limiter, VerifyRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
		if status != http.StatusUnauthorized || body.Reason != SignInReasonWrong {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		if body.TriesLeft == nil || *body.TriesLeft != 2 {
			t.Fatalf("tries_left=%v", body.TriesLeft)
		}
		out := signInUnauthorized(body)
		if out.Code != "invalid_code" {
			t.Fatalf("401 code=%q", out.Code)
		}
	})

	t.Run("last try is used up", func(t *testing.T) {
		addr := put(t, "last", "123456", 2)
		_, _, status, body := VerifyEmailCode(ctx, database, limiter, VerifyRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
		if status != http.StatusUnauthorized || body.Reason != signInReasonUsedUp {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		if body.TriesLeft != nil {
			t.Fatalf("used_up must not send tries_left: %+v", body)
		}
	})

	t.Run("no live token is expired", func(t *testing.T) {
		addr := "signin-none-" + stamp + "@example.com"
		_, _, status, body := VerifyEmailCode(ctx, database, limiter, VerifyRequest{Email: addr, Code: "123456"}, "dashboard", sql.NullString{}, false, 15*time.Minute)
		if status != http.StatusUnauthorized || body.Reason != signInReasonExpired {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
	})
}
