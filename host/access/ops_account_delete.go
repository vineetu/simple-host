package access

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// Erasure is the product's part of account deletion. Transactional methods run
// on the caller's transaction; Sweep runs only after a successful commit.
type Erasure interface {
	// Holds answers the product's holding-account fact, independently of account flags.
	Holds(context.Context, sqlkit.Querier, string) (bool, error)
	// Block follows the account lock and admin, suspension, taken-down-site and
	// hostname checks, before confirmation. A nonempty code refuses with 409.
	Block(ctx context.Context, tx *sql.Tx, userID string, operator bool) (code, msg string, err error)
	// Before runs before deletes and returns the identifiers After needs.
	Before(context.Context, *sql.Tx, string) ([]string, error)
	// Rows runs after deleting sites and before deleting the account.
	Rows(context.Context, *sql.Tx, string) error
	// After runs after the account deletion has cascaded.
	After(context.Context, *sql.Tx, []string) ([]string, error)
	Sweep(context.Context, *sql.DB, []string, func(context.Context, string, string) error)
	// HoldingText accepts delete, delete_operator, reissue, suspend, address or email.
	HoldingText(action string) string
}

// HoldingAccount asks the product; the platform alone has no holding accounts.
func HoldingAccount(ctx context.Context, q sqlkit.Querier, userID string) (bool, error) {
	if groupHooks.Erasure == nil {
		return false, nil
	}
	return groupHooks.Erasure.Holds(ctx, q, userID)
}

func HoldingText(action string) string {
	if groupHooks.Erasure == nil {
		return "this account holds sites for a group and cannot be changed here"
	}
	return groupHooks.Erasure.HoldingText(action)
}

func (s *Service) SweepErasedGroups(ctx context.Context, ids []string, trash func(context.Context, string, string) error) {
	if groupHooks.Erasure != nil {
		groupHooks.Erasure.Sweep(ctx, s.database, ids, trash)
	}
}
