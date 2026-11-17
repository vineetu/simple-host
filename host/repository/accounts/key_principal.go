package accounts

import (
	"context"
	"time"
)

func KeyPrincipal(ctx context.Context, db Querier, apiKey string) (User, *time.Time, time.Time, *time.Time, time.Time, error) {
	const query = `
		SELECT u.id, u.username, u.is_admin, u.created_at, u.handle, u.display_name, k.key_hash,
		       u.suspended_at IS NOT NULL, COALESCE(u.suspended_reason, ''),
		       k.scope, k.expires_at, k.idle_from, k.last_used_at, now()
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1
	`

	var (
		user      User
		expiresAt *time.Time
		idleFrom  time.Time
		lastUsed  *time.Time
		now       time.Time
	)
	err := db.QueryRowContext(ctx, query, HashAPIKey(apiKey)).Scan(
		&user.ID,
		&user.Username,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.Handle,
		&user.DisplayName,
		&user.KeyHash,
		&user.Suspended,
		&user.SuspendedReason,
		&user.KeyScope,
		&expiresAt,
		&idleFrom,
		&lastUsed,
		&now,
	)
	return user, expiresAt, idleFrom, lastUsed, now, err
}

func StoredKeyScope(ctx context.Context, q Querier, apiKey string) (string, error) {
	var scope string
	err := q.QueryRowContext(ctx, `SELECT scope FROM api_keys WHERE key_hash = $1`, HashAPIKey(apiKey)).Scan(&scope)
	return scope, err
}
