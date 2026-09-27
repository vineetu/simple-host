package db

import (
	"context"
	"database/sql"
	"time"
)

// Sign-in email changes and sign-in alerts (w2-account-signin-email.sql).

// EmailChange is an account's pending sign-in email change.
type EmailChange struct {
	NewEmail  string
	CodeHash  string
	Attempts  int
	ExpiresAt time.Time
}

// PutEmailChange records (or replaces) the account's pending email change.
func PutEmailChange(ctx context.Context, q Querier, userID, newEmail, codeHash string, expiresAt time.Time) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO email_changes (user_id, new_email, code_hash, attempts, expires_at)
		VALUES ($1, $2, $3, 0, $4)
		ON CONFLICT (user_id) DO UPDATE
		   SET new_email = EXCLUDED.new_email, code_hash = EXCLUDED.code_hash,
		       attempts = 0, expires_at = EXCLUDED.expires_at, created_at = now()`,
		userID, newEmail, codeHash, expiresAt)
	return err
}

// GetEmailChangeForUpdate returns the account's pending, unexpired email
// change, locked for the rest of tx. sql.ErrNoRows when there is none.
func GetEmailChangeForUpdate(ctx context.Context, tx *sql.Tx, userID string) (EmailChange, error) {
	var c EmailChange
	err := tx.QueryRowContext(ctx, `
		SELECT new_email, code_hash, attempts, expires_at FROM email_changes
		 WHERE user_id = $1 AND expires_at > now()
		   FOR UPDATE`, userID).Scan(&c.NewEmail, &c.CodeHash, &c.Attempts, &c.ExpiresAt)
	return c, err
}

// CountEmailChangeAttempt adds one wrong guess to the pending change.
func CountEmailChangeAttempt(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE email_changes SET attempts = attempts + 1 WHERE user_id = $1`, userID)
	return err
}

// ApplyEmailChange moves the account to newEmail inside tx, drops the pending
// change, and retires every unused emailed code for the old address (one
// redeemed later would otherwise create a new, empty account under it). The
// caller has checked that no other account holds newEmail; a race still ends
// in a unique violation on users.username.
func ApplyEmailChange(ctx context.Context, tx *sql.Tx, userID, oldEmail, newEmail string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET username = $2 WHERE id = $1`, userID, newEmail); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_changes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE auth_tokens SET used_at = now() WHERE email = $1 AND used_at IS NULL`, oldEmail)
	return err
}

// EmailInUseByOther reports whether an account other than userID signs in
// with address.
func EmailInUseByOther(ctx context.Context, q Querier, userID, address string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE username = $1 AND id <> $2)`, address, userID).Scan(&taken)
	return taken, err
}

// SignInAlertsOn reports whether the account gets an email after each sign-in.
func SignInAlertsOn(ctx context.Context, q Querier, userID string) (bool, error) {
	var on bool
	err := q.QueryRowContext(ctx, `SELECT signin_alerts FROM users WHERE id = $1`, userID).Scan(&on)
	return on, err
}

// SetSignInAlerts turns sign-in alert emails on or off for the account.
func SetSignInAlerts(ctx context.Context, q Querier, userID string, on bool) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET signin_alerts = $2 WHERE id = $1`, userID, on)
	return err
}

// SignInAlertTarget is what deciding and addressing a sign-in alert needs.
type SignInAlertTarget struct {
	Email  string
	Handle string
	On     bool
	// Event is an account an organiser made for an event (it holds or held
	// an "event account" key); those get no alerts.
	Event bool
}

// GetSignInAlertTarget loads the account's alert settings.
func GetSignInAlertTarget(ctx context.Context, q Querier, userID string) (SignInAlertTarget, error) {
	var t SignInAlertTarget
	err := q.QueryRowContext(ctx, `
		SELECT u.username, COALESCE(u.handle, ''), u.signin_alerts,
		       EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.name = $2)
		  FROM users u WHERE u.id = $1`, userID, KeyNameEvent).Scan(&t.Email, &t.Handle, &t.On, &t.Event)
	return t, err
}

// ClaimSignInAlert records that the alert for (account, summary, today UTC)
// is being sent, and reports false when one already was. It also prunes the
// account's rows from earlier days.
func ClaimSignInAlert(ctx context.Context, q Querier, userID, summary string) (bool, error) {
	if _, err := q.ExecContext(ctx, `DELETE FROM signin_alerts_sent WHERE user_id = $1 AND day < (now() AT TIME ZONE 'UTC')::date - 1`, userID); err != nil {
		return false, err
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO signin_alerts_sent (user_id, summary, day)
		VALUES ($1, $2, (now() AT TIME ZONE 'UTC')::date)
		ON CONFLICT DO NOTHING`, userID, summary)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
