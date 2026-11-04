package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ErrStateVersionConflict is returned by UpdateSiteStateCAS when the caller's
// expected version no longer matches the stored one (a concurrent write landed
// in between). Callers should re-read and retry.
var ErrStateVersionConflict = model.ErrStateVersionConflict

// AddAPIKey stores one more key for an account under name (see the Key*
// names). Every sign-in hands out a new key this way, because a stored hash
// cannot be handed back; the keys an account already holds keep working until
// the person revokes them or rotates.
func AddAPIKey(ctx context.Context, q Querier, userID, apiKey, name string) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO api_keys (key_hash, user_id, name, last4) VALUES ($1, $2, $3, $4)`,
		HashAPIKey(apiKey), userID, name, keyLast4(apiKey))
	return err
}

// GetUserByAPIKey resolves a presented key: a stored key by its hash, or an
// in-process internal credential (see IssueInternalKey).
func GetUserByAPIKey(ctx context.Context, db *sql.DB, apiKey string, keyIdle time.Duration) (User, error) {
	if strings.HasPrefix(apiKey, internalKeyPrefix) {
		c, ok := lookupInternalCred(apiKey)
		if !ok || c.Visitor != nil {
			return User{}, sql.ErrNoRows
		}
		u, err := GetUserByID(ctx, db, c.UserID)
		if err == nil && u.Suspended {
			return u, ErrAccountSuspended
		}
		if err == nil && c.TeamID != "" {
			// A connector connection bound to a team site (hack_sites.go).
			id, terr := ResolveTeamIdentity(ctx, db, c.UserID, c.TeamID)
			if terr != nil {
				return User{}, terr
			}
			return actAsTeam(ctx, db, u, id)
		}
		return u, err
	}
	user, expiresAt, idleFrom, lastUsed, now, err := accounts.KeyPrincipal(ctx, db, apiKey)
	if err == nil && user.KeyScope == KeyScopeDeploy {
		// A deploy key never carries admin powers, even when an admin
		// minted it: a leaked CI secret must not list every site on the
		// instance or skip the site quota.
		user.IsAdmin = false
	}
	if err == nil {
		user.KeyExpiresAt = expiresAt
		// An expired key is kept (the owner sees it in the Keys panel as
		// expired and can revoke it) but never authenticates.
		switch keyExpired(now, expiresAt, idleFrom, lastUsed, keyIdle) {
		case KeyExpiredFixed:
			return user, ErrKeyExpired
		case KeyExpiredIdle:
			return user, ErrKeyExpiredIdle
		}
	}
	if err == nil && user.Suspended {
		return user, ErrAccountSuspended
	}
	if err == nil {
		touchAPIKey(ctx, db, user.KeyHash)
	}
	if err == nil && user.KeyScope == KeyScopeTeam {
		// A member's team key: the event's holding account on the team's
		// site, only while they are on that team (hack_sites.go).
		teamID, memberID, terr := hackteams.TeamKeyBinding(ctx, db, user.KeyHash)
		if terr == nil && memberID != user.ID {
			terr = hackteams.ErrTeamKeyInactive
		}
		if terr != nil {
			return User{}, terr
		}
		id, terr := ResolveTeamIdentity(ctx, db, memberID, teamID)
		if terr != nil {
			return User{}, terr
		}
		return actAsTeam(ctx, db, user, id)
	}
	return user, err
}

// RotateAPIKey replaces every key the account holds with newKey and ends
// every site sign-in of the account (EndVisitorSessions), but only if
// the key the request was made with (currentKeyHash) is still one of them, so
// two concurrent rotations cannot both succeed.
func RotateAPIKey(ctx context.Context, db *sql.DB, userID, currentKeyHash, newKey, name string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	// Users row first, like CreateAPIKey and the admin reissue, so a key
	// minted concurrently is either deleted here or minted after.
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&one); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM api_keys WHERE key_hash = $1 AND user_id = $2 FOR UPDATE`,
		currentKeyHash, userID).Scan(&one); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if err := EndVisitorSessions(ctx, tx, userID); err != nil {
		return err
	}
	if err := AddAPIKey(ctx, tx, userID, newKey, name); err != nil {
		return err
	}
	return tx.Commit()
}

// GetUserByHandle looks up a user by their URL-safe handle.
func GetUserByHandle(ctx context.Context, db *sql.DB, handle string) (User, error) {
	return accounts.GetUserByHandle(ctx, db, handle)
}

// GetHandleBySiteName returns the URL-safe handle of the OLDEST owner of a site
// named `name` — i.e. the account the legacy <name>.<siteDomain> host used to
// serve. Used to 301 a deprecated name-subdomain to its canonical path URL.
// Returns sql.ErrNoRows if no such site; the handle is "" if the owner has none.
func GetHandleBySiteName(ctx context.Context, db *sql.DB, name string) (string, error) {
	const query = `
		SELECT COALESCE(u.handle, '')
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.name = $1 AND s.deleted_at IS NULL
		ORDER BY s.created_at ASC
		LIMIT 1
	`
	var handle string
	err := db.QueryRowContext(ctx, query, name).Scan(&handle)
	return handle, err
}

// Alias (cleanup 4.1): defined in internal/repository/sqlkit.
type Querier = sqlkit.Querier

type ExpiredSite = model.ExpiredSite

// BackfillPreviewExpiry stamps expires_at = created_at + ttlHours on every
// still-permanent (NULL) site owned by the given preview account. Existing sites
// older than the TTL become immediately expired and are removed by the next
// sweep. Returns how many rows were updated.
func BackfillPreviewExpiry(ctx context.Context, db *sql.DB, username string, ttlHours int) (int64, error) {
	return sites.BackfillPreviewExpiry(ctx, db, username, ttlHours)
}

// GetAllowedOrigins returns the extra origins (beyond the site's own subdomain)
// permitted to call this site's state/collections API — enables "backend
// anywhere" (e.g. a page hosted on external hosting using this site as its backend).
func GetAllowedOrigins(ctx context.Context, db *sql.DB, siteName string) ([]string, error) {
	var raw sql.NullString
	err := db.QueryRowContext(ctx, `SELECT allowed_origins FROM sites WHERE name = $1 AND deleted_at IS NULL`, siteName).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil
	}
	var out []string
	for _, o := range strings.Split(raw.String, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out, nil
}

// GetAllowedOriginsByID is the site_id-keyed variant of GetAllowedOrigins.
func GetAllowedOriginsByID(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	return sites.GetAllowedOriginsByID(ctx, db, siteID)
}

// GetLegacyHostnamesByID returns the frozen legacy hostnames bound to a site_id.
func GetLegacyHostnamesByID(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT hostname FROM legacy_hostnames WHERE site_id = $1`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// SetAllowedOrigins replaces the allowed-origins list for a site (comma-joined).
func SetAllowedOrigins(ctx context.Context, db *sql.DB, siteID, origins string) error {
	return sites.SetAllowedOrigins(ctx, db, siteID, origins)
}

func GetSite(ctx context.Context, db *sql.DB, name string) (Site, error) {
	const query = `
		SELECT id, user_id, name, active_version, site_url, created_at, updated_at
		FROM sites
		WHERE name = $1 AND deleted_at IS NULL
	`

	var site Site

	err := db.QueryRowContext(ctx, query, name).Scan(
		&site.ID,
		&site.UserID,
		&site.Name,
		&site.ActiveVersion,
		&site.SiteURL,
		&site.CreatedAt,
		&site.UpdatedAt,
	)
	return site, err
}

// GetSiteIDByName resolves a bare site name for the legacy edge. When multiple
// users own the same name, it returns the ORIGINAL (oldest) owner so the
// legacy subdomain / bare-name route never flips to a later same-named site.
// Returns sql.ErrNoRows if no site with that name exists.
func GetSiteIDByName(ctx context.Context, db *sql.DB, name string) (string, error) {
	return sites.GetSiteIDByName(ctx, db, name)
}

func ListAllSites(ctx context.Context, db *sql.DB) ([]Site, error) {
	return sites.ListAllSites(ctx, db)
}

// ListAllUsers returns every registered user (admin view), oldest first. API keys
// are deliberately NOT selected.
func ListAllUsers(ctx context.Context, db *sql.DB) ([]User, error) {
	return accounts.ListAllUsers(ctx, db)
}

// ListSiteOwners excludes visitor-only identities from signup statistics.
func ListSiteOwners(ctx context.Context, db *sql.DB) ([]User, error) { return listUsers(ctx, db, true) }
func listUsers(ctx context.Context, db *sql.DB, withSites bool) ([]User, error) {
	return accounts.ListUsers(ctx, db, withSites)
}

// SetSiteOffline takes a site offline (on) or brings it back (off). Nothing
// else about the site changes. sql.ErrNoRows for an unknown or deleted site.
func SetSiteOffline(ctx context.Context, q Querier, siteID string, on bool) error {
	return sites.SetSiteOffline(ctx, q, siteID, on)
}

// SiteOffline reports whether a site is offline by its owner's choice.
func SiteOffline(ctx context.Context, q Querier, siteID string) (bool, error) {
	return sites.SiteOffline(ctx, q, siteID)
}

// CreateAuthToken inserts a new verification token row.
// nonceHash binds the link token to the browser that asked for it: the token
// is redeemable only together with the nonce that hashes to it (see
// verifyEmailCode). Null means the link token can never be redeemed.
func CreateAuthToken(ctx context.Context, q Querier, email, code, linkToken string, expiresAt time.Time, purpose string, siteID, nonceHash sql.NullString) error {
	const query = `
		INSERT INTO auth_tokens (email, code, link_token, expires_at, purpose, site_id, nonce_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := q.ExecContext(ctx, query, email, code, linkToken, expiresAt, purpose, siteID, nonceHash)
	return err
}

// AuthToken is the in-memory representation of a row in auth_tokens.
type AuthToken struct {
	Purpose   string
	SiteID    sql.NullString
	ID        string
	Email     string
	Code      string
	LinkToken string
	ExpiresAt time.Time
	UsedAt    sql.NullTime
	Attempts  int
	NonceHash sql.NullString
}

// GetAuthTokenByLink returns the active (unused, not expired) token for a
// magic-link sign-in. Returns sql.ErrNoRows if none.
func GetAuthTokenByLink(ctx context.Context, db *sql.DB, linkToken string) (AuthToken, error) {
	const query = `
		SELECT id, email, code, link_token, expires_at, used_at, attempts, purpose, site_id, nonce_hash
		FROM auth_tokens
		WHERE link_token = $1 AND purpose = 'dashboard' AND used_at IS NULL AND expires_at > now()
	`
	var t AuthToken
	err := db.QueryRowContext(ctx, query, linkToken).Scan(
		&t.ID, &t.Email, &t.Code, &t.LinkToken, &t.ExpiresAt, &t.UsedAt, &t.Attempts, &t.Purpose, &t.SiteID, &t.NonceHash,
	)
	return t, err
}

// GetLatestAuthTokenForEmail returns the most recent active token for an email
// address (used for code entry). Returns sql.ErrNoRows if none.
func GetLatestAuthTokenForEmail(ctx context.Context, db *sql.DB, email, purpose string, siteID sql.NullString) (AuthToken, error) {
	const query = `
		SELECT id, email, code, link_token, expires_at, used_at, attempts, purpose, site_id
		FROM auth_tokens
		WHERE email = $1 AND purpose = $2 AND site_id IS NOT DISTINCT FROM $3 AND used_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1
	`
	var t AuthToken
	err := db.QueryRowContext(ctx, query, email, purpose, siteID).Scan(
		&t.ID, &t.Email, &t.Code, &t.LinkToken, &t.ExpiresAt, &t.UsedAt, &t.Attempts, &t.Purpose, &t.SiteID,
	)
	return t, err
}

// ClaimAuthToken consumes the token, reporting false when it was already
// used (redeemed by a concurrent verify, or retired by a sign-in email change
// that took its address away).
func ClaimAuthToken(ctx context.Context, q Querier, id string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE auth_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// IncrementAuthTokenAttempts bumps the failed-attempt counter.
func IncrementAuthTokenAttempts(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, `UPDATE auth_tokens SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}

// GetSiteState returns the site's state document and its current version. The
// version is a monotonic counter bumped on every write, used for optimistic
// concurrency (see UpdateSiteStateCAS).
func GetSiteState(ctx context.Context, db *sql.DB, name string) (json.RawMessage, int, error) {
	// COALESCE so a SQL NULL becomes the JSON literal `null` — keeps the
	// scan target (json.RawMessage / []byte) happy and the response a
	// well-formed JSON document either way.
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE name = $1 AND deleted_at IS NULL`

	var state []byte
	var version int
	if err := db.QueryRowContext(ctx, query, name).Scan(&state, &version); err != nil {
		return nil, 0, err
	}
	if len(state) == 0 {
		return json.RawMessage("null"), version, nil
	}
	return json.RawMessage(state), version, nil
}

// GetSiteStateByID is the site_id-keyed variant of GetSiteState.
func GetSiteStateByID(ctx context.Context, db *sql.DB, siteID string) (json.RawMessage, int, error) {
	return sites.GetSiteStateByID(ctx, db, siteID)
}

// UpdateSiteState overwrites state unconditionally (last-write-wins) and bumps
// the version. Returns the new version. This is the default behavior when the
// caller does not opt into optimistic concurrency.
func UpdateSiteState(ctx context.Context, db *sql.DB, name string, state json.RawMessage) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE name = $1 AND deleted_at IS NULL
		RETURNING state_version
	`

	var version int
	err := db.QueryRowContext(ctx, query, name, string(state)).Scan(&version)
	if err != nil {
		return 0, err // includes sql.ErrNoRows when the site doesn't exist
	}
	return version, nil
}

// UpdateSiteStateByID is the site_id-keyed variant of UpdateSiteState.
func UpdateSiteStateByID(ctx context.Context, db *sql.DB, siteID string, state json.RawMessage) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE id = $1
		RETURNING state_version
	`

	var version int
	err := db.QueryRowContext(ctx, query, siteID, string(state)).Scan(&version)
	if err != nil {
		return 0, err // includes sql.ErrNoRows when the site doesn't exist
	}
	return version, nil
}

// UpdateSiteStateCAS overwrites state only if the stored version equals
// `expected` (compare-and-swap / optimistic concurrency). Returns the new
// version on success, sql.ErrNoRows if the site is missing, or
// ErrStateVersionConflict if a concurrent write moved the version.
func UpdateSiteStateCAS(ctx context.Context, db *sql.DB, name string, state json.RawMessage, expected int) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE name = $1 AND deleted_at IS NULL AND state_version = $3
		RETURNING state_version
	`

	var version int
	err := db.QueryRowContext(ctx, query, name, string(state), expected).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		// No row updated: either the site is gone or the version moved. Probe
		// to return the precise error.
		var exists bool
		probe := db.QueryRowContext(ctx, `SELECT true FROM sites WHERE name = $1 AND deleted_at IS NULL`, name).Scan(&exists)
		if errors.Is(probe, sql.ErrNoRows) {
			return 0, sql.ErrNoRows
		}
		if probe != nil {
			return 0, probe
		}
		return 0, ErrStateVersionConflict
	}
	if err != nil {
		return 0, err
	}
	return version, nil
}

// UpdateSiteStateCASByID is the site_id-keyed variant of UpdateSiteStateCAS.
func UpdateSiteStateCASByID(ctx context.Context, db *sql.DB, siteID string, state json.RawMessage, expected int) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE id = $1 AND state_version = $3
		RETURNING state_version
	`

	var version int
	err := db.QueryRowContext(ctx, query, siteID, string(state), expected).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		// No row updated: either the site is gone or the version moved. Probe
		// to return the precise error.
		var exists bool
		probe := db.QueryRowContext(ctx, `SELECT true FROM sites WHERE id = $1`, siteID).Scan(&exists)
		if errors.Is(probe, sql.ErrNoRows) {
			return 0, sql.ErrNoRows
		}
		if probe != nil {
			return 0, probe
		}
		return 0, ErrStateVersionConflict
	}
	if err != nil {
		return 0, err
	}
	return version, nil
}

// GetSiteStateVersion reads ONLY the state version — cheap, for conditional GET
// (If-None-Match -> 304) so pollers don't fetch/serialize the whole document.
func GetSiteStateVersion(ctx context.Context, db *sql.DB, name string) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, `SELECT state_version FROM sites WHERE name = $1 AND deleted_at IS NULL`, name).Scan(&version)
	return version, err
}

// GetSiteStateVersionByID is the site_id-keyed variant of GetSiteStateVersion.
func GetSiteStateVersionByID(ctx context.Context, db *sql.DB, siteID string) (int, error) {
	return sites.GetSiteStateVersionByID(ctx, db, siteID)
}

// GetSiteStateForUpdate reads state + version and locks the row FOR UPDATE, so a
// read-modify-write (atomic PATCH) applies without lost updates. Concurrent
// PATCHes serialize on the lock instead of burning CPU on optimistic retries.
// Use inside a transaction.
func GetSiteStateForUpdate(ctx context.Context, q Querier, name string) (json.RawMessage, int, error) {
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE name = $1 AND deleted_at IS NULL FOR UPDATE`
	var state []byte
	var version int
	if err := q.QueryRowContext(ctx, query, name).Scan(&state, &version); err != nil {
		return nil, 0, err
	}
	if len(state) == 0 {
		return json.RawMessage("null"), version, nil
	}
	return json.RawMessage(state), version, nil
}

// GetSiteStateForUpdateByID is the site_id-keyed variant of GetSiteStateForUpdate.
func GetSiteStateForUpdateByID(ctx context.Context, q Querier, siteID string) (json.RawMessage, int, error) {
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE id = $1 FOR UPDATE`
	var state []byte
	var version int
	if err := q.QueryRowContext(ctx, query, siteID).Scan(&state, &version); err != nil {
		return nil, 0, err
	}
	if len(state) == 0 {
		return json.RawMessage("null"), version, nil
	}
	return json.RawMessage(state), version, nil
}

// SetSiteState overwrites state and bumps the version using the given Querier
// (typically a tx). Returns the new version.
func SetSiteState(ctx context.Context, q Querier, name string, state json.RawMessage) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE name = $1 AND deleted_at IS NULL
		RETURNING state_version
	`
	var version int
	if err := q.QueryRowContext(ctx, query, name, string(state)).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// SetSiteStateByID is the site_id-keyed variant of SetSiteState.
func SetSiteStateByID(ctx context.Context, q Querier, siteID string, state json.RawMessage) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE id = $1
		RETURNING state_version
	`
	var version int
	if err := q.QueryRowContext(ctx, query, siteID, string(state)).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}
