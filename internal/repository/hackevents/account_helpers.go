package hackevents

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// NewUUID asks Postgres for a fresh UUID.
func NewUUID(ctx context.Context, q sqlkit.Querier) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT gen_random_uuid()`).Scan(&id)
	return id, err
}

// LockUserRow takes FOR UPDATE on a users row so create limits serialise.
func LockUserRow(ctx context.Context, q *sql.Tx, userID string) error {
	var one int
	return q.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&one)
}

// AccountKeyCount is how many API keys the account holds (holding accounts must be 0).
func AccountKeyCount(ctx context.Context, q sqlkit.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// EventAccountFlag is users.event_account for the holding account.
func EventAccountFlag(ctx context.Context, q sqlkit.Querier, userID string) (bool, error) {
	var v bool
	err := q.QueryRowContext(ctx, `SELECT event_account FROM users WHERE id = $1`, userID).Scan(&v)
	return v, err
}
