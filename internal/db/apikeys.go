package db

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// Names given to keys by where they came from. A key minted from the Keys
// panel (POST /v1/me/keys) carries the name its owner typed instead.
const (
	KeyNameDashboard = "dashboard sign-in"
	KeyNameAgent     = "agent sign-in"
	KeyNameEvent     = "event account"
	KeyNameReviewer  = "reviewer sign-in"
	KeyNameRotated   = "replacement key"
)

// APIKey is one row of api_keys as its owner sees it. The key itself is never
// stored; Last4 is only for recognising it. Name and Last4 are empty for keys
// issued before keys had names.
type APIKey struct {
	ID         string
	Hash       string
	Name       string
	Last4      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

func keyLast4(key string) string {
	if len(key) < 4 {
		return ""
	}
	return key[len(key)-4:]
}

// touchAPIKeyInterval: last_used_at is display data, not a control, so it is
// written at most this often rather than on every request.
const touchAPIKeyInterval = 5 * time.Minute

func touchAPIKey(ctx context.Context, q Querier, keyHash string) {
	if keyHash == "" {
		return
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE api_keys SET last_used_at = now()
		 WHERE key_hash = $1 AND (last_used_at IS NULL OR last_used_at < now() - make_interval(secs => $2))`,
		keyHash, touchAPIKeyInterval.Seconds()); err != nil {
		log.Printf("api key: touch last_used_at: %v", err)
	}
}

// ListAPIKeys returns an account's keys, newest first.
func ListAPIKeys(ctx context.Context, q *sql.DB, userID string) ([]APIKey, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, key_hash, COALESCE(name, ''), COALESCE(last4, ''), created_at, last_used_at
		  FROM api_keys WHERE user_id = $1
		 ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Hash, &k.Name, &k.Last4, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CreateAPIKey stores a new key under name and returns its row.
func CreateAPIKey(ctx context.Context, q Querier, userID, apiKey, name string) (APIKey, error) {
	k := APIKey{Hash: HashAPIKey(apiKey), Name: name, Last4: keyLast4(apiKey)}
	err := q.QueryRowContext(ctx, `
		INSERT INTO api_keys (key_hash, user_id, name, last4) VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`, k.Hash, userID, name, k.Last4).Scan(&k.ID, &k.CreatedAt)
	return k, err
}

// DeleteAPIKey revokes one of userID's keys by id. It reports false when the
// account has no such key (someone else's id reads the same as a missing one).
func DeleteAPIKey(ctx context.Context, q Querier, userID, keyID string) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE id::text = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteAPIKeyByHash revokes the key a request authenticated with (sign-out).
func DeleteAPIKeyByHash(ctx context.Context, q Querier, userID, keyHash string) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE key_hash = $1 AND user_id = $2`, keyHash, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ReplaceAPIKeys removes every key an account holds and stores newKey as its
// only one. Unlike RotateAPIKey it needs no current key: the admin uses it to
// reissue a participant's lost key. Run it inside the caller's transaction.
func ReplaceAPIKeys(ctx context.Context, q Querier, userID, newKey, name string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return AddAPIKey(ctx, q, userID, newKey, name)
}
