package handler

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// TestIssueEmailCodeGeoBlock covers the required cases for the email-code
// sign-in/sign-up path: a brand-new address from a blocked country is
// refused with no email sent and no token stored; an EXISTING account from
// the same blocked country is refused the same way (owner decision
// 2026-10-01: no carve-out); an unresolved country is allowed; and an empty
// block list is a no-op.
func TestIssueEmailCodeGeoBlock(t *testing.T) {
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
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	const blockedIP = "198.51.100.9"

	assertBlocked := func(t *testing.T, address string) {
		t.Helper()
		m := &mailbox{}
		geoBlock := geoBlockFor(t, blockedIP, "IQ", []string{"IQ"})
		limiter := newRateLimiter(1000, 1000)

		_, _, status, body := issueEmailCode(ctx, database, m, limiter, address, "", "dashboard", sql.NullString{}, sql.NullString{}, blockedIP, geoBlock)
		if status != 403 || body.Code != signupBlockedCode {
			t.Fatalf("got status=%d code=%q, want 403 %s", status, body.Code, signupBlockedCode)
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
		existing, err := db.CreateUser(ctx, database, address, key, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, existing.ID) })

		assertBlocked(t, address)

		// The account itself is untouched: same id, same key.
		fresh, err := db.GetUserByAPIKey(ctx, database, key)
		if err != nil || fresh.ID != existing.ID {
			t.Fatalf("existing account changed by a refused sign-in attempt: %v %+v", err, fresh)
		}
	})

	t.Run("unresolved country is allowed", func(t *testing.T) {
		address := "emailgeoblock-unknown-" + stamp + "@example.com"
		m := &mailbox{}
		geoBlock := geoBlockFor(t, blockedIP, "IQ", []string{"IQ"})
		limiter := newRateLimiter(1000, 1000)
		// A different, unmapped IP: Lookup finds nothing, ISO is empty, so the
		// request is treated as unresolved and allowed.
		_, _, status, body := issueEmailCode(ctx, database, m, limiter, address, "", "dashboard", sql.NullString{}, sql.NullString{}, "203.0.113.77", geoBlock)
		if status != 0 {
			t.Fatalf("unresolved country must be allowed: status=%d body=%+v", status, body)
		}
		if _, sent := m.codes[address]; !sent {
			t.Fatal("an allowed request must still be emailed its code")
		}
	})

	t.Run("empty block list is off", func(t *testing.T) {
		address := "emailgeoblock-off-" + stamp + "@example.com"
		m := &mailbox{}
		limiter := newRateLimiter(1000, 1000)
		_, _, status, body := issueEmailCode(ctx, database, m, limiter, address, "", "dashboard", sql.NullString{}, sql.NullString{}, blockedIP, signupGeoBlock{})
		if status != 0 {
			t.Fatalf("empty block list must be a no-op: status=%d body=%+v", status, body)
		}
		if _, sent := m.codes[address]; !sent {
			t.Fatal("signup must proceed when the knob is empty")
		}
	})
}
