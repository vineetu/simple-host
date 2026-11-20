package ops

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/ops/repo"
)

// VerifyColumns refuses to start when a product's tables lack the columns it
// reads (table -> columns).
func VerifyColumns(ctx context.Context, db *sql.DB, required map[string][]string) error {
	return repo.VerifyColumns(ctx, db, required)
}
