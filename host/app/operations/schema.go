package operations

import (
	"context"
	"database/sql"

	repo "github.com/vsriram/simple-host/host/repository/operations"
)

// VerifyColumns refuses to start when a product's tables lack the columns it
// reads (table -> columns).
func VerifyColumns(ctx context.Context, db *sql.DB, required map[string][]string) error {
	return repo.VerifyColumns(ctx, db, required)
}
