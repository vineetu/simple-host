package accounts

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
)

type SignInIdentity = model.SignInIdentity

func ListSignInIdentities(ctx context.Context, database *sql.DB, userID string) ([]SignInIdentity, error) {
	rows, err := database.QueryContext(ctx, `SELECT id::text, provider, COALESCE(email, ''), created_at FROM oauth_identities WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SignInIdentity
	for rows.Next() {
		var s SignInIdentity
		if err := rows.Scan(&s.ID, &s.Provider, &s.Email, &s.LinkedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func UnlinkSignInIdentityInTx(ctx context.Context, tx *sql.Tx, userID, id string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM oauth_identities WHERE user_id = $1 AND id::text = $2`, userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		err = sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if err := EndVisitorSessions(ctx, tx, userID); err != nil {
		return err
	}
	return nil
}
