package accounts

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// EventAccountFlag is users.event_account for the holding account.
func EventAccountFlag(ctx context.Context, q sqlkit.Querier, userID string) (bool, error) {
	var v bool
	err := q.QueryRowContext(ctx, `SELECT event_account FROM users WHERE id = $1`, userID).Scan(&v)
	return v, err
}
