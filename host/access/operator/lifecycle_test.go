package operator

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

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

	out, err := CreateAccounts(ctx, database, []string{mixedCase, fresh, existing}, false, testAssignHandle)
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
// host/app/sites and cannot be imported here: it imports this package).
// CreatePasswordAccount and CreateAccounts both fail the whole call when no
// handle lands, so a real assign is required, not a no-op. The account's
// own id (always unique, always lowercase hex at its first 8 characters) is
// a cheap valid, collision-free handle.
func testAssignHandle(ctx context.Context, q sqlkit.Querier, id, _ string, _ bool) {
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
