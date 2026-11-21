// Package sqlkit provides the shared SQL query interface for repositories.
package sqlkit

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

// Querier abstracts *sql.DB and *sql.Tx for transaction-safe queries.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func IsUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
