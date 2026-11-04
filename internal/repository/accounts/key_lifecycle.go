package accounts

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

func ListAPIKeys(ctx context.Context, q *sql.DB, userID string) ([]model.APIKey, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, key_hash, COALESCE(name, ''), COALESCE(last4, ''), scope, created_at, last_used_at, expires_at, idle_from
		  FROM api_keys WHERE user_id = $1
		 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.APIKey{}
	for rows.Next() {
		var k model.APIKey
		if err := rows.Scan(&k.ID, &k.Hash, &k.Name, &k.Last4, &k.Scope, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.IdleFrom); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func DeleteAPIKey(ctx context.Context, q Querier, userID, keyID string) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE id::text = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func DeleteAPIKeyByHash(ctx context.Context, q Querier, userID, keyHash string) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE key_hash = $1 AND user_id = $2`, keyHash, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// LockKeyIssuer takes the account lock before the authenticating key lock, as rotation does.
func LockKeyIssuer(ctx context.Context, tx *sql.Tx, userID, keyHash string) error {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&one); err != nil {
		return err
	}
	return tx.QueryRowContext(ctx, `SELECT 1 FROM api_keys WHERE key_hash = $1 AND user_id = $2 FOR UPDATE`, keyHash, userID).Scan(&one)
}
func CountLiveKeys(ctx context.Context, tx *sql.Tx, userID string, keyIdle time.Duration) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM api_keys WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > now())
 AND ($2::float8 <= 0 OR GREATEST(idle_from, COALESCE(last_used_at, idle_from)) + make_interval(secs => $2::float8) > now())`, userID, keyIdle.Seconds()).Scan(&n)
	return n, err
}
func InsertNamedKey(ctx context.Context, tx *sql.Tx, userID string, k model.APIKey) (model.APIKey, error) {
	err := tx.QueryRowContext(ctx, `
 INSERT INTO api_keys (key_hash, user_id, name, last4, scope, expires_at) VALUES ($1, $2, $3, $4, $5, $6)
 RETURNING id, created_at, idle_from`, k.Hash, userID, k.Name, k.Last4, k.Scope, k.ExpiresAt).Scan(&k.ID, &k.CreatedAt, &k.IdleFrom)
	return k, err
}
func DeleteSSOPassParent(ctx context.Context, q Querier, passHash, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id::text=$2 AND key_hash IN (SELECT parent_key_hash FROM sso_passes WHERE id_hash=$1 AND user_id::text=$2)`, passHash, userID)
	return err
}
