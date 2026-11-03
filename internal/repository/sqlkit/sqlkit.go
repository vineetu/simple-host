// Package sqlkit provides the shared SQL query interface for repositories.
package sqlkit

import (
	"context"
	"database/sql"
)

// Querier abstracts *sql.DB and *sql.Tx for transaction-safe queries.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
