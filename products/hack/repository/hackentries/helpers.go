package hackentries

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// judgeModeSQL is whether team t is in judge's queue under the event's
// judge assignment mode (mode, the SQL of a text parameter), for event $1.
func judgeModeSQL(mode, judge string) string {
	return `(` + mode + `='open'
	OR (` + mode + ` IN ('automatic','manual') AND EXISTS (SELECT 1 FROM event_assignments a WHERE a.event_id=t.event_id AND a.judge_id=` + judge + ` AND a.team_id=t.id))
	OR (` + mode + `='panel' AND EXISTS (SELECT 1 FROM event_track_panels p WHERE p.event_id=t.event_id AND p.track_id=t.track_id AND p.judge_id=` + judge + `)))`
}

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
