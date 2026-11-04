package accounts

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

type AuthToken = model.AuthToken

func CreateAuthToken(ctx context.Context, q Querier, email, code, linkToken string, expiresAt time.Time, purpose string, siteID, nonceHash sql.NullString) error {
	const query = `
		INSERT INTO auth_tokens (email, code, link_token, expires_at, purpose, site_id, nonce_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := q.ExecContext(ctx, query, email, code, linkToken, expiresAt, purpose, siteID, nonceHash)
	return err
}

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

func ClaimAuthToken(ctx context.Context, q Querier, id string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE auth_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func IncrementAuthTokenAttempts(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, `UPDATE auth_tokens SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}
