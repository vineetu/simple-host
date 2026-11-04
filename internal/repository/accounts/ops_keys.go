package accounts

import (
	"context"
	"time"
)

const expiredKeyKeep = 30 * 24 * time.Hour

// PurgeExpiredAPIKeys deletes keys that stopped working more than 30 days ago,
// by fixed expiry or by going unused (keyIdle, KEY_IDLE_EXPIRY_DAYS), and
// reports how many.
func PurgeExpiredAPIKeys(ctx context.Context, q Querier, keyIdle time.Duration) (int64, error) {
	res, err := q.ExecContext(ctx, `
		DELETE FROM api_keys
		 WHERE (expires_at IS NOT NULL AND expires_at < now() - make_interval(secs => $1))
		    OR ($2::float8 > 0 AND GREATEST(idle_from, COALESCE(last_used_at, idle_from)) + make_interval(secs => $2::float8) < now() - make_interval(secs => $1))`,
		expiredKeyKeep.Seconds(), keyIdle.Seconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
