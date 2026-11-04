package db

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
)

// Names given to keys by where they came from. A key minted from the Keys
// panel (POST /v1/me/keys) carries the name its owner typed instead.
const (
	KeyNameDashboard = model.KeyNameDashboard
	KeyNameAgent     = model.KeyNameAgent
	KeyNameEvent     = model.KeyNameEvent
	KeyNameReviewer  = model.KeyNameReviewer
	KeyNameRotated   = model.KeyNameRotated
)

// Key scopes (api_keys.scope). A full key does everything the account can
// through the API; a deploy key only what a CI job that publishes needs
// (internal/auth/scope.go holds the route table).
const (
	KeyScopeFull   = model.KeyScopeFull
	KeyScopeDeploy = model.KeyScopeDeploy
)

// Why a stored key no longer works (APIKey.Expired).
const (
	KeyExpiredFixed = model.KeyExpiredFixed // past the expiry chosen when it was minted
	KeyExpiredIdle  = model.KeyExpiredIdle  // unused for KEY_IDLE_EXPIRY_DAYS
)

// ErrKeyExpired and ErrKeyExpiredIdle are returned by GetUserByAPIKey for a
// stored key that has stopped working. Both wrap sql.ErrNoRows, so every
// caller that treats an unknown key as "not signed in" keeps doing so; the key
// middleware tells them apart to say why.
var (
	ErrKeyExpired     = model.ErrKeyExpired
	ErrKeyExpiredIdle = model.ErrKeyExpiredIdle
)

// APIKey is one row of api_keys as its owner sees it. The key itself is never
// stored; Last4 is only for recognising it. Name and Last4 are empty for keys
// issued before keys had names.
type APIKey model.APIKey

// IdleExpiresAt is when the key stops working if it is not used before then,
// or nil when idle expiry is off (keyIdle <= 0). keyIdle is the caller's
// instance limit (KEY_IDLE_EXPIRY_DAYS).
func (k APIKey) IdleExpiresAt(keyIdle time.Duration) *time.Time {
	return idleExpiresAt(k.IdleFrom, k.LastUsedAt, keyIdle)
}

func idleExpiresAt(idleFrom time.Time, lastUsed *time.Time, keyIdle time.Duration) *time.Time {
	return accounts.IdleExpiresAt(idleFrom, lastUsed, keyIdle)
}

// Expired says whether the key has stopped working at now, and why:
// KeyExpiredFixed, KeyExpiredIdle, or "" while it works. keyIdle is the
// caller's instance limit (KEY_IDLE_EXPIRY_DAYS).
func (k APIKey) Expired(now time.Time, keyIdle time.Duration) string {
	return keyExpired(now, k.ExpiresAt, k.IdleFrom, k.LastUsedAt, keyIdle)
}

func keyExpired(now time.Time, expiresAt *time.Time, idleFrom time.Time, lastUsed *time.Time, keyIdle time.Duration) string {
	return accounts.KeyExpired(now, expiresAt, idleFrom, lastUsed, keyIdle)
}

func keyLast4(key string) string {
	if len(key) < 4 {
		return ""
	}
	return key[len(key)-4:]
}

func touchAPIKey(ctx context.Context, q Querier, keyHash string) {
	accounts.TouchAPIKey(ctx, q, keyHash)
}

// ListAPIKeys returns an account's keys, newest first.
func ListAPIKeys(ctx context.Context, q *sql.DB, userID string) ([]APIKey, error) {
	rows, err := accounts.ListAPIKeys(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	out := make([]APIKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, APIKey(row))
	}
	return out, nil
}

// ErrKeyLimit: the account already holds maxAccountKeys (MAX_KEYS_PER_ACCOUNT)
// keys. Sign-in keys are not refused by it.
var ErrKeyLimit = model.ErrKeyLimit

// CreateAPIKey stores a new key under name, with scope (KeyScopeFull or
// KeyScopeDeploy) and an optional fixed expiry, and returns its row, but only while
// the key the request came with (callerKeyHash) is still one of the account's:
// a key revoked or rotated away mid-request cannot mint a successor
// (sql.ErrNoRows). The users row is locked first, the same lock rotate and the
// admin reissue take, so a concurrent "revoke everything" cannot miss the new
// key. ErrKeyLimit when the account already holds maxAccountKeys keys; keyIdle
// is KEY_IDLE_EXPIRY_DAYS, used to decide which of those are still live.
func CreateAPIKey(ctx context.Context, db *sql.DB, userID, callerKeyHash, apiKey, name, scope string, expiresAt *time.Time, maxAccountKeys int, keyIdle time.Duration) (APIKey, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, err
	}
	defer tx.Rollback()
	if err := accounts.LockKeyIssuer(ctx, tx, userID, callerKeyHash); err != nil {
		return APIKey{}, err
	}
	n, err := accounts.CountLiveKeys(ctx, tx, userID, keyIdle)
	if err != nil {
		return APIKey{}, err
	}
	if n >= maxAccountKeys {
		return APIKey{}, ErrKeyLimit
	}
	if scope == "" {
		scope = KeyScopeFull
	}
	k := APIKey{Hash: HashAPIKey(apiKey), Name: name, Last4: keyLast4(apiKey), Scope: scope, ExpiresAt: expiresAt}
	row, err := accounts.InsertNamedKey(ctx, tx, userID, model.APIKey(k))
	if err != nil {
		return APIKey{}, err
	}
	k = APIKey(row)
	return k, tx.Commit()
}

// AddSignInKey is AddAPIKey for a sign-in: it locks the users row (the lock
// rotate and the admin reissue take) and refuses a suspended account
// (ErrAccountSuspended), so a suspension or a reissue that lands mid-sign-in
// cannot be slipped past.
func AddSignInKey(ctx context.Context, db *sql.DB, userID, apiKey, name string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var suspended bool
	if err := tx.QueryRowContext(ctx,
		`SELECT suspended_at IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&suspended); err != nil {
		return err
	}
	if suspended {
		return ErrAccountSuspended
	}
	if err := AddAPIKey(ctx, tx, userID, apiKey, name); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAPIKeyByHash revokes the key a request authenticated with (sign-out).
func DeleteAPIKeyByHash(ctx context.Context, q Querier, userID, keyHash string) (bool, error) {
	return accounts.DeleteAPIKeyByHash(ctx, q, userID, keyHash)
}

// ReservedKeyName reports whether name is one the service gives its own keys
// (case-insensitive, after trimming). A typed key name may not be one, so a
// name never reads like something the service made.
func ReservedKeyName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, r := range []string{KeyNameDashboard, KeyNameAgent, KeyNameEvent, KeyNameReviewer, KeyNameRotated} {
		if n == r {
			return true
		}
	}
	return false
}

// ReplaceAPIKeys removes every key an account holds and stores newKey as its
// only one. Unlike RotateAPIKey it needs no current key: the admin uses it to
// reissue a participant's lost key. Every site sign-in of the account ends
// too (EndVisitorSessions). Run it inside the caller's transaction.
func ReplaceAPIKeys(ctx context.Context, q Querier, userID, newKey, name string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if err := EndVisitorSessions(ctx, q, userID); err != nil {
		return err
	}
	return AddAPIKey(ctx, q, userID, newKey, name)
}

// liveKeySQL is the SQL form of keyExpired == "": the key has neither passed
// its fixed expiry nor gone unused for KEY_IDLE_EXPIRY_DAYS. The idle expiry
// in seconds is the query's $2 (0 = never).
const liveKeySQL = `(expires_at IS NULL OR expires_at > now())
	AND ($2::float8 <= 0 OR GREATEST(idle_from, COALESCE(last_used_at, idle_from)) + make_interval(secs => $2::float8) > now())`

// expiredKeyKeep is how long a key that stopped working stays in the Keys
// panel (shown as expired) before PurgeExpiredAPIKeys removes it.
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
