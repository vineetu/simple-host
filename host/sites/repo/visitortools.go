package repo

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// VisitorSitesQuerier adds row iteration to the shared query interface.
type VisitorSitesQuerier interface {
	sqlkit.Querier
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
