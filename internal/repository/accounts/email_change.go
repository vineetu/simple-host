package accounts

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

type EmailChange = model.EmailChange
type EmailChangeUndo = model.EmailChangeUndo

var ErrUndoAddressTaken = model.ErrUndoAddressTaken

func PutEmailChange(ctx context.Context, q Querier, userID, newEmail, codeHash, oldCodeHash string, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO email_changes (user_id, new_email, code_hash, old_code_hash, attempts, expires_at)
		VALUES ($1, $2, $3, $4, 0, $5)
		ON CONFLICT (user_id) DO UPDATE
		   SET new_email = EXCLUDED.new_email, code_hash = EXCLUDED.code_hash, old_code_hash = EXCLUDED.old_code_hash,
		       attempts = 0, expires_at = EXCLUDED.expires_at, created_at = now()`,
		userID, newEmail, codeHash, oldCodeHash, expiresAt)
	return err
}

func GetEmailChangeForUpdate(ctx context.Context, tx *sql.Tx, userID string) (EmailChange, error) {
	var c EmailChange
	err := tx.QueryRowContext(ctx, `
		SELECT new_email, code_hash, COALESCE(old_code_hash, ''), attempts, expires_at FROM email_changes
		 WHERE user_id = $1 AND expires_at > now()
		   FOR UPDATE`, userID).Scan(&c.NewEmail, &c.CodeHash, &c.OldCodeHash, &c.Attempts, &c.ExpiresAt)
	return c, err
}

func CountEmailChangeAttempt(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE email_changes SET attempts = attempts + 1 WHERE user_id = $1`, userID)
	return err
}

func ApplyEmailChange(ctx context.Context, tx *sql.Tx, userID, oldEmail, newEmail, keepKeyHash, undoHash string, undoTTL time.Duration) error {
	steps := []struct {
		q    string
		args []any
	}{
		{`UPDATE auth_tokens SET used_at = now() WHERE email = $1 AND used_at IS NULL`, []any{oldEmail}},
		{`UPDATE users SET username = $2 WHERE id = $1`, []any{userID, newEmail}},
		{`DELETE FROM email_changes WHERE user_id = $1`, []any{userID}},
		{`DELETE FROM api_keys WHERE user_id = $1 AND key_hash <> $2`, []any{userID, keepKeyHash}},
		{`DELETE FROM visitor_sessions WHERE user_id = $1`, []any{userID}},
		{`INSERT INTO email_change_undos (token_hash, user_id, old_email, new_email, expires_at)
		  VALUES ($1, $2, $3, $4, now() + ($5 * interval '1 second'))`, []any{undoHash, userID, oldEmail, newEmail, int64(undoTTL.Seconds())}},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return CancelIdleLinks(ctx, tx, userID)
}

func GetEmailChangeUndo(ctx context.Context, q Querier, tokenHash string) (EmailChangeUndo, error) {
	var u EmailChangeUndo
	err := q.QueryRowContext(ctx, `SELECT user_id::text, old_email, new_email, created_at FROM email_change_undos
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`, tokenHash).Scan(&u.UserID, &u.OldEmail, &u.NewEmail, &u.ChangedAt)
	return u, err
}

func EmailInUseByOther(ctx context.Context, q Querier, userID, address string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE username = $1 AND id <> $2)`, address, userID).Scan(&taken)
	return taken, err
}

func CancelIdleLinks(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE sites
		   SET idle_token_hash = NULL,
		       idle_warned_at = CASE WHEN deleted_at IS NULL THEN NULL ELSE idle_warned_at END
		 WHERE user_id = $1 AND (idle_token_hash IS NOT NULL OR (deleted_at IS NULL AND idle_warned_at IS NOT NULL))`, userID)
	return err
}

func UndoEmailChangeInTx(ctx context.Context, tx *sql.Tx, tokenHash string) (EmailChangeUndo, error) {
	var err error
	var u EmailChangeUndo
	err = tx.QueryRowContext(ctx, `SELECT user_id::text, old_email, new_email, created_at FROM email_change_undos
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now() FOR UPDATE`, tokenHash).Scan(&u.UserID, &u.OldEmail, &u.NewEmail, &u.ChangedAt)
	if err != nil {
		return u, err
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1 FOR UPDATE`, u.UserID).Scan(&current); err != nil {
		return u, err
	}
	var taken bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE lower(username) = lower($1) AND id <> $2)`, u.OldEmail, u.UserID).Scan(&taken); err != nil {
		return u, err
	}
	if taken {
		return u, ErrUndoAddressTaken
	}
	steps := []struct {
		q    string
		args []any
	}{
		{`UPDATE auth_tokens SET used_at = now() WHERE email = $1 AND used_at IS NULL`, []any{current}},
		{`UPDATE users SET username = $2 WHERE id = $1`, []any{u.UserID, u.OldEmail}},
		{`DELETE FROM api_keys WHERE user_id = $1`, []any{u.UserID}},
		{`DELETE FROM visitor_sessions WHERE user_id = $1`, []any{u.UserID}},
		{`DELETE FROM oauth_identities WHERE user_id = $1 AND created_at >= $2`, []any{u.UserID, u.ChangedAt}},
		{`DELETE FROM oauth_grants WHERE user_id = $1 AND created_at >= $2`, []any{u.UserID, u.ChangedAt}},
		{`DELETE FROM email_changes WHERE user_id = $1`, []any{u.UserID}},
		{`UPDATE email_change_undos SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`, []any{u.UserID}},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return u, err
		}
	}
	if err := CancelIdleLinks(ctx, tx, u.UserID); err != nil {
		return u, err
	}
	return u, nil
}

func LockSignInEmail(ctx context.Context, tx *sql.Tx, userID string) (string, error) {
	var email string
	err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&email)
	return email, err
}
