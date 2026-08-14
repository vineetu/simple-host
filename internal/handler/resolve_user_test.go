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
	"github.com/vsriram/simple-host/internal/oauth"
)

func TestRedactEmail(t *testing.T) {
	if got := redactEmail("jane@example.com"); got != "j***@example.com" {
		t.Fatalf("got %q", got)
	}
	if got := redactEmail("not-an-email"); got != "***" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveUserMatching(t *testing.T) {
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
	existingEmail := "unify-existing-" + stamp + "@example.com"
	newEmail := "unify-new-" + stamp + "@example.com"

	key, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	existing, err := db.CreateUser(ctx, database, existingEmail, key, false)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UNIFY_KEEP") == "" {
		t.Cleanup(func() {
			_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, existing.ID)
		})
	}

	// Verified email links onto the existing users row; key unchanged.
	linked, created, err := resolveUser(ctx, database, oauth.Identity{
		Provider:      "google",
		UserID:        "sub-existing-" + stamp,
		Email:         existingEmail,
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("verified link: %v", err)
	}
	if created {
		t.Fatal("linking an existing username must not set created")
	}
	if linked.ID != existing.ID || linked.APIKey != existing.APIKey {
		t.Fatalf("linked %+v, want existing id/key", linked)
	}

	// Unverified email must create no user and no identity.
	beforeUsers := countQuery(t, database, `SELECT count(*) FROM users`)
	beforeIDs := countQuery(t, database, `SELECT count(*) FROM oauth_identities`)
	_, _, err = resolveUser(ctx, database, oauth.Identity{
		Provider:      "google",
		UserID:        "sub-unverified-" + stamp,
		Email:         newEmail,
		EmailVerified: false,
	})
	if err != errOAuthEmailRefused {
		t.Fatalf("unverified: got %v, want errOAuthEmailRefused", err)
	}
	afterUsers := countQuery(t, database, `SELECT count(*) FROM users`)
	afterIDs := countQuery(t, database, `SELECT count(*) FROM oauth_identities`)
	if afterUsers != beforeUsers || afterIDs != beforeIDs {
		t.Fatalf("unverified created rows: users %d→%d identities %d→%d", beforeUsers, afterUsers, beforeIDs, afterIDs)
	}
	var n int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE username = $1`, newEmail).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("unverified email created a users row")
	}
}

func countQuery(t *testing.T, database *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
