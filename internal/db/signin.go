package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Sign-in email changes and sign-in alerts (w2-account-signin-email.sql).

// EmailChange is an account's pending sign-in email change: one code went to
// the new address and one to the current one; both are needed.
type EmailChange struct {
	NewEmail    string
	CodeHash    string // the code sent to the new address
	OldCodeHash string // the code sent to the current address
	Attempts    int
	ExpiresAt   time.Time
}

// PutEmailChange records (or replaces) the account's pending email change.
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

// GetEmailChangeForUpdate returns the account's pending, unexpired email
// change, locked for the rest of tx. sql.ErrNoRows when there is none.
func GetEmailChangeForUpdate(ctx context.Context, tx *sql.Tx, userID string) (EmailChange, error) {
	var c EmailChange
	err := tx.QueryRowContext(ctx, `
		SELECT new_email, code_hash, COALESCE(old_code_hash, ''), attempts, expires_at FROM email_changes
		 WHERE user_id = $1 AND expires_at > now()
		   FOR UPDATE`, userID).Scan(&c.NewEmail, &c.CodeHash, &c.OldCodeHash, &c.Attempts, &c.ExpiresAt)
	return c, err
}

// CountEmailChangeAttempt adds one wrong guess to the pending change.
func CountEmailChangeAttempt(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE email_changes SET attempts = attempts + 1 WHERE user_id = $1`, userID)
	return err
}

// EmailChangeUndoTTL is how long the undo link in the old address's notice
// works (EMAIL_CHANGE_UNDO_DAYS).
func EmailChangeUndoTTL() time.Duration { return lim().EmailChangeUndoTTL }

// ApplyEmailChange moves the account to newEmail inside tx and:
//   - drops the pending change;
//   - retires every unused emailed code and link for the old address (one
//     redeemed later would otherwise create a new, empty account under it;
//     verifyEmailCode claims its token in a transaction, so the two
//     serialize on the token row);
//   - revokes every other API key of the account except keepKeyHash (the key
//     that made the change);
//   - ends the account's emailed idle-cleanup links (they went to the old
//     address);
//   - records the undo link (undoHash) the old address is sent, valid for
//     EmailChangeUndoTTL.
//
// The caller has checked that no other account holds newEmail; a race still
// ends in a unique violation on users.username.
func ApplyEmailChange(ctx context.Context, tx *sql.Tx, userID, oldEmail, newEmail, keepKeyHash, undoHash string) error {
	steps := []struct {
		q    string
		args []any
	}{
		{`UPDATE auth_tokens SET used_at = now() WHERE email = $1 AND used_at IS NULL`, []any{oldEmail}},
		{`UPDATE users SET username = $2 WHERE id = $1`, []any{userID, newEmail}},
		{`DELETE FROM email_changes WHERE user_id = $1`, []any{userID}},
		{`DELETE FROM api_keys WHERE user_id = $1 AND key_hash <> $2`, []any{userID, keepKeyHash}},
		{`INSERT INTO email_change_undos (token_hash, user_id, old_email, new_email, expires_at)
		  VALUES ($1, $2, $3, $4, now() + ($5 * interval '1 second'))`, []any{undoHash, userID, oldEmail, newEmail, int64(EmailChangeUndoTTL().Seconds())}},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return CancelIdleLinks(ctx, tx, userID)
}

// EmailChangeUndo is an unused, unexpired undo link's change.
type EmailChangeUndo struct {
	UserID    string
	OldEmail  string
	NewEmail  string
	ChangedAt time.Time
}

// GetEmailChangeUndo looks up an undo link by its token's hash (not locked).
// sql.ErrNoRows when it is unknown, used or expired.
func GetEmailChangeUndo(ctx context.Context, q Querier, tokenHash string) (EmailChangeUndo, error) {
	var u EmailChangeUndo
	err := q.QueryRowContext(ctx, `SELECT user_id::text, old_email, new_email, created_at FROM email_change_undos
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`, tokenHash).Scan(&u.UserID, &u.OldEmail, &u.NewEmail, &u.ChangedAt)
	return u, err
}

// ErrUndoAddressTaken: the old address signs in to another account now.
var ErrUndoAddressTaken = errors.New("the old address belongs to another account now")

// UndoEmailChange puts the account back on the old address ("this wasn't
// me"), in one transaction: every API key is revoked (whoever made the change
// holds one), Google/GitHub sign-ins and connected apps added since the change
// are removed, pending changes, emailed codes for the address it had and
// idle-cleanup links end, and every undo link of the account is spent.
// sql.ErrNoRows when the link is unknown, used or expired; ErrUndoAddressTaken
// when another account signs in with the old address now.
func UndoEmailChange(ctx context.Context, database *sql.DB, tokenHash string) (EmailChangeUndo, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return EmailChangeUndo{}, err
	}
	defer tx.Rollback()
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
	return u, tx.Commit()
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
