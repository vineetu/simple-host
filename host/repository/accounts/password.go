package accounts

import (
	"context"
	"database/sql"
)

// PasswordAccount looks up a password account by its own login (not
// users.username, which a self sign-up never sets to the typed value: see
// CreatePasswordAccount and account_passwords.login). login must already be
// normalized (NormalizeLogin). sql.ErrNoRows when no account_passwords row
// has this login, including when an ordinary email-code/OAuth account's
// username happens to equal it -- that account was never a password
// account and this must never resolve to it.
func PasswordAccount(ctx context.Context, q Querier, login string) (User, string, bool, error) {
	var id, hash string
	var must bool
	err := q.QueryRowContext(ctx, `SELECT u.id, p.password_hash, p.must_change
 FROM account_passwords p JOIN users u ON u.id=p.user_id
 WHERE p.login=$1`, login).Scan(&id, &hash, &must)
	if err != nil {
		return User{}, "", false, err
	}
	user, err := GetUserByID(ctx, q, id)
	return user, hash, must, err
}

// PasswordHashByID loads the stored hash for an account by id (unlocked),
// for a caller that already knows the account, such as changing your own
// password. sql.ErrNoRows: the account has no password row.
func PasswordHashByID(ctx context.Context, q Querier, id string) (string, error) {
	var hash string
	err := q.QueryRowContext(ctx, `SELECT password_hash FROM account_passwords WHERE user_id=$1`, id).Scan(&hash)
	return hash, err
}

// PasswordLoginByID loads the stored login (what the person actually typed
// or was given, normalized) for an account by id, for display (GET /v1/me)
// instead of the account's possibly-synthetic users.username.
// sql.ErrNoRows: the account has no password row.
func PasswordLoginByID(ctx context.Context, q Querier, id string) (string, error) {
	var login string
	err := q.QueryRowContext(ctx, `SELECT login FROM account_passwords WHERE user_id=$1`, id).Scan(&login)
	return login, err
}

// PasswordLogins loads every password account's login, keyed by user id,
// for a listing that must show each one's actual login rather than a self
// sign-up's possibly-synthetic users.username (the admin Users page).
func PasswordLogins(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, login FROM account_passwords`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, login string
		if err := rows.Scan(&id, &login); err != nil {
			return nil, err
		}
		out[id] = login
	}
	return out, rows.Err()
}
func LockPassword(ctx context.Context, q Querier, id string) (string, bool, error) {
	var hash string
	var must bool
	err := q.QueryRowContext(ctx, `SELECT password_hash, must_change FROM account_passwords WHERE user_id=$1 FOR UPDATE`, id).Scan(&hash, &must)
	return hash, must, err
}

// SetPassword creates or replaces id's password row. login is used only
// when the row does not exist yet -- a brand-new password account, or an
// admin granting a password to an account that never had one -- and is
// never changed on an existing row (there is no login-change feature); a
// caller that is only ever changing an existing row's password, such as
// ChangePassword, may pass "".
func SetPassword(ctx context.Context, q Querier, id, hash, login string, must bool) error {
	_, err := q.ExecContext(ctx, `INSERT INTO account_passwords(user_id,password_hash,must_change,login) VALUES($1,$2,$3,$4)
 ON CONFLICT(user_id) DO UPDATE SET password_hash=$2,must_change=$3,updated_at=now()`, id, hash, must, login)
	return err
}
