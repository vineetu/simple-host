package legacy

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// CountVisitorAIWrite counts an attempted write, returning ErrNoRows at the cap.
func CountVisitorAIWrite(ctx context.Context, q sqlkit.Querier, userID, siteID string, cap int) error {
	var count int64
	return q.QueryRowContext(ctx, `INSERT INTO visitor_ai_daily(user_id,site_id,day,writes) VALUES($1,$2,(now() AT TIME ZONE 'UTC')::date,1) ON CONFLICT(user_id,site_id,day) DO UPDATE SET writes=visitor_ai_daily.writes+1 WHERE visitor_ai_daily.writes<$3 RETURNING writes`, userID, siteID, cap).Scan(&count)
}

// RecordVisitorAIWrite appends a non-restorable visitor history marker.
func RecordVisitorAIWrite(ctx context.Context, q sqlkit.Querier, siteID, resource, op, userID, app, historyKind string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO data_history(site_id,kind,name,op,actor_id,actor_kind,actor_app) VALUES($1,$6,$2,$3,$4,'visitor',$5)`, siteID, resource, op, userID, app, historyKind)
	return err
}
