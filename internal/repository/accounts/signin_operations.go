package accounts

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
)

func AddAPIKey(ctx context.Context, q Querier, userID, apiKey, name string) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO api_keys (key_hash, user_id, name, last4) VALUES ($1, $2, $3, $4)`,
		HashAPIKey(apiKey), userID, name, lastKeyDigits(apiKey))
	return err
}

func SignInAlertsOn(ctx context.Context, q Querier, userID string) (bool, error) {
	var on bool
	err := q.QueryRowContext(ctx, `SELECT signin_alerts FROM users WHERE id = $1`, userID).Scan(&on)
	return on, err
}

func lastKeyDigits(key string) string {
	if len(key) < 4 {
		return ""
	}
	return key[len(key)-4:]
}
func GetSSOHop(ctx context.Context, q Querier, state string, consume bool) (model.SSOHop, error) {
	query := `SELECT state,site_id::text,host,return_to,nonce_hash,consent_hash,consent_pass_hash FROM sso_hops WHERE state=$1 AND expires_at>now()`
	if consume {
		query = `DELETE FROM sso_hops WHERE state=$1 AND expires_at>now() RETURNING state,site_id::text,host,return_to,nonce_hash,consent_hash,consent_pass_hash`
	}
	var h model.SSOHop
	err := q.QueryRowContext(ctx, query, state).Scan(&h.State, &h.SiteID, &h.Host, &h.ReturnTo, &h.NonceHash, &h.ConsentHash, &h.ConsentPassHash)
	return h, err
}

func LockSignInAccount(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var suspended bool
	err := tx.QueryRowContext(ctx, `SELECT suspended_at IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&suspended)
	return suspended, err
}
func DeleteAllKeys(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id = $1`, userID)
	return err
}
