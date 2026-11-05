package accounts

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	repo "github.com/vsriram/simple-host/internal/repository/accounts"
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

	key, err := issuedAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	existing, err := repo.CreateUser(ctx, database, existingEmail, key, false)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UNIFY_KEEP") == "" {
		t.Cleanup(func() {
			_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, existing.ID)
		})
	}

	// Verified email links onto the existing users row; key unchanged.
	linked, created, err := resolveUser(ctx, database, ProviderIdentity{
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
	if linked.ID != existing.ID {
		t.Fatalf("linked %+v, want existing id", linked)
	}
	if u, err := New(database).GetUserByAPIKey(ctx, key, 180*24*time.Hour); err != nil || u.ID != existing.ID {
		t.Fatalf("existing key after link: %v %+v", err, u)
	}

	// Unverified email must create no user and no identity.
	beforeUsers := countQuery(t, database, `SELECT count(*) FROM users`)
	beforeIDs := countQuery(t, database, `SELECT count(*) FROM oauth_identities`)
	_, _, err = resolveUser(ctx, database, ProviderIdentity{
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

// TestResolveUserRefusesPasswordAccountLink covers the pre-account-hijacking
// fix on the OAuth/OIDC side (2026-11-05, redesigned): a password account
// under an address never proved it controls that address, so a provider's
// verified email for the same address must never link to or resolve into
// it. Uses an admin-created password account, since a self sign-up's
// users.username is now a synthetic value and could never match email here
// in the first place.
func TestResolveUserRefusesPasswordAccountLink(t *testing.T) {
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
	login := "resolve-password-" + stamp + "@example.com"

	user, _, err := New(database).CreatePasswordAccount(ctx, login, "", true, false, testAssignHandle)
	if err != nil {
		t.Fatalf("create admin password account: %v", err)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM users WHERE id=$1`, user.ID) })

	beforeIDs := countQuery(t, database, `SELECT count(*) FROM oauth_identities`)
	_, _, err = resolveUser(ctx, database, ProviderIdentity{
		Provider:      "google",
		UserID:        "sub-password-link-" + stamp,
		Email:         login,
		EmailVerified: true,
	})
	if err != errOAuthPasswordRefused {
		t.Fatalf("link to password account: got %v, want errOAuthPasswordRefused", err)
	}
	if after := countQuery(t, database, `SELECT count(*) FROM oauth_identities`); after != beforeIDs {
		t.Fatalf("refused link still inserted an identity: %d→%d", beforeIDs, after)
	}
}

// TestResolveUserRefusesExistingIdentityToPasswordAccount is defence in
// depth for resolveUser's existing-identity branch: that branch should
// never actually be able to reach a password account (the first-link check
// above already refuses linking one), but if that invariant ever broke,
// this proves the existing-identity branch's own check still refuses it,
// rather than silently signing the caller into a password account.
func TestResolveUserRefusesExistingIdentityToPasswordAccount(t *testing.T) {
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
	login := "resolve-identity-" + stamp + "@example.com"

	user, _, err := New(database).CreatePasswordAccount(ctx, login, "", true, false, testAssignHandle)
	if err != nil {
		t.Fatalf("create admin password account: %v", err)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM users WHERE id=$1`, user.ID) })

	// An OAuth identity already linked to it: impossible through the
	// normal flow (the first-link check refuses this), so inserted
	// directly to exercise the existing-identity branch's own check.
	providerUserID := "sub-password-existing-" + stamp
	if _, err := repo.InsertOAuthIdentity(ctx, database, user.ID, "google", providerUserID, login, true); err != nil {
		t.Fatalf("insert oauth identity: %v", err)
	}

	_, _, err = resolveUser(ctx, database, ProviderIdentity{
		Provider:      "google",
		UserID:        providerUserID,
		Email:         login,
		EmailVerified: true,
	})
	if err != errOAuthPasswordRefused {
		t.Fatalf("existing identity to a password account: got %v, want errOAuthPasswordRefused", err)
	}
}

// TestCreateAccountsCaseVariantDoesNotAbortBatch is a regression guard for
// the password redesign (2026-11-05): round 2 of the password review had
// added a lower(username) unique index, since removed in favour of
// account_passwords.login, that would have made a case-insensitive
// duplicate inside a bulk-create batch -- or against an existing username
// -- abort the whole request with an unhandled unique violation instead of
// being handled one name at a time. Without that index, a case-variant of
// an existing username is simply a different username and is created as
// its own, separate account; only an exact-case duplicate is skipped.
func TestCreateAccountsCaseVariantDoesNotAbortBatch(t *testing.T) {
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
	existing := "ada-" + stamp + "@example.com"
	mixedCase := "Ada-" + stamp + "@Example.com"
	fresh := "other-" + stamp + "@example.com"

	key, err := issuedAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	existingUser, err := repo.CreateUser(ctx, database, existing, key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM users WHERE id=$1`, existingUser.ID) })

	out, err := New(database).CreateAccounts(ctx, []string{mixedCase, fresh, existing}, false, testAssignHandle)
	if err != nil {
		t.Fatalf("batch containing a case-variant of an existing username aborted: %v", err)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM users WHERE username = ANY($1)`, []string{mixedCase, fresh}) })

	created, _ := out["created"].([]map[string]string)
	skipped, _ := out["skipped"].([]map[string]string)
	createdNames := map[string]bool{}
	for _, c := range created {
		createdNames[c["username"]] = true
	}
	if !createdNames[mixedCase] {
		t.Fatalf("case-variant of an existing username was not created as its own account: %+v", out)
	}
	if !createdNames[fresh] {
		t.Fatalf("an ordinary new name in the same batch was not created: %+v", out)
	}
	skippedNames := map[string]bool{}
	for _, s := range skipped {
		skippedNames[s["username"]] = true
	}
	if !skippedNames[existing] {
		t.Fatalf("the exact-case duplicate was not skipped: %+v", out)
	}
}

// testAssignHandle is a minimal, always-succeeding handle assignment for
// tests that create accounts directly through the Service (bypassing the
// handler's real assignHandle/SanitizeHandleBase, which lives in
// internal/app/sites and cannot be imported here: it imports this package).
// CreatePasswordAccount and CreateAccounts both fail the whole call when no
// handle lands, so a real assign is required, not a no-op. The account's
// own id (always unique, always lowercase hex at its first 8 characters) is
// a cheap valid, collision-free handle.
func testAssignHandle(ctx context.Context, q AttributionStore, id, _ string, _ bool) {
	n := 8
	if len(id) < n {
		n = len(id)
	}
	_, _ = repo.ClaimHandle(ctx, q, id, "h"+id[:n])
}

func countQuery(t *testing.T, database *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
