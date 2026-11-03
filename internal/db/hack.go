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
const TeamKeepableSQL = hackevents.TeamKeepableSQL

// Alias (cleanup 4.2): defined in internal/model.
type Event = model.Event

// Alias (cleanup 4.2): defined in internal/model.
type EventMember = model.EventMember

// Alias (cleanup 4.2): defined in internal/model.
type EventTeam = model.EventTeam

// Alias (cleanup 4.2): defined in internal/model.
type AdminEvent = model.AdminEvent

// Alias (cleanup 4.2): defined in internal/model.
type EventPerson = model.EventPerson
