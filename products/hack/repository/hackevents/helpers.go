package hackevents

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

func queryContext(ctx context.Context, q sqlkit.Querier, query string, args ...any) (*sql.Rows, error) {
	switch t := q.(type) {
	case *sql.DB:
		return t.QueryContext(ctx, query, args...)
	case *sql.Tx:
		return t.QueryContext(ctx, query, args...)
	default:
		return nil, fmt.Errorf("db: querier does not support QueryContext")
	}
}
func scanMember(row *sql.Row) (EventMember, error) {
	var m EventMember
	err := row.Scan(&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt)
	return m, err
}
