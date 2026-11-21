package hackevents

import (
	"context"
	"strings"

	hostmodel "github.com/vsriram/simple-host/host/model"

	"github.com/vsriram/simple-host/host/sqlkit"
)

func NewUUID(ctx context.Context, q sqlkit.Querier) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT gen_random_uuid()`).Scan(&id)
	return id, err
}

// HasAddress is whether the account's sign-in address can receive mail.
func HasAddress(u hostmodel.User) bool { return strings.Contains(u.Username, "@") }
