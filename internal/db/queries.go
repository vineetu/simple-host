package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrStateVersionConflict is returned by UpdateSiteStateCAS when the caller's
// expected version no longer matches the stored one (a concurrent write landed
// in between). Callers should re-read and retry.
var ErrStateVersionConflict = errors.New("state version conflict")

func CreateUser(ctx context.Context, db *sql.DB, username, apiKey string, isAdmin bool) (User, error) {
	const query = `
		INSERT INTO users (username, api_key, is_admin)
		VALUES ($1, $2, $3)
		RETURNING id, username, api_key, is_admin, created_at
	`

	var user User
	err := db.QueryRowContext(ctx, query, username, apiKey, isAdmin).Scan(
		&user.ID,
		&user.Username,
		&user.APIKey,
		&user.IsAdmin,
		&user.CreatedAt,
	)
	return user, err
}

func GetUserByAPIKey(ctx context.Context, db *sql.DB, apiKey string) (User, error) {
	const query = `
		SELECT id, username, api_key, is_admin, created_at
		FROM users
		WHERE api_key = $1
	`

	var user User
	err := db.QueryRowContext(ctx, query, apiKey).Scan(
		&user.ID,
		&user.Username,
		&user.APIKey,
		&user.IsAdmin,
		&user.CreatedAt,
	)
	return user, err
}

func GetUserByUsername(ctx context.Context, db *sql.DB, username string) (User, error) {
	const query = `
		SELECT id, username, api_key, is_admin, created_at
		FROM users
		WHERE username = $1
	`

	var user User
	err := db.QueryRowContext(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&user.APIKey,
		&user.IsAdmin,
		&user.CreatedAt,
	)
	return user, err
}

// Querier abstracts *sql.DB and *sql.Tx for transaction-safe queries.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// CreateSite inserts a new site. expiresAt is nil for permanent sites, or a
// timestamp for ephemeral "preview" sites that the background sweep deletes.
func CreateSite(ctx context.Context, q Querier, userID, name, siteURL string, expiresAt *time.Time) (Site, error) {
	const query = `
		INSERT INTO sites (user_id, name, site_url, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, active_version, site_url, created_at, updated_at
	`

	var site Site

	err := q.QueryRowContext(ctx, query, userID, name, siteURL, expiresAt).Scan(
		&site.ID,
		&site.UserID,
		&site.Name,
		&site.ActiveVersion,
		&site.SiteURL,
		&site.CreatedAt,
		&site.UpdatedAt,
	)
	if err != nil {
		return Site{}, err
	}

	return site, nil
}

// ExpiredSite identifies a site past its expires_at (for the cleanup sweep).
type ExpiredSite struct {
	ID   string
	Name string
}

// ListExpiredSites returns sites whose expires_at has passed. Sites with a NULL
// expires_at (the default) are permanent and never returned.
func ListExpiredSites(ctx context.Context, db *sql.DB) ([]ExpiredSite, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name FROM sites WHERE expires_at IS NOT NULL AND expires_at < now() ORDER BY expires_at LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExpiredSite
	for rows.Next() {
		var e ExpiredSite
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BackfillPreviewExpiry stamps expires_at = created_at + ttlHours on every
// still-permanent (NULL) site owned by the given preview account. Existing sites
// older than the TTL become immediately expired and are removed by the next
// sweep. Returns how many rows were updated.
func BackfillPreviewExpiry(ctx context.Context, db *sql.DB, username string, ttlHours int) (int64, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE sites SET expires_at = created_at + make_interval(hours => $1)
		 WHERE expires_at IS NULL
		   AND user_id IN (SELECT id FROM users WHERE lower(username) = lower($2))`,
		ttlHours, username)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetAllowedOrigins returns the extra origins (beyond the site's own subdomain)
// permitted to call this site's state/collections API — enables "backend
// anywhere" (e.g. a page hosted on GitHub Pages using this site as its backend).
func GetAllowedOrigins(ctx context.Context, db *sql.DB, siteName string) ([]string, error) {
	var raw sql.NullString
	err := db.QueryRowContext(ctx, `SELECT allowed_origins FROM sites WHERE name = $1`, siteName).Scan(&raw)
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
	var raw sql.NullString
	err := db.QueryRowContext(ctx, `SELECT allowed_origins FROM sites WHERE id = $1`, siteID).Scan(&raw)
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

// SetAllowedOrigins replaces the allowed-origins list for a site (comma-joined).
func SetAllowedOrigins(ctx context.Context, db *sql.DB, siteID, origins string) error {
	_, err := db.ExecContext(ctx, `UPDATE sites SET allowed_origins = $1 WHERE id = $2`, origins, siteID)
	return err
}

func GetSite(ctx context.Context, db *sql.DB, name string) (Site, error) {
	const query = `
		SELECT id, user_id, name, active_version, site_url, created_at, updated_at
		FROM sites
		WHERE name = $1
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

// GetSiteIDByName returns the site's UUID for a globally unique name.
// Returns sql.ErrNoRows if no site with that name exists.
func GetSiteIDByName(ctx context.Context, db *sql.DB, name string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = $1`, name).Scan(&id)
	return id, err
}

func GetSiteByUser(ctx context.Context, db *sql.DB, userID, name string) (Site, error) {
	const query = `
		SELECT id, user_id, name, active_version, site_url, created_at, updated_at
		FROM sites
		WHERE user_id = $1 AND name = $2
	`

	var site Site
	err := db.QueryRowContext(ctx, query, userID, name).Scan(
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

func DeleteSite(ctx context.Context, db Querier, siteID string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM versions WHERE site_id = $1`, siteID); err != nil {
		return err
	}

	result, err := db.ExecContext(ctx, `DELETE FROM sites WHERE id = $1`, siteID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ListAllSites(ctx context.Context, db *sql.DB) ([]Site, error) {
	const query = `
		SELECT s.id, s.user_id, s.name, s.active_version, s.site_url, s.created_at, s.updated_at, u.username
		FROM sites s
		INNER JOIN users u ON u.id = s.user_id
		ORDER BY s.created_at ASC, s.name ASC
	`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var site Site
		if err := rows.Scan(
			&site.ID,
			&site.UserID,
			&site.Name,
			&site.ActiveVersion,
			&site.SiteURL,
			&site.CreatedAt,
			&site.UpdatedAt,
			&site.OwnerUsername,
		); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sites, nil
}

func ListSitesByUser(ctx context.Context, db *sql.DB, userID string) ([]Site, error) {
	const query = `
		SELECT id, user_id, name, active_version, site_url, created_at, updated_at
		FROM sites
		WHERE user_id = $1
		ORDER BY created_at ASC, name ASC
	`

	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	return scanSiteRows(rows)
}

func scanSiteRows(rows *sql.Rows) ([]Site, error) {
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var site Site

		if err := rows.Scan(
			&site.ID,
			&site.UserID,
			&site.Name,
			&site.ActiveVersion,
			&site.SiteURL,
			&site.CreatedAt,
			&site.UpdatedAt,
		); err != nil {
			return nil, err
		}

		sites = append(sites, site)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sites, nil
}

// CreateAuthToken inserts a new verification token row.
func CreateAuthToken(ctx context.Context, db *sql.DB, email, code, linkToken string, expiresAt time.Time) error {
	const query = `
		INSERT INTO auth_tokens (email, code, link_token, expires_at)
		VALUES ($1, $2, $3, $4)
	`
	_, err := db.ExecContext(ctx, query, email, code, linkToken, expiresAt)
	return err
}

// AuthToken is the in-memory representation of a row in auth_tokens.
type AuthToken struct {
	ID         string
	Email      string
	Code       string
	LinkToken  string
	ExpiresAt  time.Time
	UsedAt     sql.NullTime
	Attempts   int
}

// GetAuthTokenByLink returns the active (unused, not expired) token for a
// magic-link sign-in. Returns sql.ErrNoRows if none.
func GetAuthTokenByLink(ctx context.Context, db *sql.DB, linkToken string) (AuthToken, error) {
	const query = `
		SELECT id, email, code, link_token, expires_at, used_at, attempts
		FROM auth_tokens
		WHERE link_token = $1 AND used_at IS NULL AND expires_at > now()
	`
	var t AuthToken
	err := db.QueryRowContext(ctx, query, linkToken).Scan(
		&t.ID, &t.Email, &t.Code, &t.LinkToken, &t.ExpiresAt, &t.UsedAt, &t.Attempts,
	)
	return t, err
}

// GetLatestAuthTokenForEmail returns the most recent active token for an email
// address (used for code entry). Returns sql.ErrNoRows if none.
func GetLatestAuthTokenForEmail(ctx context.Context, db *sql.DB, email string) (AuthToken, error) {
	const query = `
		SELECT id, email, code, link_token, expires_at, used_at, attempts
		FROM auth_tokens
		WHERE email = $1 AND used_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1
	`
	var t AuthToken
	err := db.QueryRowContext(ctx, query, email).Scan(
		&t.ID, &t.Email, &t.Code, &t.LinkToken, &t.ExpiresAt, &t.UsedAt, &t.Attempts,
	)
	return t, err
}

// MarkAuthTokenUsed marks the token as consumed.
func MarkAuthTokenUsed(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, `UPDATE auth_tokens SET used_at = now() WHERE id = $1`, id)
	return err
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
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE name = $1`

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
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE id = $1`

	var state []byte
	var version int
	if err := db.QueryRowContext(ctx, query, siteID).Scan(&state, &version); err != nil {
		return nil, 0, err
	}
	if len(state) == 0 {
		return json.RawMessage("null"), version, nil
	}
	return json.RawMessage(state), version, nil
}

// UpdateSiteState overwrites state unconditionally (last-write-wins) and bumps
// the version. Returns the new version. This is the default behavior when the
// caller does not opt into optimistic concurrency.
func UpdateSiteState(ctx context.Context, db *sql.DB, name string, state json.RawMessage) (int, error) {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, state_version = state_version + 1, updated_at = now()
		WHERE name = $1
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
		WHERE name = $1 AND state_version = $3
		RETURNING state_version
	`

	var version int
	err := db.QueryRowContext(ctx, query, name, string(state), expected).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		// No row updated: either the site is gone or the version moved. Probe
		// to return the precise error.
		var exists bool
		probe := db.QueryRowContext(ctx, `SELECT true FROM sites WHERE name = $1`, name).Scan(&exists)
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

// SetViewPasswordHash stores (or with hash="" clears) the bcrypt view-password
// hash that gates public viewing of a site.
func SetViewPasswordHash(ctx context.Context, db *sql.DB, siteID, hash string) error {
	var v any
	if hash != "" {
		v = hash
	}
	_, err := db.ExecContext(ctx, `UPDATE sites SET view_password_hash = $2, updated_at = now() WHERE id = $1`, siteID, v)
	return err
}

// LoadViewLocks returns site name -> bcrypt hash for every view-locked site, for
// the in-memory cache the nginx auth_request handler consults (so an unlocked
// page view costs a memory lookup, not a DB hit).
func LoadViewLocks(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, view_password_hash FROM sites WHERE view_password_hash IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			return nil, err
		}
		out[name] = hash
	}
	return out, rows.Err()
}

// GetSiteStateVersion reads ONLY the state version — cheap, for conditional GET
// (If-None-Match -> 304) so pollers don't fetch/serialize the whole document.
func GetSiteStateVersion(ctx context.Context, db *sql.DB, name string) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, `SELECT state_version FROM sites WHERE name = $1`, name).Scan(&version)
	return version, err
}

// GetSiteStateVersionByID is the site_id-keyed variant of GetSiteStateVersion.
func GetSiteStateVersionByID(ctx context.Context, db *sql.DB, siteID string) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, `SELECT state_version FROM sites WHERE id = $1`, siteID).Scan(&version)
	return version, err
}

// GetSiteStateForUpdate reads state + version and locks the row FOR UPDATE, so a
// read-modify-write (atomic PATCH) applies without lost updates. Concurrent
// PATCHes serialize on the lock instead of burning CPU on optimistic retries.
// Use inside a transaction.
func GetSiteStateForUpdate(ctx context.Context, q Querier, name string) (json.RawMessage, int, error) {
	const query = `SELECT COALESCE(state, 'null'::jsonb), state_version FROM sites WHERE name = $1 FOR UPDATE`
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
		WHERE name = $1
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

func UpdateSiteActiveVersion(ctx context.Context, db Querier, siteID string, version int) error {
	const query = `
		UPDATE sites
		SET active_version = $2, updated_at = now()
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, siteID, version)
	return err
}

func CreateVersion(ctx context.Context, db Querier, siteID string, versionNumber int, diskPath, archiveSHA256 string) (Version, error) {
	const query = `
		INSERT INTO versions (site_id, version_number, disk_path, status, archive_sha256)
		VALUES ($1, $2, $3, 'uploading', $4)
		RETURNING id, site_id, version_number, disk_path, status, created_at
	`

	var version Version
	err := db.QueryRowContext(ctx, query, siteID, versionNumber, diskPath, archiveSHA256).Scan(
		&version.ID,
		&version.SiteID,
		&version.VersionNumber,
		&version.DiskPath,
		&version.Status,
		&version.CreatedAt,
	)
	version.ArchiveSHA256 = archiveSHA256
	return version, err
}

func ListVersionsBySite(ctx context.Context, db *sql.DB, siteID string) ([]Version, error) {
	const query = `
		SELECT id, site_id, version_number, disk_path, status, created_at
		FROM versions
		WHERE site_id = $1
		ORDER BY version_number DESC
	`

	rows, err := db.QueryContext(ctx, query, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.SiteID, &v.VersionNumber, &v.DiskPath, &v.Status, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func ActivateVersion(ctx context.Context, db Querier, versionID string) error {
	const query = `
		UPDATE versions
		SET status = 'active'
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, versionID)
	return err
}

// LockSiteForUpdate takes a row-level lock on the sites row so concurrent
// uploads to the same site serialize their version allocation. Must be called
// inside a transaction; the lock releases on commit/rollback.
func LockSiteForUpdate(ctx context.Context, db Querier, siteID string) error {
	const query = `SELECT id FROM sites WHERE id = $1 FOR UPDATE`
	var id string
	return db.QueryRowContext(ctx, query, siteID).Scan(&id)
}

func GetMaxVersionNumber(ctx context.Context, db Querier, siteID string) (int, error) {
	const query = `
		SELECT COALESCE(MAX(version_number), 0)
		FROM versions
		WHERE site_id = $1
	`

	var maxVersion int
	err := db.QueryRowContext(ctx, query, siteID).Scan(&maxVersion)
	return maxVersion, err
}

func GetActiveSiteVersion(ctx context.Context, db *sql.DB, siteID string) (Version, error) {
	const query = `
		SELECT v.id, v.site_id, v.version_number, v.disk_path, v.status, v.created_at
		FROM versions v
		INNER JOIN sites s ON s.id = v.site_id
		WHERE v.site_id = $1
		  AND v.version_number = s.active_version
		  AND v.status = 'active'
	`

	var version Version
	err := db.QueryRowContext(ctx, query, siteID).Scan(
		&version.ID,
		&version.SiteID,
		&version.VersionNumber,
		&version.DiskPath,
		&version.Status,
		&version.CreatedAt,
	)
	return version, err
}
