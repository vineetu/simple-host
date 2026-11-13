package events

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"strings"
)

// EnsureAccountName uses the existing name, or validates and fills a first name
// inside the caller's membership transaction.
func EnsureAccountName(ctx context.Context, q sqlkit.Querier, userID, supplied string) error {
	name, err := accounts.LockDisplayName(ctx, q, userID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) != "" {
		return nil
	}
	name, invalid := hackevents.Line(supplied, "display_name", 1, 100)
	if invalid != nil {
		return &Error{invalid.Code, invalid.Message}
	}
	return accounts.FillEmptyDisplayName(ctx, q, userID, name)
}
