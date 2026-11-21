package events

import (
	"context"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// EnsureAccountName uses the existing name, or validates and fills a first name inside the caller's membership transaction.
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
		return &Error{Code: invalid.Code, Message: invalid.Message}
	}
	return accounts.FillEmptyDisplayName(ctx, q, userID, name)
}
