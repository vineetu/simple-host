package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

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

func CreateSite(ctx context.Context, q Querier, userID, name, siteURL string) (Site, error) {
	const query = `
		INSERT INTO sites (user_id, name, site_url)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, name, active_version, site_url, created_at, updated_at
	`

	var site Site

	err := q.QueryRowContext(ctx, query, userID, name, siteURL).Scan(
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

func GetSiteState(ctx context.Context, db *sql.DB, name string) (json.RawMessage, error) {
	// COALESCE so a SQL NULL becomes the JSON literal `null` — keeps the
	// scan target (json.RawMessage / []byte) happy and the response a
	// well-formed JSON document either way.
	const query = `SELECT COALESCE(state, 'null'::jsonb) FROM sites WHERE name = $1`

	var state []byte
	if err := db.QueryRowContext(ctx, query, name).Scan(&state); err != nil {
		return nil, err
	}
	if len(state) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(state), nil
}

func UpdateSiteState(ctx context.Context, db *sql.DB, name string, state json.RawMessage) error {
	const query = `
		UPDATE sites
		SET state = $2::jsonb, updated_at = now()
		WHERE name = $1
	`

	result, err := db.ExecContext(ctx, query, name, string(state))
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

func UpdateSiteActiveVersion(ctx context.Context, db Querier, siteID string, version int) error {
	const query = `
		UPDATE sites
		SET active_version = $2, updated_at = now()
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, siteID, version)
	return err
}

func CreateVersion(ctx context.Context, db Querier, siteID string, versionNumber int, diskPath string) (Version, error) {
	const query = `
		INSERT INTO versions (site_id, version_number, disk_path, status)
		VALUES ($1, $2, $3, 'uploading')
		RETURNING id, site_id, version_number, disk_path, status, created_at
	`

	var version Version
	err := db.QueryRowContext(ctx, query, siteID, versionNumber, diskPath).Scan(
		&version.ID,
		&version.SiteID,
		&version.VersionNumber,
		&version.DiskPath,
		&version.Status,
		&version.CreatedAt,
	)
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
