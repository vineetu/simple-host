package notifications

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"time"
)

func VerifiedAt(ctx context.Context, q sqlkit.Querier, owner, url string) (time.Time, error) {
	var at time.Time
	err := q.QueryRowContext(ctx, `SELECT max(verified_at) FROM site_notifications WHERE owner_id=$1 AND url=$2 AND source='mcp'`, owner, url).Scan(&at)
	return at, err
}
