package operations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// TestVerifySchemaCatchesMissingPasswordLogin is the regression this file
// exists to prevent: account_passwords.login (0004-password-login.sql) is
// read unconditionally by GET /v1/admin/users (site.go), so a database that
// has not run that migration must fail fast at startup instead of 500ing on
// that endpoint. Dropping the column from an otherwise up-to-date schema
// must make VerifySchema refuse to start, and restoring it must make
// VerifySchema pass again.
func TestVerifySchemaCatchesMissingPasswordLogin(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	if err := VerifySchema(ctx, db); err != nil {
		t.Fatalf("VerifySchema on an up-to-date schema: %v", err)
	}

	if _, err := db.ExecContext(ctx, `ALTER TABLE account_passwords DROP COLUMN login`); err != nil {
		t.Fatalf("drop login column: %v", err)
	}
	t.Cleanup(func() {
		db.ExecContext(ctx, `ALTER TABLE account_passwords ADD COLUMN IF NOT EXISTS login text`)
	})

	err = VerifySchema(ctx, db)
	if err == nil {
		t.Fatal("VerifySchema passed with account_passwords.login missing; want a refusal to start")
	}
	if !strings.Contains(err.Error(), "account_passwords.login") {
		t.Fatalf("VerifySchema error does not name the missing column: %v", err)
	}
}
