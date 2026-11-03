package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
)

func queryContext(ctx context.Context, q Querier, query string, args ...any) (*sql.Rows, error) {
	switch t := q.(type) {
	case *sql.DB:
		return t.QueryContext(ctx, query, args...)
	case *sql.Tx:
		return t.QueryContext(ctx, query, args...)
	default:
		return nil, fmt.Errorf("db: querier does not support QueryContext")
	}
}

func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func nullInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func nullStr(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// TeamKeepableSQL over event_teams t and events e: a team that may be
// removed when it empties — its deadline has not passed and nothing is
// pinned. A team past its deadline keeps its submission for judging.
// TeamKeepableSQL is used by internal/handler.
const TeamKeepableSQL = hackevents.TeamKeepableSQL

// Event is used by internal/handler.
type Event = model.Event
