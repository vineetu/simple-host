package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

func TestSignInMessagesTable(t *testing.T) {
	keys := []string{
		"code_wrong", "code_used_up", "code_expired", "rate_limited", "invalid_email",
		"send_failed", "signup_blocked", "email_signin_off", "email_unavailable",
		"link_other_browser", "account_suspended", "network", "unknown",
	}
	if len(signInMessages) != len(keys) {
		t.Errorf("table has %d keys, want %d", len(signInMessages), len(keys))
	}
	for _, k := range keys {
		v, ok := signInMessages[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if strings.TrimSpace(v) == "" {
			t.Errorf("%s is empty", k)
		}
		if strings.Contains(v, "\u2014") {
			t.Errorf("%s contains an em-dash: %q", k, v)
		}
	}
	if signInMessages["signup_blocked"] != signupBlockedMessage {
		t.Fatal("signup_blocked must keep signupBlockedMessage")
	}
	if signInMessages["email_signin_off"] != errEmailSignInOff.Error {
		t.Fatal("email_signin_off must keep errEmailSignInOff")
	}
	if signInMessages["link_other_browser"] != errLinkOtherBrowser {
		t.Fatal("link_other_browser must keep errLinkOtherBrowser")
	}
}

func TestSignInCodeSentMessage(t *testing.T) {
	got := signInCodeSentMessage("ada@example.com")
	if got != "We sent a 6-digit code to ada@example.com." {
		t.Fatalf("got %q", got)
	}
}

func TestSignInCodeWrongCopy(t *testing.T) {
	two := signInCodeWrong(2)
	if two.Reason != signInReasonWrong || two.TriesLeft == nil || *two.TriesLeft != 2 {
		t.Fatalf("2 tries: %+v", two)
	}
	if two.Error != "That code is not right. 2 tries left." {
		t.Fatalf("2 tries copy: %q", two.Error)
	}
	one := signInCodeWrong(1)
	if one.Error != "That code is not right. 1 try left." {
		t.Fatalf("1 try copy: %q", one.Error)
	}
}

func signInTestDB(t *testing.T) *sql.DB {
	t.Helper()
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
	return database
}

func TestIssueEmailCodeInvalidEmail(t *testing.T) {
	limiter := newRateLimiter(1000, 1000)
	_, _, status, body := issueEmailCode(context.Background(), nil, &mailbox{}, limiter, "not-an-email", "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", signupGeoBlock{})
	if status != http.StatusBadRequest || body.Code != "invalid_email" {
		t.Fatalf("got status=%d code=%q", status, body.Code)
	}
	if body.Error != signInMessages["invalid_email"] {
		t.Fatalf("copy: %q", body.Error)
	}
}

func TestIssueEmailCodeRateLimited(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database := signInTestDB(t)
	addr := "signin-rl-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
	limiter := newRateLimiter(1, 0)
	m := &mailbox{}
	_, _, status, body := issueEmailCode(context.Background(), database, m, limiter, addr, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", signupGeoBlock{})
	if status != 0 {
		t.Fatalf("first issue: status=%d body=%+v", status, body)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, addr) })
	_, _, status, body = issueEmailCode(context.Background(), database, m, limiter, addr, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.5", signupGeoBlock{})
	if status != http.StatusTooManyRequests || body.Code != "rate_limited" {
		t.Fatalf("second issue: status=%d code=%q body=%+v", status, body.Code, body)
	}
	if body.Error != signInMessages["rate_limited"] {
		t.Fatalf("429 copy: %q", body.Error)
	}
}

func TestVerifyEmailCodeReasons(t *testing.T) {
	database := signInTestDB(t)
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	limiter := newRateLimiter(1000, 1000)

	put := func(t *testing.T, label, code string, attempts int) string {
		t.Helper()
		addr := "signin-" + label + "-" + stamp + "@example.com"
		lt, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.CreateAuthToken(ctx, database, addr, code, lt, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
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
		_, _, status, body := verifyEmailCode(ctx, database, limiter, verifyRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{})
		if status != http.StatusUnauthorized || body.Reason != signInReasonWrong {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		if body.TriesLeft == nil || *body.TriesLeft != 2 {
			t.Fatalf("tries_left=%v", body.TriesLeft)
		}
		if body.Error != "That code is not right. 2 tries left." {
			t.Fatalf("copy: %q", body.Error)
		}
		out := signInUnauthorized(body)
		if out.Code != "invalid_code" {
			t.Fatalf("401 code=%q", out.Code)
		}
	})

	t.Run("second wrong is 1 try left", func(t *testing.T) {
		addr := put(t, "second", "123456", 1)
		_, _, status, body := verifyEmailCode(ctx, database, limiter, verifyRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{})
		if status != http.StatusUnauthorized || body.Reason != signInReasonWrong || body.TriesLeft == nil || *body.TriesLeft != 1 {
			t.Fatalf("status=%d reason=%q tries=%v body=%+v", status, body.Reason, body.TriesLeft, body)
		}
		if body.Error != "That code is not right. 1 try left." {
			t.Fatalf("copy: %q", body.Error)
		}
	})

	t.Run("last try is used up", func(t *testing.T) {
		addr := put(t, "last", "123456", 2)
		_, _, status, body := verifyEmailCode(ctx, database, limiter, verifyRequest{Email: addr, Code: "000000"}, "dashboard", sql.NullString{})
		if status != http.StatusUnauthorized || body.Reason != signInReasonUsedUp {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		if body.TriesLeft != nil {
			t.Fatalf("used_up must not send tries_left: %+v", body)
		}
		if body.Error != signInMessages["code_used_up"] {
			t.Fatalf("copy: %q", body.Error)
		}
	})

	t.Run("already at cap is used up even with the right code", func(t *testing.T) {
		addr := put(t, "cap", "123456", 3)
		_, _, status, body := verifyEmailCode(ctx, database, limiter, verifyRequest{Email: addr, Code: "123456"}, "dashboard", sql.NullString{})
		if status != http.StatusUnauthorized || body.Reason != signInReasonUsedUp {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
	})

	t.Run("no live token is expired", func(t *testing.T) {
		addr := "signin-none-" + stamp + "@example.com"
		_, _, status, body := verifyEmailCode(ctx, database, limiter, verifyRequest{Email: addr, Code: "123456"}, "dashboard", sql.NullString{})
		if status != http.StatusUnauthorized || body.Reason != signInReasonExpired {
			t.Fatalf("status=%d reason=%q body=%+v", status, body.Reason, body)
		}
		want := strings.ReplaceAll(signInMessages["code_expired"], "{min}", strconv.Itoa(signInCodeTTLMinutes()))
		if body.Error != want {
			t.Fatalf("copy: %q want %q", body.Error, want)
		}
	})
}

func TestSignInHTTPMessageAnd401(t *testing.T) {
	mb := &mailbox{}
	a := newPrivateAppMailer(t, mb)
	addr := "signin-http-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
	t.Cleanup(func() {
		_, _ = a.database.Exec(`DELETE FROM auth_tokens WHERE email = $1`, addr)
		_, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, addr)
	})
	r := a.at(t, "POST", a.appHost(), "/v1/auth", map[string]string{"email": addr}, nil)
	if r.status != http.StatusAccepted {
		t.Fatalf("request: %d %s", r.status, r.body)
	}
	got := r.json(t)
	if got["message"] != signInCodeSentMessage(addr) || got["email"] != addr {
		t.Fatalf("202 body: %s", r.body)
	}
	r = a.at(t, "POST", a.appHost(), "/v1/auth", map[string]string{"email": "nope"}, nil)
	if r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_email" {
		t.Fatalf("invalid email: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", a.appHost(), "/v1/auth/verify", map[string]string{"email": addr, "code": "000000"}, nil)
	if r.status != http.StatusUnauthorized {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
	var body errorResponse
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "invalid_code" || body.Reason != signInReasonWrong || body.TriesLeft == nil || *body.TriesLeft != 2 {
		t.Fatalf("401 shape: %+v %s", body, r.body)
	}
	if body.Error != "That code is not right. 2 tries left." {
		t.Fatalf("401 copy: %q", body.Error)
	}
}
